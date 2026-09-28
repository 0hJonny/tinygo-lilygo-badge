// SPDX-License-Identifier: MIT
// Copyright (c) 2026 0hJonny
// The transaction and bus-recovery logic follows ESP-IDF components/driver/i2c.c
// (SPDX-FileCopyrightText: 2015-2024 Espressif Systems (Shanghai) CO LTD,
// Apache-2.0; see LICENSES/Apache-2.0.txt).

//go:build tinygo && esp32s3

// Package i2cfix implements I2C transactions for ESP32-S3 the way ESP-IDF does,
// working around problems in machine_esp32xx_i2c.go (TinyGo 0.41.1 and 0.42.0).
//
// Problems in TinyGo:
//  1. transmit() discards a NACK in any transaction that contains a read
//     (`&& !readLast`): a read from a device that does not acknowledge returns
//     nil and garbage from the empty FIFO. Confirmed on hardware (cmd/i2cbug,
//     test 2); the same bug was fixed for the classic ESP32 in tinygo#5584 /
//     #5585, and the copy of that fix to esp32xx was reverted in #5602.
//  2. READ commands set the ACK-check bit (i2cCMD_READ = 3<<11 | 1<<8), which
//     ESP-IDF does not do for reads. Whether this causes false NACKs is a
//     hypothesis and has not been verified.
//  3. After an error the FIFO and the state machine are not reset and the bus
//     is not cleared (resetMaster is only called from Configure).
//
// Here a transaction is built as in ESP-IDF v4.4 (driver/i2c.c):
// i2c_master_write_read_device → START, address+W, data (ack_en), RESTART,
// address+R (ack_en), READ n−1 (ACK), READ 1 (NACK, without ack_en, as in
// i2c_master_read_static), STOP. The result is taken from the flags as in
// i2c_isr_handler_default: TRANS_COMPLETE means success; NACK / TIME_OUT /
// ARBITRATION_LOST mean an error. Error handling follows i2c_master_cmd_begin:
// on TIME_OUT or ARBITRATION_LOST it does what i2c_hw_fsm_reset() does for S3
// (no SOC_I2C_SUPPORT_HW_FSM_RST/HW_CLR_BUS): a software bus clear
// (i2c_master_clear_bus) and a full re-initialization of the peripheral. On a
// NACK it only counts, and resets after I2C_ACKERR_CNT_MAX (10) NACKs.
//
// Clock, timing and pin setup is done by machine.I2C.Configure.
package i2cfix

import (
	"device"
	"device/esp"
	"errors"
	"machine"
	"runtime/volatile"
	"time"
	"unsafe"
)

// I2C command (i2c_hw_cmd_t in hal/esp32s3/include/hal/i2c_ll.h):
// byte_num[7:0], ack_en[8], ack_exp[9], ack_val[10], op_code[13:11].
const (
	cmdAckEn  = 1 << 8
	cmdAckVal = 1 << 10

	cmdRestart = 6 << 11 // I2C_LL_CMD_RESTART
	cmdWrite   = 1 << 11 // I2C_LL_CMD_WRITE
	cmdRead    = 3 << 11 // I2C_LL_CMD_READ
	cmdStop    = 2 << 11 // I2C_LL_CMD_STOP

	fifoSize = 32
	numCmds  = 8

	txTimeout = 50 * time.Millisecond

	intAll = 0x3ffff
)

var (
	errNACK        = errors.New("i2c: NACK")
	errTimeout     = errors.New("i2c: timeout")
	errArbitration = errors.New("i2c: arbitration lost")
	errTooLong     = errors.New("i2c: transaction longer than the FIFO")
)

// ReadAckCheck is for experiments only (cmd/i2cbug): also set the ACK-check bit
// on READ commands, as TinyGo does (i2cCMD_READ = 3<<11 | 1<<8). ESP-IDF does
// not set it. Default false.
var ReadAckCheck bool

// NoRecover is for experiments only (cmd/i2ctiming): do not recover the bus
// after an error, so that the controller state left by the error is observed
// as is. Default false.
var NoRecover bool

// Diag is a snapshot of the controller taken when a transaction fails, before
// the bus is recovered. It shows how far the transaction got.
type Diag struct {
	Cmds int    // number of commands in the transaction (COMD0…COMDn-1)
	SR   uint32 // SR at the moment of the error (bit 4 = BUS_BUSY)
	// BusyAtStart: SR.BUS_BUSY was set when this transaction started, i.e. the
	// previous one did not release the bus (ESP-IDF resets the FSM in that case).
	BusyAtStart bool
	Done        uint8    // bit i set: command i was executed (COMDi done bit, bit 31)
	IntRaw      uint32   // INT_RAW at the moment of the error
	RxCount     int      // bytes in the RX FIFO (SR.RXFIFO_CNT)
	Rx          [32]byte // the first RxCount bytes of the RX FIFO
}

// Bus implements drivers.I2C.
type Bus struct {
	i2c    *machine.I2C
	config machine.I2CConfig
	pullUp bool

	// Errors counts failed transactions; Recoveries counts bus recoveries.
	Errors, Recoveries int

	// Last is the diagnostic snapshot of the last failed transaction.
	Last Diag

	// BusyStarts counts transactions that started with SR.BUS_BUSY set.
	BusyStarts  int
	busyAtStart bool

	ackErrCnt int // clear_bus_cnt in i2c_master_cmd_begin
}

// ackErrCntMax is I2C_ACKERR_CNT_MAX in ESP-IDF driver/i2c.c.
const ackErrCntMax = 10

// New configures the bus. pullUp enables the internal SCL/SDA pull-ups (like
// sda_pullup_en/scl_pullup_en in the original's i2c_config_t); TinyGo does not
// enable them and clears them on every Configure, so they are set again.
func New(i2c *machine.I2C, config machine.I2CConfig, pullUp bool) *Bus {
	b := &Bus{i2c: i2c, config: config, pullUp: pullUp}
	b.configure()
	return b
}

// Wrap returns a Bus on an already configured machine.I2C without touching the
// peripheral (no Configure). For diagnostics: comparing i2cfix and machine.I2C
// transactions on the same controller state.
func Wrap(i2c *machine.I2C, config machine.I2CConfig, pullUp bool) *Bus {
	return &Bus{i2c: i2c, config: config, pullUp: pullUp}
}

func (b *Bus) configure() {
	b.i2c.Configure(b.config)
	if b.pullUp {
		ioMux(b.config.SCL).SetBits(esp.IO_MUX_GPIO_FUN_WPU)
		ioMux(b.config.SDA).SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	}
}

// Tx is a single transaction: write w, repeated START, read r.
func (b *Bus) Tx(addr uint16, w, r []byte) error {
	if len(w)+1 > fifoSize || len(r) > fifoSize {
		return errTooLong
	}
	hw := b.i2c.Bus

	// Record whether the bus is still busy from the previous transaction (IDF
	// i2c_master_cmd_begin resets the FSM here if it is; not done yet, diagnostics only).
	b.busyAtStart = hw.SR.Get()&esp.I2C_SR_BUS_BUSY != 0
	if b.busyAtStart {
		b.BusyStarts++
	}

	// Clean start: FIFO and flags (i2c_master_cmd_begin resets the FIFO).
	hw.FIFO_CONF.SetBits(esp.I2C_FIFO_CONF_TX_FIFO_RST | esp.I2C_FIFO_CONF_RX_FIFO_RST)
	hw.FIFO_CONF.ClearBits(esp.I2C_FIFO_CONF_TX_FIFO_RST | esp.I2C_FIFO_CONF_RX_FIFO_RST)
	hw.INT_CLR.Set(intAll)

	cmds := (*[numCmds]volatile.Register32)(unsafe.Pointer(&hw.COMD0))
	n := 0

	cmds[n].Set(cmdRestart) // the first RESTART is the START
	n++
	if len(w) > 0 || len(r) == 0 {
		hw.DATA.Set(uint32(addr&0x7f) << 1) // address + W
		for _, v := range w {
			hw.DATA.Set(uint32(v))
		}
		cmds[n].Set(cmdWrite | cmdAckEn | uint32(1+len(w)))
		n++
	}
	if len(r) > 0 {
		if len(w) > 0 {
			cmds[n].Set(cmdRestart)
			n++
		}
		hw.DATA.Set(uint32(addr&0x7f)<<1 | 1) // address + R
		cmds[n].Set(cmdWrite | cmdAckEn | 1)
		n++
		readFlags := uint32(0)
		if ReadAckCheck {
			readFlags = cmdAckEn
		}
		if len(r) > 1 {
			cmds[n].Set(cmdRead | readFlags | uint32(len(r)-1)) // ACK after each byte
			n++
		}
		cmds[n].Set(cmdRead | readFlags | cmdAckVal | 1) // the last byte gets a NACK
		n++
	}
	cmds[n].Set(cmdStop)
	n++

	hw.CTR.SetBits(esp.I2C_CTR_CONF_UPGATE)
	hw.CTR.SetBits(esp.I2C_CTR_TRANS_START)

	start := time.Now()
	for {
		raw := hw.INT_RAW.Get()
		switch {
		case raw&esp.I2C_INT_RAW_ARBITRATION_LOST_INT_RAW != 0:
			b.capture(cmds, n)
			return b.fail(errArbitration)
		case raw&esp.I2C_INT_RAW_NACK_INT_RAW != 0:
			b.capture(cmds, n)
			return b.nack()
		case raw&esp.I2C_INT_RAW_TIME_OUT_INT_RAW != 0:
			b.capture(cmds, n)
			return b.fail(errTimeout)
		case raw&esp.I2C_INT_RAW_TRANS_COMPLETE_INT_RAW != 0:
			for i := range r {
				r[i] = byte(hw.DATA.Get())
			}
			hw.INT_CLR.Set(intAll)
			return nil
		}
		if time.Since(start) > txTimeout {
			b.capture(cmds, n)
			return b.fail(errTimeout)
		}
	}
}

// capture records Diag for the failed transaction (before recovery).
func (b *Bus) capture(cmds *[numCmds]volatile.Register32, n int) {
	hw := b.i2c.Bus
	d := Diag{Cmds: n, IntRaw: hw.INT_RAW.Get(), SR: hw.SR.Get(), BusyAtStart: b.busyAtStart}
	for i := 0; i < n; i++ {
		if cmds[i].Get()&(1<<31) != 0 {
			d.Done |= 1 << i
		}
	}
	d.RxCount = int(hw.GetSR_RXFIFO_CNT())
	for i := 0; i < d.RxCount && i < len(d.Rx); i++ {
		d.Rx[i] = byte(hw.DATA.Get())
	}
	b.Last = d
}

// Recover clears the bus and re-initializes it (i2c_hw_fsm_reset for S3). Call
// it when a device driver gets clearly bad data.
func (b *Bus) Recover() {
	if NoRecover {
		return
	}
	b.clearBus()
	b.configure()
	b.Recoveries++
}

// fail handles TIME_OUT and ARBITRATION_LOST: IDF maps both to
// I2C_STATUS_TIMEOUT and calls i2c_hw_fsm_reset right away.
func (b *Bus) fail(err error) error {
	b.Errors++
	b.ackErrCnt = 0
	b.Recover()
	return err
}

// nack handles I2C_STATUS_ACK_ERROR as IDF does: count, and reset the bus only
// after ackErrCntMax NACKs (the counter is not cleared by a successful
// transaction, as in IDF).
func (b *Bus) nack() error {
	b.Errors++
	b.ackErrCnt++
	if b.ackErrCnt >= ackErrCntMax {
		b.ackErrCnt = 0
		b.Recover()
	}
	return errNACK
}

// Reconfigure re-initializes the peripheral only (machine.I2C.Configure plus
// the pull-ups), without the software bus clear. For diagnostics.
func (b *Bus) Reconfigure() {
	b.configure()
}

// ClearBus runs the software bus clear only (9 clocks while SDA is low, then a
// STOP). The pins are left as GPIOs: call Reconfigure afterwards. For
// diagnostics.
func (b *Bus) ClearBus() {
	b.clearBus()
}

// clearBus is i2c_master_clear_bus(): if a device holds SDA (interrupted in the
// middle of a byte during a read), give up to 9 SCL clocks until it releases
// SDA, then a STOP. The pins are open-drain GPIOs; 5 µs per half period (100 kHz).
func (b *Bus) clearBus() {
	const halfPeriodUs = 5 // I2C_CLR_BUS_HALF_PERIOD_US
	const maxClocks = 9    // I2C_CLR_BUS_SCL_NUM
	scl, sda := b.config.SCL, b.config.SDA
	openDrain(scl)
	openDrain(sda)

	scl.Low()
	sda.High()
	waitMicros(halfPeriodUs)
	for i := 0; !sda.Get() && i < maxClocks; i++ {
		scl.High()
		waitMicros(halfPeriodUs)
		scl.Low()
		waitMicros(halfPeriodUs)
	}
	sda.Low() // prepare for STOP
	scl.High()
	waitMicros(halfPeriodUs)
	sda.High() // STOP: SDA rises while SCL is high
}

// openDrain makes an open-drain GPIO output (gpio_set_direction(…_OD)).
func openDrain(p machine.Pin) {
	p.Configure(machine.PinConfig{Mode: machine.PinOutput})
	gpioPin(p).SetBits(esp.GPIO_PIN_PAD_DRIVER)
	ioMux(p).SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

func ioMux(p machine.Pin) *volatile.Register32 {
	return (*volatile.Register32)(unsafe.Add(unsafe.Pointer(&esp.IO_MUX.GPIO0), uintptr(p)*4))
}

func gpioPin(p machine.Pin) *volatile.Register32 {
	return (*volatile.Register32)(unsafe.Add(unsafe.Pointer(&esp.GPIO.PIN0), uintptr(p)*4))
}

func waitMicros(us uint32) {
	start := ccount()
	for ccount()-start < us*240 { // the TinyGo runtime runs the ESP32-S3 CPU at 240 MHz
	}
}

func ccount() uint32 {
	return uint32(device.AsmFull("rsr {}, ccount", nil))
}

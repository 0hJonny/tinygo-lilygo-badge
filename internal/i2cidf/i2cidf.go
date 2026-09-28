// SPDX-License-Identifier: MIT
// Copyright (c) 2026 0hJonny
// A port of the I2C master driver of ESP-IDF v4.4.8 (components/driver/i2c.c,
// components/hal/i2c_hal*.c, components/hal/esp32s3/include/hal/i2c_ll.h,
// components/driver/gpio.c; SPDX-FileCopyrightText: 2015-2022 Espressif
// Systems (Shanghai) CO LTD, Apache-2.0; see LICENSES/Apache-2.0.txt).

//go:build tinygo && esp32s3

// Package i2cidf is the ESP-IDF v4.4.8 I2C master driver for ESP32-S3, ported
// function by function, for comparison with machine.I2C on the same board.
//
// It does not use machine.I2C at all: pins, clock, timing and the transaction
// state machine follow ESP-IDF. The only structural difference is that the
// interrupt handler (i2c_isr_handler_default) is called from a polling loop
// instead of from an interrupt; it sees the same INT_STATUS (INT_RAW & INT_ENA).
//
// As in ESP-IDF, a transaction is not written to the command registers at
// once: i2c_master_cmd_begin_static runs one WRITE or READ command at a time,
// each followed by END, and continues from the END_DETECT interrupt.
package i2cidf

import (
	"device/esp"
	"errors"
	"machine"
	"runtime/volatile"
	"time"
	"unsafe"
)

// i2c_hw_cmd_t (hal/esp32s3/include/hal/i2c_ll.h:27–36):
// byte_num[7:0], ack_en[8], ack_exp[9], ack_val[10], op_code[13:11], done[31].
const (
	cmdAckEn  = 1 << 8
	cmdAckVal = 1 << 10

	opRestart = 6 << 11 // I2C_LL_CMD_RESTART
	opWrite   = 1 << 11 // I2C_LL_CMD_WRITE
	opRead    = 3 << 11 // I2C_LL_CMD_READ
	opStop    = 2 << 11 // I2C_LL_CMD_STOP
	opEnd     = 4 << 11 // I2C_LL_CMD_END
	opMask    = 7 << 11

	fifoLen = 32 // SOC_I2C_FIFO_LEN

	intrMask = 0x3fff // I2C_LL_INTR_MASK

	// I2C_LL_MASTER_TX_INT / I2C_LL_MASTER_RX_INT (i2c_ll.h:81–83).
	masterTxInt = esp.I2C_INT_ENA_NACK_INT_ENA | esp.I2C_INT_ENA_TIME_OUT_INT_ENA |
		esp.I2C_INT_ENA_TRANS_COMPLETE_INT_ENA | esp.I2C_INT_ENA_ARBITRATION_LOST_INT_ENA |
		esp.I2C_INT_ENA_END_DETECT_INT_ENA
	masterRxInt = esp.I2C_INT_ENA_TIME_OUT_INT_ENA | esp.I2C_INT_ENA_TRANS_COMPLETE_INT_ENA |
		esp.I2C_INT_ENA_ARBITRATION_LOST_INT_ENA | esp.I2C_INT_ENA_END_DETECT_INT_ENA

	filterCycNumDef = 7  // I2C_FILTER_CYC_NUM_DEF
	ackErrCntMax    = 10 // I2C_ACKERR_CNT_MAX

	// GPIO matrix signals (soc/esp32s3/include/soc/gpio_sig_map.h).
	sclSig = 89 // I2CEXT0_SCL_IN_IDX / I2CEXT0_SCL_OUT_IDX
	sdaSig = 90 // I2CEXT0_SDA_IN_IDX / I2CEXT0_SDA_OUT_IDX

	xtalFreq = 40000000 // I2C_LL_CLK_SRC_FREQ(I2C_SCLK_XTAL)
)

// i2c_status_t (driver/i2c.c:125–133).
type status uint8

const (
	statusIdle status = iota
	statusDone
	statusWrite
	statusRead
	statusAckError
	statusTimeout
)

// i2c_intr_event_t (i2c_ll.h:43–52); the order matters (event < endDet).
type event uint8

const (
	evtErr event = iota
	evtArbitLost
	evtNACK
	evtTout
	evtEndDet
	evtTransDone
)

var (
	ErrFail    = errors.New("i2c: ESP_FAIL (NACK)")
	ErrTimeout = errors.New("i2c: ESP_ERR_TIMEOUT")
	errTooLong = errors.New("i2c: too many commands")
)

// Config is i2c_config_t for master mode.
type Config struct {
	SDA, SCL             machine.Pin
	SDAPullup, SCLPullup bool
	ClkSpeed             uint32

	// Experiment switches (cmd/i2cbisect): each one makes one part behave the
	// way machine.I2C of TinyGo 0.42.0 does. All false: ESP-IDF.
	TinyGoInit   bool // reset pulse on enable + resetMaster() after init
	TinyGoTiming bool // initFrequency() timing, TO = 0x10 (hardware timeout off)
	TinyGoDrive  bool // IO_MUX drive strength 1 on SCL/SDA (Pin.configure)
	Whole        bool // the whole transaction in COMD0–7 at once, one TRANS_START
	MergeAddr    bool // with Whole: address and data in one WRITE command

	// Finer switches: single parts of TinyGoInit, and END after the first WRITE.
	InitPulse    bool // enableI2C0PeriphClock reset pulse only
	InitFSMRst   bool // CTR.FSM_RST = 1 only
	InitClkRst   bool // SCL_RST_SLV: 9 SCL clocks only
	InitStretch  bool // SCL_STRETCH_CONF.SLAVE_SCL_STRETCH_EN = 1 only
	EndAfterAddr bool // with Whole: END after the first WRITE, the rest after END_DETECT
}

// cmd is i2c_cmd_t.
type cmd struct {
	hw        uint32 // i2c_hw_cmd_t
	data      []byte // data pointer, or data_byte in data[0] for a single byte
	bytesUsed int
	total     int
}

// Bus is the state of one I2C port: i2c_obj_t plus i2c_context_t.
type Bus struct {
	hw  *esp.I2C_Type
	cfg Config

	hwEnabled bool
	status    status
	cmdIdx    int
	rxCnt     int

	cmds [8]cmd
	wb   [8]byte // data_byte storage of single-byte WRITE commands
	next int     // Whole+EndAfterAddr: first command of the second part
	n    int     // number of commands in the link
	head int     // cmd_link.head

	clearBusCnt int

	// Timeout is ticks_to_wait of i2c_master_cmd_begin (the badge uses 10 ms).
	Timeout time.Duration

	// Diagnostics.
	Errors, FSMResets int
	LastSR, LastRaw   uint32
}

// New does i2c_param_config + i2c_driver_install for I2C0 in master mode.
func New(cfg Config) *Bus {
	b := &Bus{hw: esp.I2C0, cfg: cfg, Timeout: 10 * time.Millisecond}
	b.paramConfig()
	// i2c_driver_install (driver/i2c.c:352–357): i2c_hw_enable (no-op, already
	// enabled), disable and clear interrupts; the ISR is our polling loop.
	b.hwEnable()
	b.hw.INT_ENA.ClearBits(intrMask)
	b.hw.INT_CLR.Set(intrMask)
	b.status = statusIdle
	return b
}

// paramConfig is i2c_param_config (driver/i2c.c:673–725), master mode.
func (b *Bus) paramConfig() {
	// src_clk = i2c_get_clk_src(): on S3 the RTC clock is skipped, XTAL (40 MHz).
	b.setPin()
	if b.cfg.TinyGoDrive {
		ioMux(b.cfg.SDA).ReplaceBits(1, 3, esp.IO_MUX_GPIO_FUN_DRV_Pos)
		ioMux(b.cfg.SCL).ReplaceBits(1, 3, esp.IO_MUX_GPIO_FUN_DRV_Pos)
	}
	if b.cfg.TinyGoInit || b.cfg.InitPulse {
		// enableI2C0PeriphClock (machine_esp32s3_i2c.go): reset pulse.
		esp.SYSTEM.SetPERIP_RST_EN0_I2C_EXT0_RST(1)
		esp.SYSTEM.SetPERIP_CLK_EN0_I2C_EXT0_CLK_EN(1)
		esp.SYSTEM.SetPERIP_RST_EN0_I2C_EXT0_RST(0)
		b.hwEnabled = true
	}
	b.hwEnable()
	b.hw.INT_ENA.ClearBits(intrMask) // i2c_hal_disable_intr_mask
	b.hw.INT_CLR.Set(intrMask)       // i2c_hal_clr_intsts_mask
	b.masterInit()
	b.setFilter(filterCycNumDef)
	b.setBusTiming(b.cfg.ClkSpeed)
	if b.cfg.TinyGoTiming {
		b.tinygoTiming(b.cfg.ClkSpeed)
	}
	b.hw.SetCTR_CONF_UPGATE(1) // i2c_hal_update_config
	if b.cfg.TinyGoInit {
		b.tinygoResetMaster()
	}
	if b.cfg.InitFSMRst {
		b.hw.SetCTR_FSM_RST(1)
		b.hw.SetCTR_CONF_UPGATE(1)
	}
	if b.cfg.InitClkRst {
		b.hw.SetSCL_SP_CONF_SCL_RST_SLV_NUM(9)
		b.hw.SetSCL_SP_CONF_SCL_RST_SLV_EN(1)
		b.hw.SetCTR_CONF_UPGATE(1)
		for b.hw.GetSCL_SP_CONF_SCL_RST_SLV_EN() != 0 {
		}
		b.hw.SetSCL_SP_CONF_SCL_RST_SLV_NUM(0)
	}
	if b.cfg.InitStretch {
		b.hw.SetSCL_STRETCH_CONF_SLAVE_SCL_STRETCH_EN(1)
		b.hw.SetCTR_CONF_UPGATE(1)
	}
}

// tinygoTiming is initFrequency() and the TO setting of startMaster() in
// machine_esp32xx_i2c.go (TinyGo 0.42.0).
func (b *Bus) tinygoTiming(freq uint32) {
	clkmDiv := xtalFreq/(freq*1024) + 1
	sclkFreq := xtalFreq / clkmDiv
	half := sclkFreq / freq / 2
	sclWaitHigh := uint32(0)
	if freq > 50000 {
		sclWaitHigh = half / 8
	}
	b.hw.SetCLK_CONF_SCLK_DIV_NUM(xtalFreq / (freq * 1024))
	b.hw.SetSCL_LOW_PERIOD(half - 1)
	b.hw.SetSCL_HIGH_PERIOD(half - sclWaitHigh)
	b.hw.SetSCL_HIGH_PERIOD_SCL_WAIT_HIGH_PERIOD(25)
	b.hw.SetSCL_RSTART_SETUP_TIME(half)
	b.hw.SetSCL_STOP_SETUP_TIME(half)
	b.hw.SetSCL_START_HOLD_TIME(half - 1)
	b.hw.SetSCL_STOP_HOLD_TIME(half - 1)
	b.hw.SetSDA_SAMPLE_TIME(half / 2)
	b.hw.SetSDA_HOLD_TIME(half / 4)
	b.hw.TO.Set(0x10)
}

// tinygoResetMaster is resetMaster() in machine_esp32xx_i2c.go (TinyGo 0.42.0).
func (b *Bus) tinygoResetMaster() {
	b.hw.SetCTR_FSM_RST(1)
	b.hw.SetSCL_SP_CONF_SCL_RST_SLV_NUM(9)
	b.hw.SetSCL_SP_CONF_SCL_RST_SLV_EN(1)
	b.hw.SetSCL_STRETCH_CONF_SLAVE_SCL_STRETCH_EN(1)
	b.hw.SetCTR_CONF_UPGATE(1)
	b.hw.FILTER_CFG.Set(0x377)
	for b.hw.GetSCL_SP_CONF_SCL_RST_SLV_EN() != 0 {
	}
	b.hw.SetSCL_SP_CONF_SCL_RST_SLV_NUM(0)
}

// ResetModule disables the I2C0 module (clock off, reset on), so that the next
// New starts from the reset state. For experiments.
func ResetModule() {
	esp.SYSTEM.SetPERIP_CLK_EN0_I2C_EXT0_CLK_EN(0)
	esp.SYSTEM.SetPERIP_RST_EN0_I2C_EXT0_RST(1)
}

// setPin is i2c_set_pin (driver/i2c.c:867–919), master mode.
func (b *Bus) setPin() {
	b.setPinOne(b.cfg.SDA, sdaSig, b.cfg.SDAPullup)
	b.setPinOne(b.cfg.SCL, sclSig, b.cfg.SCLPullup)
}

func (b *Bus) setPinOne(p machine.Pin, sig uint32, pullup bool) {
	gpioSetLevel(p, true)                                   // gpio_set_level(io, I2C_IO_INIT_LEVEL)
	ioMux(p).ReplaceBits(1, 7, esp.IO_MUX_GPIO_MCU_SEL_Pos) // gpio_hal_iomux_func_sel(PIN_FUNC_GPIO)
	// gpio_set_direction(io, GPIO_MODE_INPUT_OUTPUT_OD) (driver/gpio.c:273–305).
	ioMux(p).SetBits(esp.IO_MUX_GPIO_FUN_IE)    // gpio_input_enable
	gpioOutputEnable(p)                         // gpio_output_enable
	outSel(p).Set(256)                          // esp_rom_gpio_connect_out_signal(io, SIG_GPIO_OUT_IDX)
	gpioPin(p).SetBits(esp.GPIO_PIN_PAD_DRIVER) // gpio_od_enable
	// gpio_set_pull_mode: GPIO_PULLUP_ONLY or GPIO_FLOATING (S3:
	// SOC_GPIO_SUPPORT_RTC_INDEPENDENT, so the IO_MUX bits are used).
	ioMux(p).ClearBits(esp.IO_MUX_GPIO_FUN_WPD)
	if pullup {
		ioMux(p).SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	} else {
		ioMux(p).ClearBits(esp.IO_MUX_GPIO_FUN_WPU)
	}
	outSel(p).Set(sig)                                                                            // esp_rom_gpio_connect_out_signal(io, sig, 0, 0)
	inSel(sig).Set(esp.GPIO_FUNC_IN_SEL_CFG_SEL | uint32(p)<<esp.GPIO_FUNC_IN_SEL_CFG_IN_SEL_Pos) // esp_rom_gpio_connect_in_signal(io, sig, 0)
}

// hwEnable is i2c_hw_enable → periph_module_enable → periph_ll_enable_clk_clear_rst.
func (b *Bus) hwEnable() {
	if !b.hwEnabled {
		esp.SYSTEM.SetPERIP_CLK_EN0_I2C_EXT0_CLK_EN(1)
		esp.SYSTEM.SetPERIP_RST_EN0_I2C_EXT0_RST(0)
		b.hwEnabled = true
	}
}

// hwDisable is i2c_hw_disable → periph_module_disable → periph_ll_disable_clk_set_rst.
func (b *Bus) hwDisable() {
	if b.hwEnabled {
		esp.SYSTEM.SetPERIP_CLK_EN0_I2C_EXT0_CLK_EN(0)
		esp.SYSTEM.SetPERIP_RST_EN0_I2C_EXT0_RST(1)
		b.hwEnabled = false
	}
}

// masterInit is i2c_hal_master_init (hal/i2c_hal.c:189–200).
func (b *Bus) masterInit() {
	// i2c_ll_master_init: ctr = ms_mode | clk_en | sda_force_out | scl_force_out.
	b.hw.CTR.Set(esp.I2C_CTR_MS_MODE | esp.I2C_CTR_CLK_EN | esp.I2C_CTR_SDA_FORCE_OUT | esp.I2C_CTR_SCL_FORCE_OUT)
	b.hw.SetFIFO_CONF_NONFIFO_EN(0) // i2c_ll_set_fifo_mode(true)
	b.hw.SetCTR_TX_LSB_FIRST(0)     // i2c_ll_set_data_mode(MSB, MSB)
	b.hw.SetCTR_RX_LSB_FIRST(0)
	b.txfifoRst()
	b.rxfifoRst()
}

func (b *Bus) txfifoRst() {
	b.hw.SetFIFO_CONF_TX_FIFO_RST(1)
	b.hw.SetFIFO_CONF_TX_FIFO_RST(0)
}

func (b *Bus) rxfifoRst() {
	b.hw.SetFIFO_CONF_RX_FIFO_RST(1)
	b.hw.SetFIFO_CONF_RX_FIFO_RST(0)
}

// setFilter is i2c_ll_set_filter (i2c_ll.h:654–665).
func (b *Bus) setFilter(n uint32) {
	if n > 0 {
		b.hw.SetFILTER_CFG_SCL_FILTER_THRES(n)
		b.hw.SetFILTER_CFG_SDA_FILTER_THRES(n)
		b.hw.SetFILTER_CFG_SCL_FILTER_EN(1)
		b.hw.SetFILTER_CFG_SDA_FILTER_EN(1)
	} else {
		b.hw.SetFILTER_CFG_SCL_FILTER_EN(0)
		b.hw.SetFILTER_CFG_SDA_FILTER_EN(0)
	}
}

// setBusTiming is i2c_hal_set_bus_timing (hal/i2c_hal.c:157–165) with
// i2c_ll_cal_bus_clk (i2c_ll.h:105–130) and i2c_ll_set_bus_timing (152–173).
func (b *Bus) setBusTiming(freq uint32) {
	b.hw.SetCLK_CONF_SCLK_SEL(0) // i2c_ll_set_source_clk(XTAL)
	clkmDiv := xtalFreq/(freq*1024) + 1
	sclkFreq := xtalFreq / clkmDiv
	half := sclkFreq / freq / 2
	sclLow := half
	var sclWaitHigh uint32
	if freq >= 80*1000 {
		sclWaitHigh = half/2 - 2
	} else {
		sclWaitHigh = half / 4
	}
	sclHigh := half - sclWaitHigh
	sdaHold := half / 4
	sdaSample := half / 2
	setup := half
	hold := half
	// tout = (sizeof(half_cycle)*8 - __builtin_clz(5*half_cycle)) + 2
	tout := uint32(32-clz32(5*half)) + 2

	b.hw.SetCLK_CONF_SCLK_DIV_NUM(clkmDiv - 1)
	b.hw.SetSCL_LOW_PERIOD(sclLow - 1)
	b.hw.SetSCL_HIGH_PERIOD(sclHigh)
	b.hw.SetSCL_HIGH_PERIOD_SCL_WAIT_HIGH_PERIOD(sclWaitHigh)
	b.hw.SetSDA_HOLD_TIME(sdaHold - 1)
	b.hw.SetSDA_SAMPLE_TIME(sdaSample - 1)
	b.hw.SetSCL_RSTART_SETUP_TIME(setup - 1)
	b.hw.SetSCL_STOP_SETUP_TIME(setup - 1)
	b.hw.SetSCL_START_HOLD_TIME(hold - 1)
	b.hw.SetSCL_STOP_HOLD_TIME(hold - 1)
	b.hw.SetTO_TIME_OUT_VALUE(tout)
	b.hw.SetTO_TIME_OUT_EN(1)
}

func clz32(v uint32) int {
	n := 0
	for i := 31; i >= 0 && v&(1<<i) == 0; i-- {
		n++
	}
	return n
}

// fsmReset is i2c_hw_fsm_reset (driver/i2c.c:617–655), branch
// !SOC_I2C_SUPPORT_HW_FSM_RST (true for S3 in v4.4.8).
func (b *Bus) fsmReset() {
	b.FSMResets++
	sclHigh := b.hw.GetSCL_HIGH_PERIOD()
	sclWaitHigh := b.hw.GetSCL_HIGH_PERIOD_SCL_WAIT_HIGH_PERIOD()
	sclLow := b.hw.GetSCL_LOW_PERIOD()
	rstartSetup := b.hw.GetSCL_RSTART_SETUP_TIME()
	startHold := b.hw.GetSCL_START_HOLD_TIME() + 1 // i2c_ll_get_start_timing
	stopSetup := b.hw.GetSCL_STOP_SETUP_TIME()
	stopHold := b.hw.GetSCL_STOP_HOLD_TIME()
	sdaSample := b.hw.GetSDA_SAMPLE_TIME()
	sdaHold := b.hw.GetSDA_HOLD_TIME()
	tout := b.hw.GetTO_TIME_OUT_VALUE()
	filter := b.hw.GetFILTER_CFG_SCL_FILTER_THRES()

	b.hwDisable()
	b.clearBus()
	b.hwEnable()

	b.masterInit()
	b.hw.INT_ENA.ClearBits(intrMask)
	b.hw.INT_CLR.Set(intrMask)
	// i2c_ll_set_scl_clk_timing, set_start_timing, set_stop_timing,
	// set_sda_timing, set_tout (value only), set_filter.
	b.hw.SetSCL_LOW_PERIOD(sclLow)
	b.hw.SetSCL_HIGH_PERIOD(sclHigh)
	b.hw.SetSCL_HIGH_PERIOD_SCL_WAIT_HIGH_PERIOD(sclWaitHigh)
	b.hw.SetSCL_RSTART_SETUP_TIME(rstartSetup)
	b.hw.SetSCL_START_HOLD_TIME(startHold - 1)
	b.hw.SetSCL_STOP_SETUP_TIME(stopSetup)
	b.hw.SetSCL_STOP_HOLD_TIME(stopHold)
	b.hw.SetSDA_HOLD_TIME(sdaHold)
	b.hw.SetSDA_SAMPLE_TIME(sdaSample)
	b.hw.SetTO_TIME_OUT_VALUE(tout)
	b.setFilter(filter)
}

// clearBus is i2c_master_clear_bus (driver/i2c.c:579–611), branch
// SOC_I2C_SUPPORT_HW_CLR_BUS (defined for S3 in v4.4.8):
// i2c_ll_master_clr_bus (i2c_ll.h:855–861). In fsmReset it runs while the
// module is disabled, as in ESP-IDF.
func (b *Bus) clearBus() {
	b.hw.SetSCL_SP_CONF_SCL_RST_SLV_NUM(9)
	b.hw.SetSCL_SP_CONF_SCL_RST_SLV_EN(0)
	b.hw.SetCTR_CONF_UPGATE(1)
	b.hw.SetSCL_SP_CONF_SCL_RST_SLV_EN(1)
}

// Link building: i2c_master_start/write_byte/write/read/stop (driver/i2c.c:1170–1290).

func (b *Bus) reset() { b.n = 0 }

func (b *Bus) add(c cmd) error {
	if b.n >= len(b.cmds) {
		return errTooLong
	}
	b.cmds[b.n] = c
	b.n++
	return nil
}

func (b *Bus) start() error { return b.add(cmd{hw: opRestart}) }
func (b *Bus) stop() error  { return b.add(cmd{hw: opStop}) }

func (b *Bus) writeByte(v byte, ackEn bool) error {
	hw := uint32(opWrite)
	if ackEn {
		hw |= cmdAckEn
	}
	b.wb[b.n] = v
	return b.add(cmd{hw: hw, data: b.wb[b.n : b.n+1], total: 1})
}

func (b *Bus) write(data []byte, ackEn bool) error {
	if len(data) == 1 {
		return b.writeByte(data[0], ackEn)
	}
	hw := uint32(opWrite)
	if ackEn {
		hw |= cmdAckEn
	}
	return b.add(cmd{hw: hw, data: data, total: len(data)})
}

// read is i2c_master_read(..., I2C_MASTER_LAST_NACK).
func (b *Bus) read(data []byte) error {
	if len(data) > 1 {
		// i2c_master_read_static(data, len-1, I2C_MASTER_ACK): ack_val = 0.
		if err := b.add(cmd{hw: opRead, data: data[:len(data)-1], total: len(data) - 1}); err != nil {
			return err
		}
	}
	// i2c_master_read_byte(data+len-1, I2C_MASTER_NACK): ack_val = 1.
	return b.add(cmd{hw: opRead | cmdAckVal, data: data[len(data)-1:], total: 1})
}

// Tx implements drivers.I2C: i2c_master_write_read_device,
// i2c_master_write_to_device or i2c_master_read_from_device (driver/i2c.c:921–1035).
func (b *Bus) Tx(addr uint16, w, r []byte) error {
	b.reset()
	a := byte(addr&0x7f) << 1
	var err error
	switch {
	case len(w) > 0 && len(r) > 0:
		err = firstErr(b.start(), b.writeByte(a, true), b.write(w, true),
			b.start(), b.writeByte(a|1, true), b.read(r), b.stop())
	case len(r) > 0:
		err = firstErr(b.start(), b.writeByte(a|1, true), b.read(r), b.stop())
	default:
		err = b.start()
		if err == nil {
			err = b.writeByte(a, true)
		}
		if err == nil && len(w) > 0 {
			err = b.write(w, true)
		}
		if err == nil {
			err = b.stop()
		}
	}
	if err != nil {
		return err
	}
	return b.cmdBegin(b.Timeout)
}

func firstErr(errs ...error) error {
	for _, e := range errs {
		if e != nil {
			return e
		}
	}
	return nil
}

// cmdBegin is i2c_master_cmd_begin (driver/i2c.c:1423–1535).
func (b *Bus) cmdBegin(ticksToWait time.Duration) error {
	start := time.Now()
	if b.status == statusTimeout || b.hw.SR.Get()&esp.I2C_SR_BUS_BUSY != 0 {
		b.fsmReset()
		b.clearBusCnt = 0
	}
	b.txfifoRst()
	b.rxfifoRst()
	b.cmds[0].bytesUsed = 0
	b.head = 0
	b.status = statusIdle
	b.cmdIdx = 0
	b.rxCnt = 0
	b.txfifoRst()
	b.rxfifoRst()
	b.hw.INT_ENA.ClearBits(intrMask)
	b.hw.INT_CLR.Set(intrMask)
	if b.cfg.Whole {
		b.wholeStart()
	} else {
		b.beginStatic()
	}

	var ret error
	for {
		// The interrupt handler, polled.
		done := false
		if b.cfg.Whole {
			done = b.wholePoll()
		} else {
			done = b.isr()
		}
		if done {
			switch b.status {
			case statusTimeout:
				b.snapshot()
				b.fsmReset()
				b.clearBusCnt = 0
				ret = ErrTimeout
			case statusAckError:
				b.snapshot()
				b.clearBusCnt++
				if b.clearBusCnt >= ackErrCntMax {
					b.clearBusCnt = 0
					b.fsmReset()
				}
				ret = ErrFail
			}
			break
		}
		if time.Since(start) >= ticksToWait {
			b.snapshot()
			ret = ErrTimeout
			b.fsmReset()
			b.clearBusCnt = 0
			break
		}
	}
	b.status = statusDone
	if ret != nil {
		b.Errors++
	}
	return ret
}

func (b *Bus) snapshot() {
	b.LastSR = b.hw.SR.Get()
	b.LastRaw = b.hw.INT_RAW.Get()
}

// isr is i2c_isr_handler_default (driver/i2c.c:483–525), master part. It
// returns true when i2c_master_cmd_begin_static posted I2C_CMD_EVT_DONE.
func (b *Bus) isr() bool {
	if b.hw.INT_STATUS.Get() == 0 {
		return false
	}
	evt := evtErr
	switch b.status {
	case statusWrite:
		evt = b.handleEvent(masterTxInt)
	case statusRead:
		evt = b.handleEvent(masterRxInt)
	}
	switch evt {
	case evtNACK:
		b.status = statusAckError
		return b.beginStatic()
	case evtTout, evtArbitLost:
		b.status = statusTimeout
		return b.beginStatic()
	case evtEndDet:
		return b.beginStatic()
	case evtTransDone:
		if b.status != statusAckError && b.status != statusIdle {
			return b.beginStatic()
		}
	}
	return false
}

// handleEvent is i2c_hal_master_handle_tx_event / _rx_event (hal/i2c_hal_iram.c).
func (b *Bus) handleEvent(mask uint32) event {
	st := b.hw.INT_STATUS.Get()
	if st == 0 {
		return evtErr
	}
	// i2c_ll_master_get_event (i2c_ll.h:885–901).
	var evt event
	switch {
	case st&esp.I2C_INT_STATUS_ARBITRATION_LOST_INT_ST != 0:
		evt = evtArbitLost
	case st&esp.I2C_INT_STATUS_NACK_INT_ST != 0:
		evt = evtNACK
	case st&esp.I2C_INT_STATUS_TIME_OUT_INT_ST != 0:
		evt = evtTout
	case st&esp.I2C_INT_STATUS_END_DETECT_INT_ST != 0:
		evt = evtEndDet
	case st&esp.I2C_INT_STATUS_TRANS_COMPLETE_INT_ST != 0:
		evt = evtTransDone
	default:
		evt = evtErr
	}
	if evt < evtEndDet || evt == evtTransDone {
		b.hw.INT_ENA.ClearBits(mask) // i2c_ll_master_disable_tx_it / _rx_it
		b.hw.INT_CLR.Set(mask)       // i2c_ll_master_clr_tx_it / _rx_it
	} else if evt == evtEndDet {
		b.hw.INT_CLR.Set(mask)
	}
	return evt
}

// beginStatic is i2c_master_cmd_begin_static (driver/i2c.c:1296–1395). It
// returns true when it posts I2C_CMD_EVT_DONE.
func (b *Bus) beginStatic() bool {
	if b.head < b.n && b.status == statusRead {
		c := &b.cmds[b.head]
		for i := 0; i < b.rxCnt; i++ { // i2c_ll_read_rxfifo
			c.data[c.bytesUsed+i] = byte(b.hw.GetDATA_FIFO_RDATA())
		}
		c.bytesUsed += b.rxCnt
		if c.bytesUsed != c.total {
			b.cmdIdx = 0
		} else {
			b.head++
			if b.head < b.n {
				b.cmds[b.head].bytesUsed = 0
			}
		}
	} else if b.status == statusAckError || b.status == statusTimeout {
		return true
	} else if b.status == statusDone {
		return false
	}

	if b.head >= b.n {
		b.status = statusIdle
		return true
	}

	for b.head < b.n {
		c := &b.cmds[b.head]
		remaining := c.total - c.bytesUsed
		hw := c.hw
		switch c.hw & opMask {
		case opWrite:
			fill := 1
			if c.total != 1 {
				fill = min(remaining, fifoLen)
			}
			for i := 0; i < fill; i++ { // i2c_ll_write_txfifo
				b.hw.DATA.Set(uint32(c.data[c.bytesUsed+i]))
			}
			if c.total != 1 {
				c.bytesUsed += fill
			}
			hw = hw&^0xff | uint32(fill)
			b.writeCmd(b.cmdIdx, hw)
			b.writeCmd(b.cmdIdx+1, opEnd)
			b.hw.INT_CLR.Set(0xffffffff) // i2c_ll_master_enable_tx_it
			b.hw.INT_ENA.Set(masterTxInt)
			b.cmdIdx = 0
			if c.total == 1 || c.total == c.bytesUsed {
				b.head++
				if b.head < b.n {
					b.cmds[b.head].bytesUsed = 0
				}
			}
			b.status = statusWrite
		case opRead:
			fill := min(remaining, fifoLen)
			b.rxCnt = fill
			hw = hw&^0xff | uint32(fill)
			b.writeCmd(b.cmdIdx, hw)
			b.writeCmd(b.cmdIdx+1, opEnd)
			b.hw.INT_CLR.Set(0xffffffff) // i2c_ll_master_enable_rx_it
			b.hw.INT_ENA.Set(masterRxInt)
			b.status = statusRead
		default:
			b.writeCmd(b.cmdIdx, hw)
			b.cmdIdx++
			b.head++
			if b.head >= b.n || b.cmdIdx >= 15 {
				b.cmdIdx = 0
				break
			}
			continue
		}
		break
	}
	b.hw.SetCTR_CONF_UPGATE(1) // i2c_hal_update_config
	b.hw.SetCTR_TRANS_START(1) // i2c_hal_trans_start
	return false
}

func (b *Bus) writeCmd(idx int, v uint32) {
	cmds := (*[8]volatile.Register32)(unsafe.Pointer(&b.hw.COMD0))
	cmds[idx].Set(v)
}

// GPIO helpers (hal/esp32s3/include/hal/gpio_ll.h).

func ioMux(p machine.Pin) *volatile.Register32 {
	return (*volatile.Register32)(unsafe.Add(unsafe.Pointer(&esp.IO_MUX.GPIO0), uintptr(p)*4))
}

func gpioPin(p machine.Pin) *volatile.Register32 {
	return (*volatile.Register32)(unsafe.Add(unsafe.Pointer(&esp.GPIO.PIN0), uintptr(p)*4))
}

func outSel(p machine.Pin) *volatile.Register32 {
	return (*volatile.Register32)(unsafe.Add(unsafe.Pointer(&esp.GPIO.FUNC0_OUT_SEL_CFG), uintptr(p)*4))
}

func inSel(sig uint32) *volatile.Register32 {
	return (*volatile.Register32)(unsafe.Add(unsafe.Pointer(&esp.GPIO.FUNC0_IN_SEL_CFG), uintptr(sig)*4))
}

func gpioSetLevel(p machine.Pin, high bool) {
	bit := uint32(1) << (uint32(p) % 32)
	switch {
	case p < 32 && high:
		esp.GPIO.OUT_W1TS.Set(bit)
	case p < 32:
		esp.GPIO.OUT_W1TC.Set(bit)
	case high:
		esp.GPIO.OUT1_W1TS.Set(bit)
	default:
		esp.GPIO.OUT1_W1TC.Set(bit)
	}
}

func gpioOutputEnable(p machine.Pin) {
	if p < 32 {
		esp.GPIO.ENABLE_W1TS.Set(1 << uint32(p))
	} else {
		esp.GPIO.ENABLE1_W1TS.Set(1 << (uint32(p) - 32))
	}
}

// wholeStart writes the whole command list to COMD0–7 at once and starts it
// (experiment: the way machine.I2C and i2cfix do it). With MergeAddr a
// single-byte address WRITE is merged with the following WRITE.
func (b *Bus) wholeStart() {
	b.wholeFrom(0)
}

// wholeFrom writes commands from index `from`; with EndAfterAddr it stops
// after the first WRITE with an END and remembers where to continue.
func (b *Bus) wholeFrom(from int) {
	idx := 0
	b.next = 0
	for i := from; i < b.n; i++ {
		c := &b.cmds[i]
		switch c.hw & opMask {
		case opWrite:
			hw := c.hw&^0xff | uint32(c.total)
			for _, v := range c.data[:c.total] {
				b.hw.DATA.Set(uint32(v))
			}
			if b.cfg.MergeAddr && c.total == 1 && i+1 < b.n && b.cmds[i+1].hw&opMask == opWrite {
				n := b.cmds[i+1]
				for _, v := range n.data[:n.total] {
					b.hw.DATA.Set(uint32(v))
				}
				hw = c.hw&^0xff | uint32(c.total+n.total)
				i++
			}
			b.writeCmd(idx, hw)
			if b.cfg.EndAfterAddr && from == 0 {
				b.writeCmd(idx+1, opEnd)
				b.next = i + 1
				i = b.n // stop here
				continue
			}
		case opRead:
			b.writeCmd(idx, c.hw&^0xff|uint32(c.total))
		default:
			b.writeCmd(idx, c.hw)
		}
		idx++
	}
	b.hw.INT_CLR.Set(0xffffffff)
	b.hw.INT_ENA.Set(masterTxInt)
	b.status = statusWrite
	b.hw.SetCTR_CONF_UPGATE(1)
	b.hw.SetCTR_TRANS_START(1)
}

// wholePoll checks the flags of a whole transaction; true when it has ended.
func (b *Bus) wholePoll() bool {
	st := b.hw.INT_STATUS.Get()
	switch {
	case st == 0:
		return false
	case st&esp.I2C_INT_STATUS_ARBITRATION_LOST_INT_ST != 0, st&esp.I2C_INT_STATUS_TIME_OUT_INT_ST != 0:
		b.status = statusTimeout
	case st&esp.I2C_INT_STATUS_NACK_INT_ST != 0:
		b.status = statusAckError
	case st&esp.I2C_INT_STATUS_END_DETECT_INT_ST != 0 && b.next > 0:
		b.hw.INT_CLR.Set(masterTxInt)
		b.wholeFrom(b.next)
		return false
	case st&esp.I2C_INT_STATUS_TRANS_COMPLETE_INT_ST != 0:
		for i := 0; i < b.n; i++ {
			c := &b.cmds[i]
			if c.hw&opMask == opRead {
				for j := 0; j < c.total; j++ {
					c.data[j] = byte(b.hw.GetDATA_FIFO_RDATA())
				}
			}
		}
		b.status = statusIdle
	default:
		b.hw.INT_CLR.Set(st)
		return false
	}
	b.hw.INT_ENA.ClearBits(masterTxInt)
	b.hw.INT_CLR.Set(masterTxInt)
	return true
}

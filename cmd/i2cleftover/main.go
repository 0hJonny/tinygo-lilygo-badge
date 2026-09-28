// Test: do bytes left in the TX FIFO after a NACK go out in the next
// machine.I2C transaction? (Review of the esp32xx I2C PR: transmit() returns on
// NACK without resetting the FIFOs.)
//
// A data NACK is made on purpose at register level: one WRITE command with the
// PCF8563 address and three 0x84 bytes, with ack_check_en and ack_exp = 1. The
// PCF8563 ACKs its address, the controller expects a NACK, raises NACK_INT and
// stops, and the three bytes stay in the TX FIFO. 0x84 is the write address of
// 0x42, where there is no device, so if such a byte goes out as an address
// nothing on the bus accepts it and no register is written.
//
// Then machine.I2C0 reads the PCF8563 (register 0x00, 2 bytes) twice.
// The same program is built without and with the FIFO reset at the start of
// transmit(); the header says which.
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/i2cidf"
)

var (
	cfg = machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	i2c = machine.I2C0
)

func hexBytes(buf []byte) string {
	const hexd = "0123456789abcdef"
	h := ""
	for _, b := range buf {
		h += string([]byte{hexd[b>>4], hexd[b&15]}) + " "
	}
	return h
}

func errStr(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func hex32(v uint32) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = hexd[v&15]
		v >>= 4
	}
	return string(b)
}

func txfifo() uint32 { return (esp.I2C0.SR.Get() >> 18) & 0x3f }

// provoke leaves three 0x84 bytes in the TX FIFO after a NACK.
func provoke() (nack bool, left uint32) {
	hw := esp.I2C0
	hw.INT_CLR.Set(0x3ffff)
	hw.DATA.Set(0x51 << 1) // PCF8563 address + W
	for i := 0; i < 3; i++ {
		hw.DATA.Set(0x84)
	}
	const write, ackEn, ackExp = 1 << 11, 1 << 8, 1 << 9
	hw.COMD0.Set(6 << 11)                    // RSTART
	hw.COMD1.Set(write | ackEn | ackExp | 4) // WRITE 4 bytes, expect NACK
	hw.COMD2.Set(2 << 11)                    // STOP
	hw.SetCTR_CONF_UPGATE(1)
	hw.SetCTR_TRANS_START(1)
	start := time.Now()
	for time.Since(start) < 10*time.Millisecond {
		raw := hw.INT_RAW.Get()
		if raw&esp.I2C_INT_RAW_NACK_INT_RAW != 0 {
			nack = true
			break
		}
		if raw&esp.I2C_INT_RAW_TRANS_COMPLETE_INT_RAW != 0 {
			break
		}
	}
	hw.INT_CLR.Set(0x3ffff)
	return nack, txfifo()
}

// provokeData makes the NACK on a data byte with the command layout of the
// PR: [RSTART, WRITE address, END], then [WRITE 3 data bytes, STOP] with
// ack_exp = 1. The PCF8563 ACKs the first data byte (0x84, taken as its
// register pointer, nothing is written), the controller raises NACK_INT and
// the other two bytes stay in the TX FIFO.
func provokeData() (nack bool, left uint32) {
	hw := esp.I2C0
	const write, ackEn, ackExp, end = 1 << 11, 1 << 8, 1 << 9, 4 << 11
	hw.INT_CLR.Set(0x3ffff)
	hw.DATA.Set(0x51 << 1)
	hw.COMD0.Set(6 << 11)           // RSTART
	hw.COMD1.Set(write | ackEn | 1) // WRITE address
	hw.COMD2.Set(end)               // END
	hw.SetCTR_CONF_UPGATE(1)
	hw.SetCTR_TRANS_START(1)
	start := time.Now()
	for hw.INT_RAW.Get()&esp.I2C_INT_RAW_END_DETECT_INT_RAW == 0 && time.Since(start) < 10*time.Millisecond {
	}
	hw.INT_CLR.Set(0x3ffff)
	for i := 0; i < 3; i++ {
		hw.DATA.Set(0x84)
	}
	hw.COMD0.Set(write | ackEn | ackExp | 3) // WRITE 3 data bytes, expect NACK
	hw.COMD1.Set(2 << 11)                    // STOP
	hw.SetCTR_CONF_UPGATE(1)
	hw.SetCTR_TRANS_START(1)
	start = time.Now()
	for time.Since(start) < 10*time.Millisecond {
		raw := hw.INT_RAW.Get()
		if raw&esp.I2C_INT_RAW_NACK_INT_RAW != 0 {
			nack = true
			break
		}
		if raw&esp.I2C_INT_RAW_TRANS_COMPLETE_INT_RAW != 0 {
			break
		}
	}
	hw.INT_CLR.Set(0x3ffff)
	return nack, txfifo()
}

func readPCF() ([]byte, error) {
	b := []byte{0xEE, 0xEE}
	return b, i2c.Tx(0x51, []byte{0x00}, b)
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	println("=== i2cleftover:", variant, "===")
	i2c.Configure(cfg)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)

	ref, err := readPCF()
	println("reference PCF8563 read | err:", errStr(err), "| data:", hexBytes(ref), "| TXFIFO_CNT after:", txfifo())

	// Part 1: machine.I2C0 (the header says which TinyGo code).
	for round := 1; round <= 3; round++ {
		nack, left := provokeData()
		println("--- machine.I2C0, round", round, "| forced data NACK:", nack, "| TXFIFO_CNT after it:", left)
		for i := 1; i <= 3; i++ {
			sr := esp.I2C0.SR.Get()
			b, err := readPCF()
			ok := err == nil && string(b) == string(ref)
			res := "OK  "
			if !ok {
				res = "FAIL"
			}
			println(res, "read", i, "| SR before: 0x"+hex32(sr), "BUS_BUSY", (sr>>4)&1, "| err:", errStr(err), "| data:", hexBytes(b))
		}
	}

	// Part 2: the same through i2cidf (ESP-IDF v4.4.8 port) as the reference.
	i2cidf.ResetModule()
	idf := i2cidf.New(i2cidf.Config{SDA: machine.GPIO18, SCL: machine.GPIO17, SDAPullup: true, SCLPullup: true, ClkSpeed: 400000})
	idf.Timeout = 50 * time.Millisecond
	for round := 1; round <= 3; round++ {
		nack, left := provokeData()
		println("--- i2cidf, round", round, "| forced data NACK:", nack, "| TXFIFO_CNT after it:", left)
		for i := 1; i <= 3; i++ {
			sr := esp.I2C0.SR.Get()
			resets := idf.FSMResets
			b := []byte{0xEE, 0xEE}
			err := idf.Tx(0x51, []byte{0x00}, b)
			ok := err == nil && string(b) == string(ref)
			res := "OK  "
			if !ok {
				res = "FAIL"
			}
			println(res, "read", i, "| SR before: 0x"+hex32(sr), "BUS_BUSY", (sr>>4)&1, "| err:", errStr(err), "| data:", hexBytes(b), "| i2c_hw_fsm_reset during read:", idf.FSMResets-resets)
		}
	}
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

// Test of i2cidf, the ESP-IDF v4.4.8 I2C master driver ported as is:
// the same situations in which machine.I2C / i2cfix failed on this board.
//
//	S  GT911 as the very first transaction after initialization, then PCF8563
//	   and GT911.
//	N  One read from the empty address 0x42, then PCF8563 and GT911 (ESP-IDF
//	   resets the controller before a transaction if SR.BUS_BUSY is set).
//	M  12 reads from 0x42 (ESP-IDF resets after 10 NACKs), then PCF8563 and
//	   GT911.
//	R  i2cidf.New again (second i2c_param_config), then GT911 first.
//
// Each line prints the error, the data, FSM resets so far and, on error, SR and
// INT_RAW at the moment of the error. Nothing else is configured: the driver
// sets up the pins itself (internal pull-ups on, as in the badge).
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/i2cidf"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

const (
	addrGT911   = 0x5D
	addrPCF8563 = 0x51
	addrAbsent  = 0x42
)

var (
	bus  *i2cidf.Bus
	reg0 = []byte{0x00}
	pid  = []byte{0x81, 0x40}
)

func hex32(v uint32) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = hexd[v&15]
		v >>= 4
	}
	return string(b)
}

func hexBytes(buf []byte) string {
	const hexd = "0123456789abcdef"
	h := ""
	for _, b := range buf {
		h += string([]byte{hexd[b>>4], hexd[b&15]}) + " "
	}
	return h
}

func tx(label string, addr uint16, w []byte, n int) {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 0xEE
	}
	err := bus.Tx(addr, w, buf)
	s := "nil"
	info := ""
	if err != nil {
		s = err.Error()
		info = "| SR 0x" + hex32(bus.LastSR) + " INT_RAW 0x" + hex32(bus.LastRaw)
	}
	println("  ", label, "| err:", s, "| data:", hexBytes(buf), "| resets", bus.FSMResets, info)
}

func newBus() *i2cidf.Bus {
	return i2cidf.New(i2cidf.Config{
		SDA: machine.GPIO18, SCL: machine.GPIO17,
		SDAPullup: true, SCLPullup: true,
		ClkSpeed: 400000,
	})
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	// Wake the GT911 as the badge does (gt911_touch.cpp: INT high 20 + 50 ms,
	// then input).
	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})

	println("=== i2cidftest: ESP-IDF v4.4.8 I2C master driver (port) ===")
	bus = newBus()
	bus.Timeout = 50 * time.Millisecond // the badge's Product ID read uses 50 ms

	println("S  first transaction after init")
	tx("GT911  ", addrGT911, pid, 4)
	tx("PCF8563", addrPCF8563, reg0, 2)
	tx("GT911  ", addrGT911, pid, 4)

	bus.Timeout = 10 * time.Millisecond // the badge's touch reads use 10 ms
	for round := 1; round <= 2; round++ {
		println("--- round", round, "---")
		println("N  one NACK")
		tx("0x42   ", addrAbsent, pid, 4)
		tx("PCF8563", addrPCF8563, reg0, 2)
		tx("GT911  ", addrGT911, pid, 4)
		tx("GT911  ", addrGT911, pid, 4)

		println("M  12 NACKs")
		for i := 0; i < 12; i++ {
			tx("0x42   ", addrAbsent, pid, 4)
		}
		tx("PCF8563", addrPCF8563, reg0, 2)
		tx("GT911  ", addrGT911, pid, 4)
	}

	println("R  i2cidf.New again, GT911 first")
	bus = newBus()
	tx("GT911  ", addrGT911, pid, 4)
	tx("PCF8563", addrPCF8563, reg0, 2)
	tx("GT911  ", addrGT911, pid, 4)
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

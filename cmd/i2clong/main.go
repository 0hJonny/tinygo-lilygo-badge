// Test: reads longer than the 32-byte FIFO. The final check of the TinyGo fix
// (cmd/i2cfinal) showed that a 64-byte read got the first 32 bytes right,
// then timed out, and the bus stayed stuck until Configure. This test isolates
// it: every case starts with Configure (+ pull-ups), reads n bytes from the
// GT911 configuration area (0x8047, static), then reads the GT911 Product ID
// to see whether the bus survived.
//
// The same binary is built with two TinyGo trees (the header says which), and
// at the end the same reads go through internal/i2cidf (the ESP-IDF v4.4.8
// driver port) as a reference.
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
	cfg   = machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	pid   = []byte{0x81, 0x40}
	gtCfg = []byte{0x80, 0x47}
	ref   []byte
	sizes = []int{64, 96, 128, 184, 63, 65, 64, 96, 128, 184, 63, 65, 64, 96, 128, 184, 4}
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

type txer interface {
	Tx(addr uint16, w, r []byte) error
}

func run(bus txer, reset func()) {
	for _, n := range sizes {
		reset()
		b := make([]byte, n)
		for i := range b {
			b[i] = 0xEE
		}
		// A throwaway read first: after a transaction that hung, the GT911 did
		// not acknowledge the next one. This keeps the cases independent.
		pre := make([]byte, 4)
		preErr := bus.Tx(0x5D, pid, pre)
		if preErr != nil {
			println("        (pre-read PID failed:", errStr(preErr), ")")
		}
		err := bus.Tx(0x5D, gtCfg, b)
		p := make([]byte, 4)
		perr := bus.Tx(0x5D, pid, p)
		match := "-"
		if ref != nil && err == nil {
			match = "yes"
			if string(b) != string(ref[:n]) {
				match = "NO"
			}
		}
		println("  n =", n, "| err:", errStr(err), "| same as IDF 184:", match, "| first 4:", hexBytes(b[:4]))
		println("        then PID | err:", errStr(perr), "| data:", hexBytes(p))
	}
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})

	println("=== i2clong:", variant, "===")

	// Reference: the whole configuration area (184 bytes) through i2cidf.
	i2cidf.ResetModule()
	r := i2cidf.New(i2cidf.Config{SDA: machine.GPIO18, SCL: machine.GPIO17, SDAPullup: true, SCLPullup: true, ClkSpeed: 400000})
	r.Timeout = 50 * time.Millisecond
	ref = make([]byte, 184)
	rerr := r.Tx(0x5D, gtCfg, ref)
	if rerr != nil {
		ref = nil
	}
	println("reference (i2cidf, 184 bytes) | err:", errStr(rerr))
	println("--- machine.I2C0")
	run(machine.I2C0, func() {
		machine.I2C0.Configure(cfg)
		esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
		esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	})

	println("--- i2cidf (ESP-IDF v4.4.8 port), reference")
	var idf *i2cidf.Bus
	newIDF := func() {
		i2cidf.ResetModule()
		idf = i2cidf.New(i2cidf.Config{SDA: machine.GPIO18, SCL: machine.GPIO17, SDAPullup: true, SCLPullup: true, ClkSpeed: 400000})
		idf.Timeout = 50 * time.Millisecond
	}
	newIDF()
	run(txFunc(func(a uint16, w, r []byte) error { return idf.Tx(a, w, r) }), newIDF)
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

type txFunc func(addr uint16, w, r []byte) error

func (f txFunc) Tx(addr uint16, w, r []byte) error { return f(addr, w, r) }

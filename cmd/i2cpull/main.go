// Test: does the ESP32-S3 I2C controller come back after an address NACK when
// machine.I2C0.Configure is not followed by enabling the internal pull-ups?
//
// machine.I2C.Configure rewrites the IO_MUX registers of SCL/SDA
// (Pin.configure, PinOutput: no pull-up), so the internal pull-ups the program
// enabled are gone after every Configure. In the tests where Configure brought
// the controller back, the pull-ups were enabled again right after it; in the
// patched TinyGo (re-initialization inside transmit()) they were not.
//
// Build with the unmodified TinyGo 0.42.0. machine.I2C0 is used only for
// Configure; the transactions go through i2cfix with NoRecover set.
//
// Each case starts from a checked state (Configure + pull-ups, 10 ms, PCF8563
// read must succeed). Nothing is printed until the case is over.
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/i2cfix"
)

const (
	addrGT911   = 0x5D
	addrPCF8563 = 0x51
	addrAbsent  = 0x42
)

var (
	cfg  = machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	fix  *i2cfix.Bus
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

func configure(pullUp bool) {
	machine.I2C0.Configure(cfg)
	if pullUp {
		esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
		esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	}
}

type result struct {
	err  error
	data []byte
	info string
}

func tx(addr uint16, w []byte, n int) result {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 0xEE
	}
	err := fix.Tx(addr, w, buf)
	r := result{err: err, data: buf}
	if err != nil {
		d := fix.Last
		done := ""
		for i := 0; i < d.Cmds; i++ {
			if d.Done&(1<<i) != 0 {
				done += "1"
			} else {
				done += "0"
			}
		}
		r.info = "done " + done + " SR 0x" + hex32(d.SR) + " INT_RAW 0x" + hex32(d.IntRaw)
	}
	return r
}

func show(label string, r result) {
	s := "nil"
	if r.err != nil {
		s = r.err.Error()
	}
	println("  ", label, "| err:", s, "| data:", hexBytes(r.data), "|", r.info)
}

func pcf() result { return tx(addrPCF8563, reg0, 2) }

// runCase: checked start; optionally an error on 0x42; Configure with or
// without pull-ups; PCF8563 twice (the second 10 ms later).
func runCase(label string, withError, pullUp bool) {
	configure(true)
	time.Sleep(10 * time.Millisecond)
	pre := pcf()
	var e result
	if withError {
		e = tx(addrAbsent, pid, 4)
	}
	configure(pullUp)
	r := pcf()
	time.Sleep(10 * time.Millisecond)
	r2 := pcf()

	println(label, "| error first:", withError, "| pull-ups after Configure:", pullUp)
	show("pre    PCF8563", pre)
	if withError {
		show("error  0x42   ", e)
	}
	show("result PCF8563", r)
	show("again  PCF8563", r2)
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	// Wake the GT911 (like gt911.Wake).
	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})

	println("=== i2cpull: Configure with and without the internal pull-ups ===")
	i2cfix.NoRecover = true
	fix = i2cfix.Wrap(machine.I2C0, cfg, true)

	configure(true)
	time.Sleep(10 * time.Millisecond)
	println("S  after the first Configure (with pull-ups)")
	show("PCF8563", pcf())

	runCase("P0  no error, no pull-ups  ", false, false)
	runCase("P1  no error, pull-ups     ", false, true)
	runCase("E0  error,    no pull-ups  ", true, false)
	runCase("E1  error,    pull-ups     ", true, true)
	runCase("E0  error,    no pull-ups  ", true, false)
	runCase("E1  error,    pull-ups     ", true, true)
	runCase("P0  no error, no pull-ups  ", false, false)

	// GT911 at the end, from a checked state, with and without pull-ups.
	configure(true)
	time.Sleep(10 * time.Millisecond)
	println("G  at the end")
	show("PCF8563 (pull-ups)   ", pcf())
	show("GT911   (pull-ups)   ", tx(addrGT911, pid, 4))
	configure(false)
	show("GT911   (no pull-ups)", tx(addrGT911, pid, 4))
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

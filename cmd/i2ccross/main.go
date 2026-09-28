// Test: after an address NACK the ESP32-S3 I2C controller stays stuck, and
// machine.I2C0.Configure brings it back when both the failing transaction and
// the next one go through i2cfix (cmd/i2ctiming). With a locally patched TinyGo
// that re-initializes inside transmit(), it did not come back. This test
// crosses the two sides: the failing transaction and the next one each go
// either through machine.I2C0 (T) or through i2cfix (F).
//
// Build with the locally patched TinyGo, steps 1+2 only (READ commands without
// the ACK-check bit, NACK always reported, FIFO reset per transaction; no
// re-initialization inside transmit()). i2cfix runs with NoRecover set.
//
// Each case starts from a checked state (Configure, 10 ms, i2cfix PCF8563 read
// must succeed), then a read-shaped transaction to the empty address 0x42, then
// the case's steps and one PCF8563 read. Nothing is printed until the case is
// over.
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

func reconfigure() {
	machine.I2C0.Configure(cfg)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

type result struct {
	err  error
	data []byte
	info string
}

// txT is a transaction through machine.I2C0. info: done bits of COMD0–6, SR.
func txT(addr uint16, w []byte, n int) result {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 0xEE
	}
	err := machine.I2C0.Tx(addr, w, buf)
	r := result{err: err, data: buf}
	if err != nil {
		hw := esp.I2C0
		regs := []uint32{hw.COMD0.Get(), hw.COMD1.Get(), hw.COMD2.Get(), hw.COMD3.Get(), hw.COMD4.Get(), hw.COMD5.Get(), hw.COMD6.Get()}
		done := ""
		for _, v := range regs {
			if v&(1<<31) != 0 {
				done += "1"
			} else {
				done += "0"
			}
		}
		r.info = "done " + done + " SR 0x" + hex32(hw.SR.Get())
	}
	return r
}

// txF is a transaction through i2cfix.
func txF(addr uint16, w []byte, n int) result {
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

type side func(addr uint16, w []byte, n int) result

// runCase: checked start, error through errSide, optional Configure and pause,
// PCF8563 read through readSide.
func runCase(label string, errSide side, configure bool, pause time.Duration, readSide side) {
	reconfigure()
	time.Sleep(10 * time.Millisecond)
	pre := txF(addrPCF8563, reg0, 2)
	e := errSide(addrAbsent, pid, 4)
	if configure {
		reconfigure()
	}
	if pause > 0 {
		time.Sleep(pause)
	}
	r := readSide(addrPCF8563, reg0, 2)

	steps := "no Configure"
	if configure {
		steps = "Configure"
	}
	println(label, "|", steps, "| pause", pause.String())
	show("pre    PCF8563 (F)", pre)
	show("error  0x42       ", e)
	show("result PCF8563    ", r)
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

	println("=== i2ccross: error and next read through machine.I2C0 (T) or i2cfix (F) ===")
	i2cfix.NoRecover = true
	fix = i2cfix.Wrap(machine.I2C0, cfg, true)

	// Start: PCF8563 first (the GT911 may not answer yet), GT911 last.
	reconfigure()
	time.Sleep(10 * time.Millisecond)
	println("S  after the first Configure")
	show("PCF8563 (F)", txF(addrPCF8563, reg0, 2))

	runCase("T-T   ", txT, false, 0, txT)
	runCase("F-C-F ", txF, true, 0, txF)
	runCase("T-C-F ", txT, true, 0, txF)
	runCase("F-C-T ", txF, true, 0, txT)
	runCase("T-C-T ", txT, true, 0, txT)
	runCase("T-C10-T", txT, true, 10*time.Millisecond, txT)
	runCase("T-C-T ", txT, true, 0, txT)
	runCase("F-C-T ", txF, true, 0, txT)
	runCase("T-C-F ", txT, true, 0, txF)

	reconfigure()
	time.Sleep(10 * time.Millisecond)
	println("G  at the end")
	show("PCF8563 (T)", txT(addrPCF8563, reg0, 2))
	show("PCF8563 (F)", txF(addrPCF8563, reg0, 2))
	show("GT911   (T)", txT(addrGT911, pid, 4))
	show("GT911   (F)", txF(addrGT911, pid, 4))
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

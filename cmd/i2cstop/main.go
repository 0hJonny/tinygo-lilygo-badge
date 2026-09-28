// Test: does the GT911 answer the first transaction after
// machine.I2C0.Configure, and does a STOP on the bus before it matter?
//
// After an address NACK the ESP32-S3 I2C controller stops without a STOP
// condition. ESP-IDF generates one when it resets the bus
// (i2c_master_clear_bus); machine.I2C.Configure does not. Observed so far: a
// GT911 read as the first transaction after Configure got a NACK in several
// runs, and a GT911 read after a PCF8563 read (a complete transaction with a
// STOP) worked.
//
// Build with the unmodified TinyGo 0.42.0. machine.I2C0 is used only for
// Configure; the transactions go through i2cfix with NoRecover set. Configure is
// always followed by enabling the internal pull-ups (the bus does not work
// without them on this board).
//
// Each case is run three times in rotation. In every case the GT911 is read
// twice (the second read 10 ms later, without Configure). Nothing is printed
// until the case is over.
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

func configure() {
	machine.I2C0.Configure(cfg)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
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

func gt911() result { return tx(addrGT911, pid, 4) }

type step struct {
	name string
	do   func() result // nil result: nothing to show
}

var (
	stepConfigure = step{"Configure", func() result { configure(); return result{} }}
	stepSleep     = step{"sleep 10ms", func() result { time.Sleep(10 * time.Millisecond); return result{} }}
	stepClear     = step{"clear bus + STOP", func() result { fix.ClearBus(); return result{} }}
	stepPCF       = step{"PCF8563", func() result { return tx(addrPCF8563, reg0, 2) }}
	stepAbsent    = step{"read 0x42 (NACK)", func() result { return tx(addrAbsent, pid, 4) }}
)

// runCase does the steps, then reads the GT911 twice.
func runCase(label string, steps []step) {
	var rs [8]result
	shown := [8]bool{}
	for i, s := range steps {
		rs[i] = s.do()
		shown[i] = s.name == "PCF8563" || s.name == "read 0x42 (NACK)"
	}
	g1 := gt911()
	time.Sleep(10 * time.Millisecond)
	g2 := gt911()

	names := ""
	for _, s := range steps {
		names += s.name + ", "
	}
	println(label, "|", names+"GT911, GT911")
	for i, s := range steps {
		if shown[i] {
			show(s.name+"      ", rs[i])
		}
	}
	show("GT911 (1)      ", g1)
	show("GT911 (2, 10ms)", g2)
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
	time.Sleep(500 * time.Millisecond)

	println("=== i2cstop: GT911 as the first transaction after Configure ===")
	i2cfix.NoRecover = true
	fix = i2cfix.Wrap(machine.I2C0, cfg, true)

	configure()
	time.Sleep(10 * time.Millisecond)
	println("S  start: Configure, PCF8563, GT911")
	show("PCF8563", tx(addrPCF8563, reg0, 2))
	show("GT911  ", gt911())

	for round := 0; round < 3; round++ {
		println("--- round", round+1, "---")
		runCase("A  Configure only      ", []step{stepConfigure, stepSleep})
		runCase("B  Configure, PCF8563  ", []step{stepConfigure, stepSleep, stepPCF})
		runCase("C  STOP, Configure     ", []step{stepClear, stepConfigure, stepSleep})
		runCase("D  NACK, Configure     ", []step{stepAbsent, stepConfigure, stepSleep})
		runCase("E  NACK, STOP, Configure", []step{stepAbsent, stepClear, stepConfigure, stepSleep})
	}
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

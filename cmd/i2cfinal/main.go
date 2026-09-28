// Final check of the machine.I2C fix proposed for TinyGo (branch
// esp32xx-i2c-fix): only machine.I2C0, no i2cfix. Besides the cases that failed
// before, it covers the other paths the change touches: read-only and
// write-only transactions, reads longer than the 32-byte FIFO (several END
// segments), repeated Configure, and many transactions in a row.
//
// Devices: GT911 at 0x5D, PCF8563 at 0x51, nothing at 0x42. The PCF8563 is
// only read (and its register pointer set); nothing is written to its
// registers. The GT911 configuration area (0x8047…) is static.
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

const (
	addrGT911   = 0x5D
	addrPCF8563 = 0x51
	addrAbsent  = 0x42
)

var (
	cfg     = machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	i2c     = machine.I2C0
	pid     = []byte{0x81, 0x40}
	gtCfg   = []byte{0x80, 0x47}
	pcfReg0 = []byte{0x00}
	fails   int
)

func hexBytes(buf []byte) string {
	const hexd = "0123456789abcdef"
	h := ""
	for _, b := range buf {
		h += string([]byte{hexd[b>>4], hexd[b&15]}) + " "
	}
	return h
}

func configure() {
	i2c.Configure(cfg)
	// The board needs the internal pull-ups (not part of the fix).
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

// check prints one result and counts it as a failure if it is not what is
// expected.
func check(label string, err error, wantErr bool, got, want []byte) {
	ok := (err != nil) == wantErr
	if ok && want != nil {
		ok = string(got) == string(want)
	}
	s := "nil"
	if err != nil {
		s = err.Error()
	}
	res := "OK  "
	if !ok {
		res = "FAIL"
		fails++
	}
	println(res, label, "| err:", s, "| data:", hexBytes(got))
}

func readPID() ([]byte, error) {
	b := make([]byte, 4)
	return b, i2c.Tx(addrGT911, pid, b)
}

func readPCF() ([]byte, error) {
	b := make([]byte, 2)
	return b, i2c.Tx(addrPCF8563, pcfReg0, b)
}

func readAbsent() error {
	b := make([]byte, 4)
	return i2c.Tx(addrAbsent, pid, b)
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	// Wake the GT911 (INT high 20 + 50 ms, then input).
	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})

	println("=== i2cfinal: machine.I2C0 with the esp32xx-i2c-fix branch ===")
	id := []byte{'9', '1', '1', 0}
	configure()

	println("--- 1. GT911 as the first transaction after Configure")
	b, err := readPID()
	check("GT911 PID                     ", err, false, b, id)
	b, err = readPCF()
	check("PCF8563 reg 0x00-0x01         ", err, false, b, nil)
	pcf := append([]byte(nil), b...) // reference value for later reads

	println("--- 2. absent address: read, write-only, read-only")
	err = readAbsent()
	check("read 0x42 (expect error)      ", err, true, nil, nil)
	b, err = readPCF()
	check("PCF8563 right after           ", err, false, b, pcf)
	err = i2c.Tx(addrAbsent, []byte{0x00}, nil)
	check("write-only 0x42 (expect error)", err, true, nil, nil)
	b, err = readPID()
	check("GT911 right after             ", err, false, b, id)
	b = make([]byte, 2)
	err = i2c.Tx(addrAbsent, nil, b)
	check("read-only 0x42 (expect error) ", err, true, nil, nil)
	b, err = readPCF()
	check("PCF8563 right after           ", err, false, b, pcf)

	println("--- 3. write-only and read-only to a present device")
	err = i2c.Tx(addrPCF8563, []byte{0x00}, nil) // set the register pointer only
	check("PCF8563 write-only pointer 0  ", err, false, nil, nil)
	b = make([]byte, 2)
	err = i2c.Tx(addrPCF8563, nil, b) // read-only from the pointer
	check("PCF8563 read-only 2 bytes     ", err, false, b, pcf)

	println("--- 4. read lengths (GT911 config area 0x8047, compared with one 64-byte read)")
	ref := make([]byte, 64)
	err = i2c.Tx(addrGT911, gtCfg, ref)
	check("GT911 64 bytes (reference)    ", err, false, ref[:8], nil)
	for _, n := range []int{1, 2, 3, 31, 32, 33, 40, 64} {
		b = make([]byte, n)
		err = i2c.Tx(addrGT911, gtCfg, b)
		label := "GT911 read n bytes            "
		println("     n =", n)
		show := b
		if len(show) > 8 {
			show = show[:8]
		}
		if err == nil && string(b) != string(ref[:n]) {
			err = nil
			println("FAIL data differs from the reference")
			fails++
		}
		check(label, err, false, show, nil)
	}

	println("--- 5. Configure again x5, GT911 first every time")
	for i := 0; i < 5; i++ {
		configure()
		b, err = readPID()
		check("GT911 PID after Configure     ", err, false, b, id)
	}

	println("--- 6. 200 cycles: read 0x42, PCF8563, GT911")
	e42, ePCF, eGT := 0, 0, 0
	for i := 0; i < 200; i++ {
		if readAbsent() == nil {
			e42++
		}
		if b, err := readPCF(); err != nil || string(b) != string(pcf) {
			ePCF++
		}
		if b, err := readPID(); err != nil || string(b) != string(id) {
			eGT++
		}
	}
	println("     0x42 without error:", e42, "| PCF8563 bad:", ePCF, "| GT911 bad:", eGT)
	fails += e42 + ePCF + eGT

	println("--- 7. 1000 GT911 reads in a row")
	bad := 0
	start := time.Now()
	for i := 0; i < 1000; i++ {
		if b, err := readPID(); err != nil || string(b) != string(id) {
			bad++
		}
	}
	println("     bad:", bad, "| time ms:", int(time.Since(start).Milliseconds()))
	fails += bad

	println("=== done, failures:", fails, "===")
	for {
		time.Sleep(time.Second)
	}
}

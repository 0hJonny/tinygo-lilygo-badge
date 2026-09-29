// Check for tinygo-org/tinygo PR #5783 (bus clear with a GPIO STOP in
// Configure) on the LILYGO T5-4.7-S3: the first read after Configure, for the
// GT911 (0x5D) and the PCF8563 (0x51).
//
// After boot: Configure, GT911 Product ID; then 5 x (Configure, GT911). Then
// Configure, PCF8563 registers 0x00-0x01; then 5 x (Configure, PCF8563).
// Only machine.I2C0; the internal pull-ups are enabled after every Configure,
// as in the earlier tests.
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

var cfg = machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}

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

func configure() {
	machine.I2C0.Configure(cfg)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

func phase(name string, addr uint16, w []byte, n int, want string) {
	ok := 0
	for i := 0; i <= 5; i++ {
		configure()
		b := make([]byte, n)
		for j := range b {
			b[j] = 0xEE
		}
		err := machine.I2C0.Tx(addr, w, b)
		label := "Configure, first read"
		if i > 0 {
			label = "Configure again, read"
		}
		res := "FAIL"
		if err == nil && (want == "" || string(b) == want) {
			res = "OK  "
			ok++
		}
		println(res, name, i, label, "| err:", errStr(err), "| data:", hexBytes(b))
	}
	println("SUMMARY", name, "| OK", ok, "of 6")
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
	time.Sleep(100 * time.Millisecond)

	println("=== i2cfirst: PR #5783, first read after Configure ===")
	phase("GT911  ", 0x5D, []byte{0x81, 0x40}, 4, "911\x00")
	phase("PCF8563", 0x51, []byte{0x00}, 2, "")
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

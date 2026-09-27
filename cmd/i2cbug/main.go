// Reproduction of the TinyGo I2C problem on ESP32-S3 without the display, for an
// issue in tinygo-org/tinygo. Only the board's I2C bus is needed: the GT911 at
// 0x5D (present) and the free address 0x42 (nothing there).
//
// Tests:
//  0. Idle SDA/SCL levels without pull-ups and with the internal pull-ups:
//     do the board's external pull-ups work (N6/N7/N8 in the V2.4 schematic).
//  1. machine.I2C0.Tx: read the GT911, the baseline (should be "911").
//  2. machine.I2C0.Tx: read from the empty address 0x42, an error is expected.
//  3. machine.I2C0.Tx: write to the empty address 0x42, an error is expected.
//  4. machine.I2C0.Tx: read the GT911 right after test 2, is it corrupted.
//  5. i2cfix (ESP-IDF style): the same reads, the empty address and the GT911.
//  6. i2cfix with the ACK-check bit on READ (as in TinyGo): read the GT911,
//     to test the hypothesis of why simply removing `!readLast` for esp32xx
//     was reverted (PR #5602).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/i2cfix"
)

const (
	addrGT911  = 0x5D
	addrAbsent = 0x42
	reps       = 3
)

var regProductID = []byte{0x81, 0x40}

func show(label string, err error, buf []byte) {
	s := "nil"
	if err != nil {
		s = err.Error()
	}
	h := ""
	for _, b := range buf {
		const hexd = "0123456789abcdef"
		h += string([]byte{hexd[b>>4], hexd[b&15]}) + " "
	}
	println(label, "| err:", s, "| data:", h)
}

func fill(buf []byte) {
	for i := range buf {
		buf[i] = 0xEE // marker: if nothing is written to the buffer, EE remains
	}
}

// pullUps enables the internal SCL/SDA pull-ups, like i2c_config_t
// (scl/sda_pullup_en) in the original; machine.I2C.Configure does not enable them.
func pullUps() {
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

func levels(label string, mode machine.PinMode) {
	machine.GPIO17.Configure(machine.PinConfig{Mode: mode})
	machine.GPIO18.Configure(machine.PinConfig{Mode: mode})
	time.Sleep(5 * time.Millisecond)
	println("0", label, "| SCL(IO17):", machine.GPIO17.Get(), "SDA(IO18):", machine.GPIO18.Get())
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	// The display bus is not needed.
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	// Wake the GT911 (like gt911.Wake / i2c_bus_init in the original).
	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})

	println("=== Idle bus levels (true = high, expected true) ===")
	levels("no pull-ups            ", machine.PinInput)
	levels("internal pull-ups      ", machine.PinInputPullup)

	cfg := machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	buf := make([]byte, 4)

	println("=== TinyGo machine.I2C0.Tx (ESP32-S3), internal pull-ups enabled ===")
	machine.I2C0.Configure(cfg)
	pullUps()
	for i := 0; i < reps; i++ {
		fill(buf)
		err := machine.I2C0.Tx(addrGT911, regProductID, buf)
		show("1 read  GT911 0x5D (present) ", err, buf)
	}
	for i := 0; i < reps; i++ {
		fill(buf)
		err := machine.I2C0.Tx(addrAbsent, regProductID, buf)
		show("2 read  0x42 (nothing there) ", err, buf)
	}
	for i := 0; i < reps; i++ {
		err := machine.I2C0.Tx(addrAbsent, regProductID, nil)
		show("3 write 0x42 (nothing there) ", err, nil)
	}
	machine.I2C0.Configure(cfg)
	pullUps()
	fill(buf)
	_ = machine.I2C0.Tx(addrAbsent, regProductID, buf) // a failed read…
	for i := 0; i < reps; i++ {
		fill(buf)
		err := machine.I2C0.Tx(addrGT911, regProductID, buf) // …and right away a valid one
		show("4 read  GT911 after test 2   ", err, buf)
	}

	println("=== i2cfix: ESP-IDF style transaction (READ without ACK check) ===")
	bus := i2cfix.New(machine.I2C0, cfg, true)
	for i := 0; i < reps; i++ {
		fill(buf)
		err := bus.Tx(addrAbsent, regProductID, buf)
		show("5 read  0x42 (nothing there) ", err, buf)
	}
	for i := 0; i < reps; i++ {
		fill(buf)
		err := bus.Tx(addrGT911, regProductID, buf)
		show("5 read  GT911 0x5D (present) ", err, buf)
	}

	println("=== i2cfix + ACK check on READ (as in TinyGo) ===")
	i2cfix.ReadAckCheck = true
	for i := 0; i < reps; i++ {
		fill(buf)
		err := bus.Tx(addrGT911, regProductID, buf)
		show("6 read  GT911 0x5D (present) ", err, buf)
	}
	i2cfix.ReadAckCheck = false
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

//go:build tinygo

// Package gt911 is a driver for the Goodix GT911 capacitive touch controller (I2C).
//
// It is a port of main/gt911_touch.cpp from the epd-lilygo-badge project (ESP-IDF,
// tested on LILYGO T5-4.7-S3 V2.4) without LVGL: init, reading the first point, sleep.
// The style follows ft6336 from tinygo.org/x/drivers; it implements touch.Pointer.
//
// The rotation of the touch film axes relative to the panel is a property of the
// board, not of the chip, so it is not here (see panelPoint in cmd/touch).
package gt911

import (
	"errors"
	"machine"
	"time"

	"tinygo.org/x/drivers"
	"tinygo.org/x/drivers/touch"
)

// The address is set by the INT level when the chip leaves reset: low gives 0x5D,
// high gives 0x14 (Goodix documentation). On the T5-4.7-S3 there is no reset line on a GPIO
// (T_RST is an RC network), so the address is latched at power-on; in practice it is 0x5D.
const (
	Address    = 0x5D
	AddressAlt = 0x14
)

// Registers (16-bit addresses, high byte first).
const (
	regCommand   = 0x8040 // 0x05 = sleep
	regXMax      = 0x8048 // X Output Max, low byte first (+ 0x8049)
	regYMax      = 0x804A // Y Output Max (+ 0x804B)
	regProductID = 0x8140 // 4 ASCII bytes, "911"
	regStatus    = 0x814E // bit 7: data ready, bits 0–3: number of touches
	regPoint1    = 0x8150 // X (2 bytes), Y (2 bytes), size (2 bytes), LE

	cmdSleep = 0x05

	statusReady = 0x80
)

var (
	errNotGT911 = errors.New("gt911: controller not found (Product ID is not \"911\")")
	errBadData  = errors.New("gt911: bad data (coordinates outside the resolution)")
)

// Device is a GT911 controller on an I2C bus.
type Device struct {
	bus     drivers.I2C
	intPin  machine.Pin
	Address uint16

	xMax, yMax uint16
	w          [3]byte
	r          [6]byte
}

// New returns a device. The I2C bus must already be configured.
func New(bus drivers.I2C, intPin machine.Pin) *Device {
	return &Device{bus: bus, intPin: intPin, Address: Address}
}

// Config holds the GT911 settings.
type Config struct {
	// Address is the I2C address. 0 means try 0x5D, then 0x14.
	Address uint16
}

// Configure wakes the controller with a pulse on INT and checks the Product ID
// (i2c_bus_init() of the original, except for bus setup and the RTC CLKOUT).
func (d *Device) Configure(config Config) error {
	d.Wake()

	addrs := []uint16{Address, AddressAlt}
	if config.Address != 0 {
		addrs = []uint16{config.Address}
	}
	// Several attempts: right after flashing or an ESP32 reset the controller
	// (it has its own power) may not answer the first time. The original makes
	// one attempt, but IDF itself recovers the bus after an error.
	found := false
	for attempt := 0; attempt < 5 && !found; attempt++ {
		for _, a := range addrs {
			d.Address = a
			if id, err := d.ProductID(); err == nil && id == "911" {
				found = true
				break
			}
		}
		if !found {
			time.Sleep(20 * time.Millisecond)
		}
	}
	if !found {
		return errNotGT911
	}

	// The resolution is used to scale ReadTouchPoint to 16 bits, like other touch
	// drivers in tinygo.org/x/drivers. The original does not read it.
	if d.readReg(regXMax, d.r[:4]) == nil {
		d.xMax = uint16(d.r[0]) | uint16(d.r[1])<<8
		d.yMax = uint16(d.r[2]) | uint16(d.r[3])<<8
	}
	return nil
}

// Wake drives INT high for 20 + 50 ms, then makes INT an input without pulls
// (like i2c_bus_init() in the original; per the Goodix documentation a high INT
// wakes the controller from sleep). A comment in the original calls this selecting
// address 0x5D, but without a reset line the address does not change this way.
func (d *Device) Wake() {
	d.intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	d.intPin.High()
	time.Sleep(20 * time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	// The documentation requires INT to float as an input, with no pulls.
	d.intPin.Configure(machine.PinConfig{Mode: machine.PinInput})
}

// ProductID reads the product identifier ("911" for the GT911).
func (d *Device) ProductID() (string, error) {
	if err := d.readReg(regProductID, d.r[:4]); err != nil {
		return "", err
	}
	n := 0
	for n < 4 && d.r[n] != 0 {
		n++
	}
	return string(d.r[:n]), nil
}

// Resolution returns X/Y Output Max from the controller configuration (0 if not read).
func (d *Device) Resolution() (x, y uint16) {
	return d.xMax, d.yMax
}

// ReadRaw reads the first touch point in controller coordinates
// (gt911_read_touch() in the original). touched = false if there is no touch or
// no new data yet (status bit 7 clear), as in the original.
func (d *Device) ReadRaw() (x, y, size int, touched bool, err error) {
	if err = d.readReg(regStatus, d.r[:1]); err != nil {
		return
	}
	status := d.r[0]
	if status&statusReady == 0 {
		return
	}
	if status&0x0F > 0 {
		// The original reads 4 bytes (X, Y); here 2 more bytes of size are read for Z.
		if err = d.readReg(regPoint1, d.r[:6]); err == nil {
			x = int(d.r[0]) | int(d.r[1])<<8
			y = int(d.r[2]) | int(d.r[3])<<8
			size = int(d.r[4]) | int(d.r[5])<<8
			touched = true
			// Coordinates outside the resolution indicate a bus failure that the bus did
			// not detect (see i2cfix): an error, not a touch. The bus is not reset here:
			// a bus recovery was observed to make a healthy GT911 stop responding.
			if d.xMax > 0 && d.yMax > 0 && (x >= int(d.xMax) || y >= int(d.yMax)) {
				x, y, size, touched, err = 0, 0, 0, false, errBadData
				return
			}
		}
	}
	// ALWAYS clear the ready flag (including on release): otherwise the controller
	// stops reporting new touches and the screen "freezes" (learned in the original).
	d.writeReg(regStatus, 0x00)
	return
}

// ReadTouchPoint implements touch.Pointer: X and Y are scaled to 0…0xFFFF
// by the controller resolution (like other touch drivers in tinygo.org/x/drivers),
// and Z > 0 means a touch (the contact size, at least 1).
func (d *Device) ReadTouchPoint() touch.Point {
	x, y, size, touched, err := d.ReadRaw()
	if err != nil || !touched {
		return touch.Point{}
	}
	if d.xMax > 0 && d.yMax > 0 {
		x = x * 0xFFFF / int(d.xMax)
		y = y * 0xFFFF / int(d.yMax)
	}
	return touch.Point{X: x, Y: y, Z: max(size, 1)}
}

// Touched reports whether there is a touch.
func (d *Device) Touched() bool {
	return d.ReadTouchPoint().Z > 0
}

// Sleep puts the controller to sleep (gt911_sleep() in the original). INT is
// pulled low first: without that the GT911 silently ignores the command and keeps
// scanning (verified on hardware in the original, ≈ mA of extra current). INT stays
// low, because a high level wakes the controller. Wait at least 58 ms before waking it.
func (d *Device) Sleep() error {
	d.intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	d.intPin.Low()
	time.Sleep(10 * time.Millisecond)

	err := d.writeReg(regCommand, cmdSleep)

	time.Sleep(60 * time.Millisecond)
	return err
}

func (d *Device) readReg(reg uint16, buf []byte) error {
	d.w[0], d.w[1] = byte(reg>>8), byte(reg)
	return d.bus.Tx(d.Address, d.w[:2], buf)
}

func (d *Device) writeReg(reg uint16, v byte) error {
	d.w[0], d.w[1], d.w[2] = byte(reg>>8), byte(reg), v
	return d.bus.Tx(d.Address, d.w[:3], nil)
}

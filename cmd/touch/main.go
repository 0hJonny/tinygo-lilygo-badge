// Stage 9: GT911 touch. Phase 1 draws dots under the finger (checks coordinates
// and axis rotation), phase 2 checks sleep (like the table in the badge project README).
package main

import (
	"image/color"
	"machine"
	"strconv"
	"time"

	"tinygo.org/x/tinyfont"
	"tinygo.org/x/tinyfont/freesans"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/gt911"
)

var display epd.Display // 253 KB framebuffer, so it is global

var black = color.RGBA{A: 255}

// panelPoint maps GT911 coordinates to T5-4.7-S3 panel coordinates: the film is
// bonded rotated by 90° (lv_touchpad_read() in the badge project:
// phys_x = raw_y, phys_y = (PHYS_H − 1) − raw_x).
func panelPoint(rawX, rawY int) (x, y int) {
	return rawY, epd.Height - 1 - rawX
}

func main() {
	// First of all: put the 74HCT4094 register into the "power off" state.
	initErr := epd.Init()

	// I2C as in i2c_bus_init() of the original: SCL IO17, SDA IO18, 400 kHz,
	// internal pull-ups enabled (TinyGo does not enable them itself; the board has
	// 10 kΩ + 10 kΩ to VDD3V3, N6/N7/N8). The bus implementation is chosen at build
	// time (bus_*.go): i2cfix by default, machine.I2C0 with -tags stdi2c.
	bus, busName := newBus(machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz})

	tp := gt911.New(bus, machine.GPIO47)
	tpErr := tp.Configure(gt911.Config{})

	time.Sleep(3 * time.Second)
	if initErr != nil {
		println("touch: epd.Init error:", initErr.Error())
		halt()
	}
	if tpErr != nil {
		println("touch:", tpErr.Error())
		halt()
	}
	id, _ := tp.ProductID()
	xMax, yMax := tp.Resolution()
	println("touch: I2C bus:", busName)
	println("touch: GT911 address 0x" + strconv.FormatUint(uint64(tp.Address), 16) + " ID " + id)
	println("touch: resolution", xMax, "x", yMax)
	println("touch: I2C errors", busErrors(), "recoveries", busRecoveries())

	display.ClearBuffer()
	tinyfont.WriteLine(&display, &freesans.Bold18pt7b, 30, 60, "GT911 touch test", black)
	tinyfont.WriteLine(&display, &freesans.Regular12pt7b, 30, 105, "Phase 1 (30 s): tap anywhere - a dot appears under the finger.", black)
	tinyfont.WriteLine(&display, &freesans.Regular12pt7b, 30, 140, "Phase 2 (30 s): keep tapping - awake / asleep / woken counts in the log.", black)
	if err := display.Display(); err != nil {
		println("touch: draw error:", err.Error())
		halt()
	}

	println("touch: phase 1 - touch the screen for 30 s")
	const dot = 20
	pressed := false
	for end := time.Now().Add(30 * time.Second); time.Now().Before(end); {
		rx, ry, _, touched, err := tp.ReadRaw()
		if err != nil {
			println("touch: error:", err.Error(), "(total errors", busErrors(), "recoveries", busRecoveries(), ")")
		}
		if touched && !pressed {
			px, py := panelPoint(rx, ry)
			println("touch: raw", rx, ry, "-> panel", px, py)
			r := epd.Rect{X: px - dot/2, Y: py - dot/2, W: dot, H: dot}
			display.FB.FillRect(r.X, r.Y, r.W, r.H, 0)
			if err := display.DisplayArea(r); err != nil {
				println("touch: draw error:", err.Error())
				halt()
			}
		}
		pressed = touched
		time.Sleep(20 * time.Millisecond)
	}

	println("touch: phase 2 - keep touching the screen")
	count := func(label string) {
		n, errs := 0, 0
		was := false
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); {
			_, _, _, touched, err := tp.ReadRaw()
			if err != nil {
				errs++
			}
			if touched && !was {
				n++
			}
			was = touched
			time.Sleep(20 * time.Millisecond)
		}
		println("touch:", label, "- touches in 10 s:", n, "errors:", errs, "(I2C total", busErrors(), "recoveries", busRecoveries(), ")")
	}
	count("awake")
	if err := tp.Sleep(); err != nil {
		println("touch: sleep command error:", err.Error())
	}
	count("after Sleep (expected 0)")
	tp.Wake()
	count("after Wake")
	println("touch: done")
	halt()
}

func halt() {
	for i := 0; ; i++ {
		println("tick", i)
		time.Sleep(time.Second)
	}
}

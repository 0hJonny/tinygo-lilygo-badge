// Stage 5: full output with text (tinyfont) and a partial area update.
package main

import (
	"image/color"
	"strconv"
	"time"

	"tinygo.org/x/tinyfont"
	"tinygo.org/x/tinyfont/freesans"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

var display epd.Display // 253 KB framebuffer, so it is global

var black = color.RGBA{A: 255}

// Counter area: X is odd, to exercise the nibble shift.
var box = epd.Rect{X: 301, Y: 330, W: 359, H: 90}

func drawBox(n int) {
	display.FB.FillRect(box.X, box.Y, box.W, box.H, 15)
	// 2 px border of level 4, black text.
	display.FB.FillRect(box.X, box.Y, box.W, 2, 4)
	display.FB.FillRect(box.X, box.Y+box.H-2, box.W, 2, 4)
	display.FB.FillRect(box.X, box.Y, 2, box.H, 4)
	display.FB.FillRect(box.X+box.W-2, box.Y, 2, box.H, 4)
	tinyfont.WriteLine(&display, &freesans.Bold24pt7b, int16(box.X+24), int16(box.Y+60), "update #"+strconv.Itoa(n), black)
}

func main() {
	// First of all: put the 74HCT4094 register into the "power off" state.
	initErr := epd.Init()

	time.Sleep(3 * time.Second)
	if initErr != nil {
		println("area: Init error:", initErr.Error())
		halt()
	}

	display.ClearBuffer()
	tinyfont.WriteLine(&display, &freesans.Bold24pt7b, 40, 90, "TinyGo on LilyGo T5-4.7-S3", black)
	tinyfont.WriteLine(&display, &freesans.Regular18pt7b, 40, 150, "ED047TC1 driver in pure Go: LCD_CAM + GDMA", black)
	tinyfont.WriteLine(&display, &freesans.Regular12pt7b, 40, 200, "Partial update below: only the box is redrawn.", black)
	// A small 16-level scale for reference.
	for g := 0; g < 16; g++ {
		display.FB.FillRect(40+g*55, 240, 55, 50, uint8(g))
	}
	drawBox(0)

	start := time.Now()
	if err := display.Display(); err != nil {
		println("area: full output error:", err.Error())
		halt()
	}
	println("area: full output", time.Since(start).Milliseconds(), "ms")

	for n := 1; n <= 5; n++ {
		time.Sleep(3 * time.Second)
		drawBox(n)
		start = time.Now()
		if err := display.DisplayArea(box); err != nil {
			println("area: area update error:", err.Error())
			halt()
		}
		println("area: area update", n, time.Since(start).Milliseconds(), "ms")
	}
	println("area: done, power is off")
	halt()
}

func halt() {
	for i := 0; ; i++ {
		println("tick", i)
		time.Sleep(time.Second)
	}
}

// Stage 4: grayscale output check: 16 bands, a border and orientation marks.
package main

import (
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

// The framebuffer is 253 KB, so it is global rather than on the stack.
var fb epd.Framebuffer

func drawTestPattern() {
	fb.Fill(15) // white

	// 16 vertical bands, 60 pixels each: black (0) on the left, white (15) on the right.
	const bandW = epd.Width / 16
	for g := 0; g < 16; g++ {
		fb.FillRect(g*bandW, 120, bandW, 300, uint8(g))
	}

	// A 4-pixel border around the screen.
	fb.FillRect(0, 0, epd.Width, 4, 0)
	fb.FillRect(0, epd.Height-4, epd.Width, 4, 0)
	fb.FillRect(0, 0, 4, epd.Height, 0)
	fb.FillRect(epd.Width-4, 0, 4, epd.Height, 0)

	// Orientation marks: a black 60×60 square in the top-left corner
	// and a gray (level 8) 30×30 square in the bottom-right corner.
	fb.FillRect(20, 20, 60, 60, 0)
	fb.FillRect(epd.Width-50, epd.Height-50, 30, 30, 8)
}

func main() {
	epd.UseDMA = busMode != "bitbang"
	// First of all: put the 74HCT4094 register into the "power off" state.
	initErr := epd.Init()

	time.Sleep(3 * time.Second)
	println("gray: bus", busMode)
	if initErr != nil {
		println("gray: Init error:", initErr.Error())
		halt()
	}
	println("gray: preparing the framebuffer")
	drawTestPattern()

	epd.PowerOn()
	println("gray: power on, clearing")
	start := time.Now()
	if err := epd.Clear(); err != nil {
		epd.PowerOff()
		println("gray: clear error:", err.Error(), "- power is off")
		halt()
	}
	println("gray: clear", time.Since(start).Milliseconds(), "ms")

	start = time.Now()
	if err := epd.DrawGrayscale(&fb, epd.BlackOnWhite); err != nil {
		epd.PowerOff()
		println("gray: draw error:", err.Error(), "- power is off")
		halt()
	}
	println("gray: draw", time.Since(start).Milliseconds(), "ms")

	epd.PowerOff()
	println("gray: power off")
	halt()
}

func halt() {
	for i := 0; ; i++ {
		println("done, tick", i)
		time.Sleep(time.Second)
	}
}

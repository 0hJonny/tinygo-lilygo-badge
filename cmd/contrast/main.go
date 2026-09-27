// Contrast table tuning: 4 scales of 16 levels, each drawn with its own frame
// time table, so they can be compared in a single photo.
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

type candidate struct {
	name string
	c    epd.Contrast
}

// Level v gets frames 0…14−v. The original table sums to 1020 ticks, yet bands
// with 8 or more frames (sum ≥230) already look equally black.
// The candidates spread the drive over 240–300 ticks. Result on hardware
// (2026-09-27): the shorter tables are visibly grainy and do not separate the
// dark levels much better, so the original table was kept.
var candidates = [4]candidate{
	{"A: LilyGo original", epd.DefaultContrast},
	{"B: uniform 16", epd.Contrast{16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16, 16}},
	{"C: uniform 20", epd.Contrast{20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20, 20}},
	{"D: original x0.3", epd.Contrast{9, 9, 6, 6, 9, 9, 9, 12, 12, 15, 15, 15, 30, 60, 90}},
}

const (
	rowH   = 130 // label 30 + scale 100
	labelH = 30
	bandW  = epd.Width / 16
)

func rowRect(i int) epd.Rect {
	return epd.Rect{X: 0, Y: 10 + i*rowH, W: epd.Width, H: rowH - 10}
}

func sum(c *epd.Contrast) int {
	s := 0
	for _, v := range c {
		s += int(v)
	}
	return s
}

func main() {
	// First of all: put the 74HCT4094 register into the "power off" state.
	initErr := epd.Init()

	time.Sleep(3 * time.Second)
	if initErr != nil {
		println("contrast: Init error:", initErr.Error())
		halt()
	}

	display.ClearBuffer()
	for i := range candidates {
		r := rowRect(i)
		label := candidates[i].name + "  (sum " + strconv.Itoa(sum(&candidates[i].c)) + ")"
		tinyfont.WriteLine(&display, &freesans.Regular12pt7b, 8, int16(r.Y+22), label, color.RGBA{A: 255})
		for g := 0; g < 16; g++ {
			display.FB.FillRect(g*bandW, r.Y+labelH, bandW, r.H-labelH, uint8(g))
		}
	}

	epd.PowerOn()
	start := time.Now()
	if err := epd.Clear(); err != nil {
		fail("clear", err)
	}
	println("contrast: clear", time.Since(start).Milliseconds(), "ms")
	for i := range candidates {
		epd.SetContrast(&candidates[i].c)
		start = time.Now()
		if err := epd.DrawFramebufferArea(&display.FB, rowRect(i), epd.BlackOnWhite); err != nil {
			fail("draw", err)
		}
		println("contrast:", candidates[i].name, "sum", sum(&candidates[i].c), "-", time.Since(start).Milliseconds(), "ms")
	}
	epd.SetContrast(nil)
	epd.PowerOff()
	println("contrast: done, power is off")
	halt()
}

func fail(what string, err error) {
	epd.PowerOff()
	println("contrast: error", what+":", err.Error(), "- power is off")
	halt()
}

func halt() {
	for i := 0; ; i++ {
		println("tick", i)
		time.Sleep(time.Second)
	}
}

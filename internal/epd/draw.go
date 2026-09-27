// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package epd

import (
	"errors"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd/raster"
)

// Panel output. Power must be on (PowerOn). On a bus error the functions return
// it; the caller must turn power off (PowerOff).

var errBadImage = errors.New("epd: data size does not match the area")

// Clear clears the whole screen (epd_clear).
func Clear() error {
	return ClearArea(FullScreen)
}

// ClearArea clears an area: 4 cycles of 4 frames "to black" and 4 frames
// "to white" (epd_clear_area). The area is clipped to the screen.
func ClearArea(a Rect) error {
	return ClearAreaCycles(a, 4, 50)
}

// ClearAreaCycles is epd_clear_area_cycles: cycles cycles with frame time
// cycleTime (each row takes cycleTime × 10 ticks of 0.1 µs). The area is clipped
// to the screen.
func ClearAreaCycles(a Rect, cycles int, cycleTime int16) error {
	a = clipRect(a)
	if a.W <= 0 || a.H <= 0 {
		return nil
	}
	gen.ClearAreaCycles(sink, a, cycles, cycleTime)
	return sink.takeErr()
}

// DrawImage draws an image into area a in 15 frames (epd_draw_image). data is
// 4 bits per pixel (0 = black, 15 = white), the low nibble is the left pixel,
// and each row is ImageStride(a.W) bytes. For BlackOnWhite the area must be
// cleared first.
func DrawImage(a Rect, data []byte, mode DrawMode) error {
	if a.W <= 0 || a.H <= 0 || len(data) < ImageStride(a.W)*a.H {
		return errBadImage
	}
	gen.DrawImage(sink, a, data, mode)
	return sink.takeErr()
}

// ImageStride returns the number of bytes per row of a DrawImage image of width w.
func ImageStride(w int) int { return w/2 + w%2 }

// DrawGrayscale draws the whole framebuffer (epd_draw_grayscale_image over the
// full screen, or epd_draw_image with the given mode).
func DrawGrayscale(fb *Framebuffer, mode DrawMode) error {
	return DrawImage(FullScreen, fb[:], mode)
}

// DrawFramebufferArea draws area a of the framebuffer. The result is the same as
// DrawImage with the area cut out of the buffer (checked by the raster tests),
// but without copying. The area is clipped to the screen.
func DrawFramebufferArea(fb *Framebuffer, a Rect, mode DrawMode) error {
	gen.DrawFramebufferArea(sink, fb, a, mode)
	return sink.takeErr()
}

func clipRect(a Rect) Rect {
	if a.X < 0 {
		a.W += a.X
		a.X = 0
	}
	if a.Y < 0 {
		a.H += a.Y
		a.Y = 0
	}
	if a.X+a.W > Width {
		a.W = Width - a.X
	}
	if a.Y+a.H > Height {
		a.H = Height - a.Y
	}
	return a
}

// DefaultContrast is the original frame time table (contrast_cycles_4).
var DefaultContrast = raster.DefaultContrast

// SetContrast sets the time table for the 15 image frames (ticks of 0.1 µs,
// darkest first) for BlackOnWhite and WhiteOnWhite. nil means the original
// table. Level v gets frames 0…14−v.
func SetContrast(c *Contrast) {
	gen.SetContrast(c, nil)
}

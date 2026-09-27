// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package epd

import "image/color"

// Display is a framebuffer with panel output. Its Size, SetPixel and Display
// methods match the drivers.Displayer interface from tinygo.org/x/drivers, so
// tinyfont, tinydraw and similar packages work with it. The buffer takes
// 253 KB, so declare Display as a global variable, not on the stack.
//
// Display and DisplayArea turn panel power on and off themselves.
type Display struct {
	FB Framebuffer
}

// Size returns the screen size in pixels.
func (d *Display) Size() (x, y int16) {
	return Width, Height
}

// SetPixel sets a pixel. The color is converted to luminance (like
// color.GrayModel) and rounded down to 16 levels. Alpha is ignored.
func (d *Display) SetPixel(x, y int16, c color.RGBA) {
	lum := (19595*uint32(c.R) + 38470*uint32(c.G) + 7471*uint32(c.B) + 1<<15) >> 16
	d.FB.SetPixel(int(x), int(y), uint8(lum>>4))
}

// ClearBuffer fills the buffer with white. It does not update the screen.
func (d *Display) ClearBuffer() {
	d.FB.Fill(15)
}

// Display outputs the whole buffer: power on, clear, 15 BlackOnWhite frames,
// power off (the typical cycle in the LilyGo examples).
func (d *Display) Display() error {
	PowerOn()
	err := Clear()
	if err == nil {
		err = DrawGrayscale(&d.FB, BlackOnWhite)
	}
	PowerOff()
	return err
}

// DisplayArea updates only area a: power on, epd_clear_area, output of the area
// from the buffer, power off. The rest of the screen is unchanged.
func (d *Display) DisplayArea(a Rect) error {
	PowerOn()
	err := ClearArea(a)
	if err == nil {
		err = DrawFramebufferArea(&d.FB, a, BlackOnWhite)
	}
	PowerOff()
	return err
}

// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package raster

// Framebuffer is the panel framebuffer, 4 bits (16 gray levels) per pixel:
// 0 is black, 15 is white. In each byte the low nibble is the even x and the
// high nibble is the odd x, as in the original epd_draw_pixel(). Size:
// 960×540/2 = 259200 bytes.
type Framebuffer [Width * Height / 2]byte

// Fill fills the whole buffer with level gray (0–15).
func (fb *Framebuffer) Fill(gray uint8) {
	g := gray & 0x0F
	b := g | g<<4
	for i := range fb {
		fb[i] = b
	}
}

// SetPixel sets a pixel to level gray (0–15). Points outside the screen are ignored.
func (fb *Framebuffer) SetPixel(x, y int, gray uint8) {
	if x < 0 || x >= Width || y < 0 || y >= Height {
		return
	}
	i := y*FBLineBytes + x/2
	if x%2 == 0 {
		fb[i] = fb[i]&0xF0 | gray&0x0F
	} else {
		fb[i] = fb[i]&0x0F | gray<<4
	}
}

// FillRect fills a rectangle with level gray.
func (fb *Framebuffer) FillRect(x, y, w, h int, gray uint8) {
	for j := y; j < y+h; j++ {
		for i := x; i < x+w; i++ {
			fb.SetPixel(i, j, gray)
		}
	}
}

// Line returns row y of the buffer (FBLineBytes bytes).
func (fb *Framebuffer) Line(y int) []byte {
	return fb[y*FBLineBytes : (y+1)*FBLineBytes]
}

// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package raster

// Frame is the frame generator state. It corresponds to the static data of
// epd_driver.c: skipping (the skipped-row counter, shared by all calls as in the
// original), buffer (the current bus row) and the provide_out line. The zero
// value is ready to use (as after epd_init).
type Frame struct {
	skipping uint32
	cur      *BusRow // "current buffer": the row shifted out last

	rows    [2]BusRow // the next row is prepared while the previous one is on the bus
	next    int
	zero    BusRow // zeros, i.e. "leave as is" (memset in skip_row)
	push    BusRow // the epd_push_pixels row
	pushTmp [LineBytes]byte

	line    [FBLineBytes]byte // line in provide_out
	shifted bool
	tbl     [256]byte

	// contrast and contrastWhite are the frame time tables; nil means the original tables.
	contrast, contrastWhite *Contrast
}

// SetContrast sets the frame time tables for the *_ON_WHITE modes (c) and
// WhiteOnBlack (white). nil means the original table. A custom table is a
// deviation from the original; the comparison tests use the default tables.
func (f *Frame) SetContrast(c, white *Contrast) {
	f.contrast, f.contrastWhite = c, white
}

// Reset restores the initial state (skipping = 0 in epd_init).
func (f *Frame) Reset() {
	f.skipping = 0
	f.cur = &f.zero
}

func (f *Frame) current() *BusRow {
	if f.cur == nil {
		f.cur = &f.zero
	}
	return f.cur
}

// writeRow — write_row().
func (f *Frame) writeRow(s Sink, ticks uint32) {
	f.skipping = 0
	s.OutputRow(ticks, f.current())
}

// skipRow is skip_row(uint8_t pipeline_finish_time). The parameter is 8-bit as
// in the original: feed_display passes contrast_lut[frame] here, and 300 becomes
// 44.
func (f *Frame) skipRow(s Sink, finish uint8) {
	switch {
	case f.skipping == 0:
		// Output the previously loaded row, load zeros.
		f.cur = &f.zero
		s.OutputRow(uint32(finish), f.cur)
	case f.skipping < 2:
		s.OutputRow(10, f.current())
	default:
		s.Skip()
	}
	f.skipping++
}

// PushPixels is epd_push_pixels: one frame in which every pixel of the area is
// driven "to white" (white) or "to black" for time × 10 ticks.
//
// Difference from the original: no reorder_line_buffer(). That swap of the
// 16-bit halves of 32-bit words is needed for I2S on ESP32, but on S3 (esp_lcd,
// bytes in memory order) it shifts the edges of a partial-width area by
// ±8 pixels; the S3 image path does not do it. For full-width fills the result
// is the same. The area must lie within the screen in X (otherwise the C code
// writes past the buffer).
func (f *Frame) PushPixels(s Sink, a Rect, time int16, white bool) {
	b := byte(darkByte)
	if white {
		b = clearByte
	}
	row := &f.pushTmp
	for i := range row {
		row[i] = 0
	}
	for i := 0; i < a.W; i++ {
		position := i + a.X%4
		mask := b & (0b11 << (2 * (position % 4)))
		row[a.X/4+position/4] |= mask
	}

	writeTicks := uint32(int32(time) * 10)
	s.StartFrame()
	for i := 0; i < Height; i++ {
		switch {
		case i < a.Y:
			f.skipRow(s, uint8(time))
		case i == a.Y:
			copy(f.push.B[:LineBytes], row[:])
			f.cur = &f.push
			f.writeRow(s, writeTicks)
		case i >= a.Y+a.H:
			f.skipRow(s, uint8(time))
		default:
			f.writeRow(s, writeTicks)
		}
	}
	// Output is pipelined: one more step latches the last row.
	f.writeRow(s, writeTicks)
	s.EndFrame()
}

// ClearAreaCycles is epd_clear_area_cycles: cycles times 4 frames "to black"
// and 4 frames "to white" of length cycleTime.
func (f *Frame) ClearAreaCycles(s Sink, a Rect, cycles int, cycleTime int16) {
	for c := 0; c < cycles; c++ {
		for i := 0; i < 4; i++ {
			f.PushPixels(s, a, cycleTime, false)
		}
		for i := 0; i < 4; i++ {
			f.PushPixels(s, a, cycleTime, true)
		}
	}
}

// ClearArea is epd_clear_area: 4 cycles of 50.
func (f *Frame) ClearArea(s Sink, a Rect) {
	f.ClearAreaCycles(s, a, 4, 50)
}

// ImageStride returns the bytes per row of a DrawImage image of width w
// (area.width / 2 + area.width % 2 in provide_out).
func ImageStride(w int) int {
	return w/2 + w%2
}

// DrawImage is epd_draw_image(area, data, mode): 15 frames. data is the image
// of the area (4 bits per pixel, low nibble first, rows of ImageStride(a.W)
// bytes); for the whole screen it is the framebuffer.
func (f *Frame) DrawImage(s Sink, a Rect, data []byte, mode Mode) {
	for k := 0; k < GrayFrames; k++ {
		f.drawFrame(s, a, k, mode, func(ptr *int) []byte { return f.imageLine(a, data, ptr) })
	}
}

// DrawFramebufferArea draws area a straight from the framebuffer (15 frames).
// The result is the same as DrawImage with the area image cut out of the buffer
// (checked by a test), but without a copy: pixels outside the area are replaced
// with 0xF. The area is clipped to the screen.
func (f *Frame) DrawFramebufferArea(s Sink, fb *Framebuffer, a Rect, mode Mode) {
	a = clip(a)
	if a.W <= 0 || a.H <= 0 {
		return
	}
	for k := 0; k < GrayFrames; k++ {
		f.drawFrame(s, a, k, mode, func(ptr *int) []byte {
			y := a.Y + *ptr
			*ptr++
			return f.framebufferLine(fb, a, y)
		})
	}
}

// drawFrame is one frame: provide_out (next returns the next row of the area)
// and feed_display.
func (f *Frame) drawFrame(s Sink, a Rect, k int, mode Mode, next func(ptr *int) []byte) {
	contrast := f.contrast
	if contrast == nil {
		contrast = &DefaultContrast
	}
	if mode == WhiteOnBlack {
		contrast = f.contrastWhite
		if contrast == nil {
			contrast = &DefaultContrastWhite
		}
	}
	ticks := contrast[k]
	buildFrameTable(&f.tbl, k, mode)

	// provide_out: memset(line, 255) at the start of every frame.
	for i := range f.line {
		f.line[i] = 0xFF
	}
	f.shifted = false
	ptr := 0
	if a.X < 0 {
		ptr += -a.X / 2
	}
	if a.Y < 0 {
		ptr += ImageStride(a.W) * -a.Y
	}

	s.StartFrame()
	for i := 0; i < Height; i++ {
		if i < a.Y || i >= a.Y+a.H {
			f.skipRow(s, uint8(ticks))
			continue
		}
		line := next(&ptr)
		row := &f.rows[f.next]
		f.next ^= 1
		f.prepRow(s, row, line)
		f.cur = row
		f.writeRow(s, ticks)
	}
	if f.skipping == 0 {
		// One more step latches the last row (the buffer is sent again).
		f.writeRow(s, ticks)
	}
	s.EndFrame()
}

// prepRow is calc_epd_input_4bpp: a buffer row → bus bytes via the frame table.
// Bus byte j holds pixels 4j…4j+3, with pixel 4j in bits 0–1.
func (f *Frame) prepRow(s Sink, row *BusRow, line []byte) {
	for j := 0; j < LineBytes; j++ {
		row.B[j] = f.tbl[line[2*j]] | f.tbl[line[2*j+1]]<<4
		if j&7 == 7 {
			s.Poll()
		}
	}
}

// imageLine prepares a row of the area as in provide_out(); *ptr is the offset into data.
func (f *Frame) imageLine(a Rect, data []byte, ptr *int) []byte {
	if f.shifted {
		// memset(line, 255) after sending a shifted row.
		for i := range f.line {
			f.line[i] = 0xFF
		}
		f.shifted = false
	}
	if a.W == Width && a.X == 0 {
		line := data[*ptr : *ptr+FBLineBytes]
		*ptr += FBLineBytes
		return line
	}

	bufStart := 0
	lineBytes := uint32(ImageStride(a.W))
	if a.X >= 0 {
		bufStart += a.X / 2
	} else {
		// Reduce line_bytes to the bytes actually used (a.X/2 ≤ 0).
		lineBytes += uint32(a.X / 2)
	}
	if bufStart >= FBLineBytes {
		// In C this is an unsigned overflow and a write past the buffer; there is
		// nothing to draw off screen.
		lineBytes = 0
	} else {
		lineBytes = min(lineBytes, uint32(FBLineBytes-bufStart))
	}
	n := int(lineBytes)
	copy(f.line[bufStart:bufStart+n], data[*ptr:*ptr+n])
	*ptr += ImageStride(a.W)

	// Mask the last nibble for an odd width.
	if a.W%2 == 1 && a.X/2+a.W/2+1 < Width && n > 0 {
		f.line[bufStart+n-1] |= 0xF0
	}
	if a.X%2 == 1 && a.X < Width {
		f.shifted = true
		nibbleShiftRight(f.line[bufStart:], min(uint32(n)+1, uint32(FBLineBytes-bufStart)))
	}
	return f.line[:]
}

// nibbleShiftRight is nibble_shift_buffer_right: a shift one pixel to the
// right; the freed first nibble becomes 0xF.
func nibbleShiftRight(buf []byte, n uint32) {
	carry := byte(0xF)
	for i := uint32(0); i < n; i++ {
		val := buf[i]
		buf[i] = val<<4 | carry
		carry = (val & 0xF0) >> 4
	}
}

// framebufferLine returns row y of the framebuffer with pixels outside
// [a.X, a.X+a.W) replaced by 0xF ("leave as is" in the *_ON_WHITE modes). a is
// already clipped to the screen.
func (f *Frame) framebufferLine(fb *Framebuffer, a Rect, y int) []byte {
	src := fb.Line(y)
	x0, x1 := a.X, a.X+a.W
	if x0 == 0 && x1 == Width {
		return src
	}
	for i := range f.line {
		f.line[i] = 0xFF
	}
	b0, b1 := (x0+1)/2, x1/2 // bytes that lie entirely inside the area
	if b1 > b0 {
		copy(f.line[b0:b1], src[b0:b1])
	}
	if x0%2 == 1 { // odd x0: only the high nibble of byte x0/2
		f.line[x0/2] = src[x0/2]&0xF0 | 0x0F
	}
	if x1%2 == 1 { // last pixel x1−1 is even: the low nibble of byte x1/2
		f.line[x1/2] = 0xF0 | src[x1/2]&0x0F
	}
	return f.line[:]
}

func clip(a Rect) Rect {
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

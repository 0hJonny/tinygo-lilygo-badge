// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

// Package raster is the hardware-independent part of the ED047TC1 driver: which
// rows and skips make up a frame and which bytes go on the bus. It is a
// line-by-line port of epd_driver.c (LilyGo-EPD47, esp32s3 branch). The package
// does not depend on hardware and is checked on the host byte-for-byte against
// the original C code (raster_test.go, testdata/cref).
package raster

const (
	Width  = 960
	Height = 540

	// LineBytes is the number of bus bytes per data row: 2 bits per pixel (EPD_LINE_BYTES).
	LineBytes = Width / 4
	// BusRowBytes is the length of a bus transfer: a row plus 32 pixels of
	// margin ((960 + 32) / 4 in i2s_start_line_output).
	BusRowBytes = (Width + 32) / 4
	// FBLineBytes is the number of framebuffer bytes per row (4 bits per pixel).
	FBLineBytes = Width / 2

	// GrayFrames is the number of frames per image (frame_count in epd_draw_image).
	GrayFrames = 15

	clearByte = 0b10101010 // CLEAR_BYTE: "to white"
	darkByte  = 0b01010101 // DARK_BYTE: "to black"
)

// Rect is a screen area (Rect_t).
type Rect struct {
	X, Y, W, H int
}

// FullScreen is the whole screen (epd_full_screen).
var FullScreen = Rect{0, 0, Width, Height}

// Mode is the image drawing mode (DrawMode_t).
type Mode uint8

const (
	// BlackOnWhite draws dark on a white screen (after clearing). The main mode.
	BlackOnWhite Mode = iota
	// WhiteOnWhite draws "with white ink" on a white screen.
	WhiteOnWhite
	// WhiteOnBlack draws white on a black screen.
	WhiteOnBlack
)

// Contrast is the row time for each of the 15 frames, in ticks of 0.1 µs
// (darkest first). In BlackOnWhite mode level v gets frames 0…14−v, i.e.
// Contrast[0] + … + Contrast[14−v] in total.
type Contrast [GrayFrames]uint32

// DefaultContrast and DefaultContrastWhite are the original tables:
// contrast_cycles_4 and contrast_cycles_4_white from epd_driver.c.
var (
	DefaultContrast      = Contrast{30, 30, 20, 20, 30, 30, 30, 40, 40, 50, 50, 50, 100, 200, 300}
	DefaultContrastWhite = Contrast{10, 10, 8, 8, 8, 8, 8, 10, 10, 15, 15, 20, 20, 100, 300}
)

// BusRow is a bus row. The zero-length uint32 array aligns it to 4 bytes (for
// DMA). The generator writes only the first LineBytes bytes; the tail stays
// zero, like the static buffer in i2s_data_bus.c.
type BusRow struct {
	_ [0]uint32
	B [BusRowBytes]byte
}

// Sink is the low level (ed047tc1.c + bus) that the generator hands a frame to.
type Sink interface {
	// StartFrame is epd_start_frame.
	StartFrame()
	// OutputRow is epd_output_row: latch the previously shifted row, pulse CKV
	// for ticks (0.1 µs each), and start shifting out row. row is not modified
	// until the next OutputRow call.
	OutputRow(ticks uint32, row *BusRow)
	// Skip is epd_skip: only a CKV pulse (advance to the next row).
	Skip()
	// EndFrame is epd_end_frame.
	EndFrame()
	// Poll is called while a row is being prepared (to drop CKV on time).
	Poll()
}

// pixelCode returns the 2 bus bits for a pixel of level v in frame k: 01 "to
// black", 10 "to white", 00 "leave as is". It is the closed form of the
// cumulative reset_lut() + update_LUT() table: a pixel stops being driven from
// the frame where k (15 − k for *_ON_WHITE) equals its level.
func pixelCode(v uint8, k int, mode Mode) byte {
	switch mode {
	case BlackOnWhite: // 15 − v frames "to black"
		if int(v) < 15-k {
			return 0b01
		}
	case WhiteOnWhite:
		if int(v) < 15-k {
			return 0b10
		}
	case WhiteOnBlack: // v frames "to white"
		if int(v) > k {
			return 0b10
		}
	}
	return 0b00
}

// buildFrameTable maps a framebuffer byte (2 pixels) to 4 bus bits for frame k.
// It replaces the 64 KB conversion_lut (2 bytes → 1 bus byte) with a 256-byte
// table.
func buildFrameTable(tbl *[256]byte, k int, mode Mode) {
	for b := 0; b < 256; b++ {
		lo := pixelCode(uint8(b&0x0F), k, mode)
		hi := pixelCode(uint8(b>>4), k, mode)
		tbl[b] = lo | hi<<2
	}
}

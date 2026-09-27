// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package epd

import (
	"runtime/interrupt"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd/raster"
)

// Frame sinks: the raster.Frame generator calls their methods, and they drive
// the hardware (ed047tc1.c + bus). Interrupts are disabled for the duration of
// a frame so that CKV timing does not drift. A bus error "sticks": the rest of
// the frame's calls are skipped, but EndFrame still clears OE and MODE.

var (
	gen  raster.Frame
	sink frameSink
)

type frameSink interface {
	raster.Sink
	takeErr() error
}

// dmaSink uses the LCD_CAM + GDMA bus. While DMA shifts out a row, the generator
// prepares the next one and drops CKV on time via Poll (the original uses two
// cores for this).
type dmaSink struct {
	t   ckvTimer
	err error
	irq interrupt.State
}

func (s *dmaSink) StartFrame() {
	s.irq = interrupt.Disable()
	if s.err == nil {
		s.err = lcdWaitIdle() // while (i2s_is_busy()) in epd_start_frame
	}
	if s.err == nil {
		startFrame()
	}
}

func (s *dmaSink) OutputRow(ticks uint32, row *raster.BusRow) {
	if s.err != nil {
		return
	}
	s.t.finish()
	if s.err = lcdWaitIdle(); s.err != nil { // while (i2s_is_busy()) in epd_output_row
		return
	}
	s.t = beginRow(ticks)
	lcdSendRow(row)
}

func (s *dmaSink) Skip() {
	if s.err != nil {
		return
	}
	s.t.finish()
	pulseSkip()
}

func (s *dmaSink) Poll() { s.t.poll() }

func (s *dmaSink) EndFrame() {
	s.t.finish()
	if s.err == nil {
		s.err = lcdWaitIdle()
	}
	endFrame()
	interrupt.Restore(s.irq)
}

func (s *dmaSink) takeErr() error {
	err := s.err
	s.err = nil
	return err
}

// bitbangSink is the software bus: the row is shifted out inside OutputRow, and
// CKV is dropped by the timer while shifting.
type bitbangSink struct {
	irq interrupt.State
}

func (s *bitbangSink) StartFrame() {
	s.irq = interrupt.Disable()
	startFrame()
}

func (s *bitbangSink) OutputRow(ticks uint32, row *raster.BusRow) {
	t := beginRow(ticks)
	shiftRow(row.B[:], &t)
	t.finish()
}

func (s *bitbangSink) Skip() { pulseSkip() }
func (s *bitbangSink) Poll() {}

func (s *bitbangSink) EndFrame() {
	endFrame()
	interrupt.Restore(s.irq)
}

func (s *bitbangSink) takeErr() error { return nil }

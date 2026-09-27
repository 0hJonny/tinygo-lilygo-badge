// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package epd

import (
	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd/raster"
)

// Types and constants of the frame generator (package raster), re-exported so
// that programs need a single import.
type (
	Rect        = raster.Rect
	Framebuffer = raster.Framebuffer
	DrawMode    = raster.Mode
	Contrast    = raster.Contrast
)

const (
	Width  = raster.Width
	Height = raster.Height

	BlackOnWhite = raster.BlackOnWhite
	WhiteOnWhite = raster.WhiteOnWhite
	WhiteOnBlack = raster.WhiteOnBlack
)

// FullScreen is the whole screen.
var FullScreen = raster.FullScreen

var cfg cfgReg

// UseDMA selects the data bus: true means LCD_CAM + GDMA, like esp_lcd in the
// original (default); false means a software (bit-bang) bus, for comparison and
// debugging. Change it before calling Init.
var UseDMA = true

// Init configures the pins and sets the initial register state (epd_base_init,
// epd_init). The 74HCT4094 register is cleared first, before the bus is set up,
// so the panel is guaranteed to be unpowered if anything fails.
func Init() error {
	configureOutput(pinCfgData, false)
	configureOutput(pinCfgClk, false)
	configureOutput(pinCfgStr, false)

	// All bits 0, like epd_poweroff_all(): PWR_EN = 0, the panel is unpowered.
	// The original epd_base_init sets ep_scan_direction (= PWR_EN on V2.4) to 1,
	// i.e. it turns on the panel's high voltage right at initialization.
	PowerOffAll()

	gen.Reset() // skipping = 0 in epd_init

	configureOutput(pinCKV, false)
	if UseDMA {
		// STH = DC of the i80 bus (low when idle), CKH = WR (high when idle).
		sink = &dmaSink{}
		return lcdInit()
	}
	initBusMasks()
	configureOutput(pinSTH, true)
	configureOutput(pinCKH, true)
	for _, p := range busPins {
		configureOutput(p, false)
	}
	sink = &bitbangSink{}
	return nil
}

// PowerOn turns on panel power (epd_poweron).
//
// On V2.4 panel power is controlled by PWR_EN (QP5) alone: through Q2/Q4 it
// switches the 3V3 rail, from which the LT1945 (SHDN pulled up to 3V3)
// immediately produces +22/−20 V, and the 78L15/79L15 produce ±15 V. The
// SMPS/POS/NEG_CTRL bits go nowhere on V2.4 (R6, R7, R21 are not fitted), so the
// "negative first, then positive" sequence has no effect here. The sequence is
// kept as in the original for other board revisions.
func PowerOn() {
	cfg.pwrEn = true
	cfg.smpsCtrl = false
	cfg.push()
	waitMicros(100)
	cfg.negCtrl = true
	cfg.push()
	waitMicros(500)
	cfg.posCtrl = true
	cfg.push()
	waitMicros(100)
	cfg.stv = true
	cfg.push()
}

// PowerOff turns off panel power: the epd_poweroff() sequence, then clearing the
// whole register (epd_poweroff_all).
//
// The second step is required on V2.4: epd_poweroff() does not clear
// ep_scan_direction, which is PWR_EN. Without it the panel's boost converter
// stays on (visible on LED7 on the 3V3 rail).
func PowerOff() {
	cfg.posCtrl = false
	cfg.push()
	waitMicros(10)
	cfg.negCtrl = false
	cfg.push()
	waitMicros(100)
	cfg.smpsCtrl = true
	cfg.push()

	cfg.stv = false
	cfg.push()

	PowerOffAll()
}

// startFrame is epd_start_frame: an STV pulse on the first CKV clock.
// The caller waits for the previous row transfer to finish beforehand
// (while (i2s_is_busy())).
func startFrame() {
	cfg.mode = true
	cfg.push()

	pulseCKVMicros(1, 1)

	// Timing-critical: STV low is captured by the CKV rising edge, then STV goes
	// back high while CKV is still high.
	cfg.stv = false
	cfg.push()
	waitMicros(1)
	start := ccount()
	ckvHigh()
	cfg.stv = true
	cfg.push()
	waitUntil(start, 10*cpuMHz)
	ckvLow()
	waitMicros(10)
	pulseCKVMicros(0, 10)

	cfg.outputEnable = true
	cfg.push()

	pulseCKVMicros(1, 1)
}

func endFrame() {
	cfg.outputEnable = false
	cfg.push()
	cfg.mode = false
	cfg.push()
	pulseCKVMicros(1, 1)
	pulseCKVMicros(1, 1)
}

func latchRow() {
	cfg.latchEnable = true
	cfg.push()
	cfg.latchEnable = false
	cfg.push()
}

// beginRow is the start of epd_output_row(): latch the previously shifted row
// (LE) and raise CKV for outputTicks (0.1 µs each). The timer drops CKV.
func beginRow(outputTicks uint32) ckvTimer {
	waitCKVReady()
	latchRow()
	t := ckvTimer{start: ccount(), high: outputTicks * cyclesPerTick, on: true}
	ckvHigh()
	return t
}

// PowerOffAll clears all 8 bits of the 74HCT4094 (epd_poweroff_all): PWR_EN = 0.
func PowerOffAll() {
	cfg = cfgReg{}
	cfg.push()
}

// Bits for ProbeCfg, one per 74HCT4094 output. POS/NEG_CTRL are never set.
// ProbePwrEn turns on panel power (3V3 and high voltage).
const (
	ProbeLatchEnable = 1 << iota
	ProbeSmpsCtrl
	ProbeSTV
	ProbePwrEn
	ProbeMode
	ProbeOutputEnable
)

// ProbeCfg is a diagnostic: it sets the selected register bits and clears the rest.
func ProbeCfg(bits uint8) {
	cfg = cfgReg{
		latchEnable:  bits&ProbeLatchEnable != 0,
		smpsCtrl:     bits&ProbeSmpsCtrl != 0,
		stv:          bits&ProbeSTV != 0,
		pwrEn:        bits&ProbePwrEn != 0,
		mode:         bits&ProbeMode != 0,
		outputEnable: bits&ProbeOutputEnable != 0,
	}
	cfg.push()
}

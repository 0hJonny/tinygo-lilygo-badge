// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package epd

import (
	"device/esp"
	"machine"
)

// busAllMask covers all 8 data bus pins (all of them are < 32).
var busAllMask uint32

// busSetMask[b] lists the pins to raise to put byte b on the bus.
var busSetMask [256]uint32

func initBusMasks() {
	for _, p := range busPins {
		busAllMask |= 1 << p
	}
	for b := 0; b < 256; b++ {
		var m uint32
		for k := 0; k < 8; k++ {
			if b&(1<<k) != 0 {
				m |= 1 << busPins[k]
			}
		}
		busSetMask[b] = m
	}
}

func configureOutput(p machine.Pin, high bool) {
	p.Configure(machine.PinConfig{Mode: machine.PinOutput})
	p.Set(high)
}

// A row is sent to the bus byte by byte, clocked by CKH, while STH is low. This
// replaces esp_lcd_panel_io_tx_color() (i80, WR = CKH, DC = STH) from
// i2s_data_bus.c.
//
// In the original, loading the row (DMA) and the CKV pulse (RMT) run in
// parallel: CKV stays high for exactly the given time, even if loading takes
// longer. Here the same is done by checking CCOUNT after every byte: as soon as
// the time is up, CKV goes low right in the middle of loading.

// putByte puts a byte on the bus and pulses CKH (data is latched on the rising edge).
func putByte(b byte) {
	set := busSetMask[b]
	esp.GPIO.OUT_W1TC.Set(busAllMask &^ set)
	esp.GPIO.OUT_W1TS.Set(set)
	esp.GPIO.OUT1_W1TC.Set(mask1CKH)
	esp.GPIO.OUT1_W1TS.Set(mask1CKH)
}

// ckvTimer drops CKV once high cycles have passed since start.
type ckvTimer struct {
	start, high uint32
	on          bool
}

func (t *ckvTimer) poll() {
	if t.on && ccount()-t.start >= t.high {
		ckvLow()
		t.on = false
		ckvFell(ckvRowLowTicks)
	}
}

// finish waits for the end of the CKV pulse if loading was shorter than the pulse.
func (t *ckvTimer) finish() {
	if t.on {
		waitUntil(t.start, t.high)
		ckvLow()
		t.on = false
		ckvFell(ckvRowLowTicks)
	}
}

// shiftRow outputs ready bus bytes (already 2 bits per pixel).
func shiftRow(row []byte, t *ckvTimer) {
	esp.GPIO.OUT1_W1TC.Set(mask1STH)
	for _, b := range row {
		putByte(b)
		t.poll()
	}
	esp.GPIO.OUT1_W1TS.Set(mask1STH)
}

// CKV times are given in ticks of 0.1 µs, like the RMT in rmt_pulse.c.
const cyclesPerTick = cpuMHz / 10

func ckvHigh() { esp.GPIO.OUT1_W1TS.Set(mask1CKV) }
func ckvLow()  { esp.GPIO.OUT1_W1TC.Set(mask1CKV) }

// In the original, CKV pulses go through the RMT queue: the next one starts no
// earlier than the low phase of the previous one ends. Here that time is
// tracked: lastCKVFall is the moment of the falling edge, lastCKVLow is how many
// cycles CKV must stay low after it.
var lastCKVFall, lastCKVLow uint32

const (
	ckvRowLowTicks   = 50 // pulse_ckv_ticks(output_time_dus, 50) in epd_output_row
	ckvSkipHighTicks = 45 // epd_skip: pulse_ckv_ticks(45, 5)
	ckvSkipLowTicks  = 5
)

func ckvFell(lowTicks uint32) {
	lastCKVFall = ccount()
	lastCKVLow = lowTicks * cyclesPerTick
}

// waitCKVReady waits for the low phase of the previous pulse to end.
func waitCKVReady() {
	waitUntil(lastCKVFall, lastCKVLow)
}

// pulseCKVTicks outputs a CKV pulse and waits for it to finish
// (pulse_ckv_ticks(…, wait=true)): high for highTicks, then low for lowTicks. As
// in the original, highTicks == 0 produces a high pulse lowTicks long.
func pulseCKVTicks(highTicks, lowTicks uint32) {
	if highTicks == 0 {
		highTicks, lowTicks = lowTicks, 0
	}
	waitCKVReady()
	ckvHigh()
	waitCycles(highTicks * cyclesPerTick)
	ckvLow()
	ckvFell(lowTicks)
	waitCKVReady()
}

// pulseSkip is epd_skip(): a 4.5 µs CKV pulse without latch or data; the 0.5 µs
// low phase is not waited for (pulse_ckv_ticks(45, 5, false) in the original).
func pulseSkip() {
	waitCKVReady()
	ckvHigh()
	waitCycles(ckvSkipHighTicks * cyclesPerTick)
	ckvLow()
	ckvFell(ckvSkipLowTicks)
}

func pulseCKVMicros(highUs, lowUs uint32) {
	pulseCKVTicks(highUs*10, lowUs*10)
}

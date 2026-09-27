// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package epd

import "device"

// CPU frequency set by the TinyGo runtime on ESP32-S3.
const cpuMHz = 240

// ccount reads the Xtensa core cycle counter (CCOUNT register).
func ccount() uint32 {
	return uint32(device.AsmFull("rsr {}, ccount", nil))
}

// waitCycles busy-waits for the given number of cycles (like busy_delay in
// ed047tc1.c).
func waitCycles(cycles uint32) {
	start := ccount()
	for ccount()-start < cycles {
	}
}

// waitUntil waits until cycles cycles have passed since start.
func waitUntil(start, cycles uint32) {
	for ccount()-start < cycles {
	}
}

func waitMicros(us uint32) {
	waitCycles(us * cpuMHz)
}

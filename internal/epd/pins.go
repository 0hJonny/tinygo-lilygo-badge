// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

// Package epd is a pure-Go driver for the ED047TC1 e-paper panel on the
// LilyGo T5-4.7-S3 board.
//
// It is a port of the LilyGo-EPD47 driver (copy of the original in
// test/idf-original-gray/components/epd_driver) without ESP-IDF: the 74HCT4094
// register and CKV (ed047tc1.c), and the data bus through LCD_CAM + GDMA
// (i2s_data_bus.c → esp_lcd). Frame logic lives in the raster subpackage.
package epd

import "machine"

// Pinout from ed047tc1.h (ESP32-S3 branch), confirmed by the LilyGO wiki:
// https://wiki.lilygo.cc/products/t5-series/t5-e-paper/#pin-mapping
const (
	// 74HCT4094 shift register: panel power, STV, LE, OE, MODE.
	pinCfgData = machine.GPIO13
	pinCfgClk  = machine.GPIO12
	pinCfgStr  = machine.GPIO0

	pinCKV = machine.GPIO38 // row clock (gate driver)
	pinSTH = machine.GPIO40 // row start (source driver), active low
	pinCKH = machine.GPIO41 // data clock (source driver)

	pinD0 = machine.GPIO8
	pinD1 = machine.GPIO1
	pinD2 = machine.GPIO2
	pinD3 = machine.GPIO3
	pinD4 = machine.GPIO4
	pinD5 = machine.GPIO5
	pinD6 = machine.GPIO6
	pinD7 = machine.GPIO7
)

// busPins maps bit k of a row buffer byte to the pin that outputs it. The order
// is taken from data_gpio_nums in i2s_bus_init() (i2s_data_bus.c): bit 0 goes to
// D6, bit 1 to D7, and so on.
var busPins = [8]machine.Pin{pinD6, pinD7, pinD4, pinD5, pinD2, pinD3, pinD0, pinD1}

// Masks for direct GPIO register writes. Pins 0–31 live in OUT_*, pins 32–48
// in OUT1_* (bit = pin number − 32).
const (
	maskCfgData = 1 << pinCfgData
	maskCfgClk  = 1 << pinCfgClk
	maskCfgStr  = 1 << pinCfgStr

	mask1CKV = 1 << (pinCKV - 32)
	mask1STH = 1 << (pinSTH - 32)
	mask1CKH = 1 << (pinCKH - 32)
)

// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package epd

import "device/esp"

// cfgReg holds the 8 outputs of the 74HCT4094 shift register (U3),
// epd_config_register_t in ed047tc1.c. Field names follow the Screen-4.7-S3-V2.4
// schematic (LilyGo-EPD47, schematic/); each comment gives the U3 output and
// the field name in the C driver.
type cfgReg struct {
	latchEnable  bool // QP0 → R34 0R → EP_LE           (ep_latch_enable)
	smpsCtrl     bool // QP1 → R6 NC → SMPS_CTRL       (power_disable)     not connected on V2.4
	posCtrl      bool // QP2 → R7 NC → POS_CTRL        (pos_power_enable)  not connected on V2.4
	negCtrl      bool // QP3 → R21 NC → NEG_CTRL       (neg_power_enable)  not connected on V2.4
	stv          bool // QP4 → R33 0R → EP_STV          (ep_stv)
	pwrEn        bool // QP5 → PWR_EN: panel 3V3 rail, which feeds the LT1945 boost (±15/+22/−20 V) and LED7 (ep_scan_direction)
	mode         bool // QP6 → EP_MODE                  (ep_mode)
	outputEnable bool // QP7 → EP_OE                    (ep_output_enable)
}

func pushCfgBit(bit bool) {
	esp.GPIO.OUT_W1TC.Set(maskCfgClk)
	if bit {
		esp.GPIO.OUT_W1TS.Set(maskCfgData)
	} else {
		esp.GPIO.OUT_W1TC.Set(maskCfgData)
	}
	esp.GPIO.OUT_W1TS.Set(maskCfgClk)
}

// push writes the whole register: STR low, 8 bits in reverse order, STR high.
func (c *cfgReg) push() {
	esp.GPIO.OUT_W1TC.Set(maskCfgStr)

	pushCfgBit(c.outputEnable)
	pushCfgBit(c.mode)
	pushCfgBit(c.pwrEn)
	pushCfgBit(c.stv)

	pushCfgBit(c.negCtrl)
	pushCfgBit(c.posCtrl)
	pushCfgBit(c.smpsCtrl)
	pushCfgBit(c.latchEnable)

	esp.GPIO.OUT_W1TS.Set(maskCfgStr)
}

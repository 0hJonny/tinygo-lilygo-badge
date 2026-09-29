//go:build idfi2c

package main

import (
	"machine"

	"tinygo.org/x/drivers"

	"github.com/0hJonny/tinygo-lilygo-badge/i2cidf"
)

var idfBus *i2cidf.Bus

// newBus uses i2cidf, the ESP-IDF v4.4.8 I2C master driver ported as is, with
// the configuration of the original badge (gt911_touch.cpp: internal pull-ups,
// 400 kHz, 10 ms per transaction).
func newBus(cfg machine.I2CConfig) (drivers.I2C, string) {
	idfBus = i2cidf.New(i2cidf.Config{
		SDA: cfg.SDA, SCL: cfg.SCL,
		SDAPullup: true, SCLPullup: true,
		ClkSpeed: cfg.Frequency,
	})
	return idfBus, "i2cidf (ESP-IDF v4.4.8 port)"
}

// busRecoveries reports i2c_hw_fsm_reset calls.
func busErrors() int     { return idfBus.Errors }
func busRecoveries() int { return idfBus.FSMResets }

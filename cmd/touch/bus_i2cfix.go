//go:build !stdi2c && !idfi2c

package main

import (
	"machine"

	"tinygo.org/x/drivers"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/i2cfix"
)

var fixBus *i2cfix.Bus

// newBus uses i2cfix: ESP-IDF style transactions that work around the TinyGo
// esp32xx I2C issue.
func newBus(cfg machine.I2CConfig) (drivers.I2C, string) {
	fixBus = i2cfix.New(machine.I2C0, cfg, true)
	return fixBus, "i2cfix"
}

func busErrors() int     { return fixBus.Errors }
func busRecoveries() int { return fixBus.Recoveries }

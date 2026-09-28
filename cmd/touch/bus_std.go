//go:build stdi2c

package main

import (
	"device/esp"
	"machine"

	"tinygo.org/x/drivers"
)

// newBus uses the plain machine.I2C0 (for testing a patched TinyGo), with the
// internal pull-ups enabled as in the original.
func newBus(cfg machine.I2CConfig) (drivers.I2C, string) {
	machine.I2C0.Configure(cfg)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	return machine.I2C0, "machine.I2C0"
}

// machine.I2C does not count errors or recoveries.
func busErrors() int     { return 0 }
func busRecoveries() int { return 0 }

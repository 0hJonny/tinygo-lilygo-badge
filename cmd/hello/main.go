// Minimal firmware to check that TinyGo runs on the LilyGo T5-4.7-S3 board at
// all: no display output, only USB-Serial/JTAG. The panel power register is still
// cleared, because its state is random after power-up.
package main

import (
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0), even if the
	// display is not used, because its state is random after power-up.
	epd.UseDMA = false // the display bus is not needed, only the register
	_ = epd.Init()

	// Time to open the serial monitor after a reset.
	time.Sleep(2 * time.Second)

	println("TinyGo is running on LilyGo T5-4.7-S3")

	for i := 0; ; i++ {
		println("tick", i)
		time.Sleep(time.Second)
	}
}

// Stage 1: panel check: power on, clear the screen, power off.
package main

import (
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

func main() {
	// Init right at startup: after power-up or an ESP32 reset the 74HCT4094
	// contents are undefined, and panel power may end up on.
	// Init writes the safe "power off" state to the register.
	initErr := epd.Init()

	time.Sleep(3 * time.Second)
	println("epd: init done, panel power is off")
	if initErr != nil {
		println("epd: Init error:", initErr.Error())
		for {
			time.Sleep(time.Second)
		}
	}

	println("epd: power on")
	epd.PowerOn()

	println("epd: clearing...")
	start := time.Now()
	if err := epd.Clear(); err != nil {
		epd.PowerOff()
		println("epd: clear error:", err.Error(), "- power is off")
		for {
			time.Sleep(time.Second)
		}
	}
	println("epd: clear done in", time.Since(start).Milliseconds(), "ms")

	epd.PowerOff()
	println("epd: power off")

	for i := 0; ; i++ {
		println("done, tick", i)
		time.Sleep(time.Second)
	}
}

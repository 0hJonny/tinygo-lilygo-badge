// Diagnostic: which 74HCT4094 output drives the LED on the board.
// Warning: step 5 turns on PWR_EN, which on V2.4 is panel power, including the high voltage.
package main

import (
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

func step(n int, name string, bits uint8) {
	epd.ProbeCfg(bits)
	println("step", n, ":", name, "- watch the LED for 5 s")
	time.Sleep(5 * time.Second)
}

func main() {
	epd.UseDMA = false // the bus is not needed, only the register
	_ = epd.Init()
	time.Sleep(3 * time.Second)

	// One round, then everything off: step 5 turns on PWR_EN (high voltage) for
	// 5 s, and there is no reason to repeat that forever.
	for round := 1; round <= 1; round++ {
		println("=== round", round, "===")

		epd.PowerOff()
		println("step 1 : state after PowerOff (as in cmd/clear) - 5 s")
		time.Sleep(5 * time.Second)

		epd.PowerOffAll()
		println("step 2 : PowerOffAll, all bits 0 - 5 s")
		time.Sleep(5 * time.Second)

		step(3, "only SMPS_CTRL (power_disable)", epd.ProbeSmpsCtrl)
		step(4, "only STV", epd.ProbeSTV)
		step(5, "only PWR_EN (scan_direction) - panel power", epd.ProbePwrEn)
		step(6, "only MODE", epd.ProbeMode)
		step(7, "only OE", epd.ProbeOutputEnable)
		step(8, "only LE", epd.ProbeLatchEnable)

		epd.PowerOffAll()
		println("end of round: all bits 0")
		time.Sleep(5 * time.Second)
	}
	epd.PowerOffAll()
	println("cfgprobe: done, all bits 0")
	for {
		time.Sleep(time.Second)
	}
}

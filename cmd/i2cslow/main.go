// Test: at 100 kHz, a PCF8563 read right after a read from an absent address
// timed out every time (cmd/i2cfinal section 6); with a println in between
// (section 2) it worked, and at 400 kHz it always worked.
//
// For machine.I2C0 (PR branch) at 100 and 400 kHz: 20 cycles of
// "read 0x42, pause, read PCF8563" for pauses 0, 20 us, 100 us, 1 ms. SR is
// read right before the PCF8563 read (BUS_BUSY, and SCL_MAIN_STATE_LAST).
// Then the same with pause 0 through i2cidf (ESP-IDF v4.4.8 port) at
// 100 kHz, counting i2c_hw_fsm_reset.
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/i2cidf"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

type txer interface {
	Tx(addr uint16, w, r []byte) error
}

func hex32(v uint32) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = hexd[v&15]
		v >>= 4
	}
	return string(b)
}

func errStr(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func waitUs(us int64) {
	if us == 0 {
		return
	}
	end := time.Now().Add(time.Duration(us) * time.Microsecond)
	for time.Now().Before(end) {
	}
}

var ref []byte

// run does n cycles and prints a summary plus the first failure.
func run(label string, bus txer, pauseUs int64, n int) {
	bad, busy := 0, 0
	var firstErr error
	var firstSR uint32
	for i := 0; i < n; i++ {
		_ = bus.Tx(0x42, []byte{0x81, 0x40}, make([]byte, 4))
		waitUs(pauseUs)
		sr := esp.I2C0.SR.Get()
		if sr&esp.I2C_SR_BUS_BUSY != 0 {
			busy++
		}
		b := make([]byte, 2)
		err := bus.Tx(0x51, []byte{0x00}, b)
		if err != nil || string(b) != string(ref) {
			if bad == 0 {
				firstErr, firstSR = err, sr
			}
			bad++
		}
	}
	println(label, "pause", pauseUs, "us | PCF8563 bad:", bad, "of", n, "| BUS_BUSY before read:", busy, "| first bad: err", errStr(firstErr), "SR before 0x"+hex32(firstSR))
}

func configure(freq uint32) {
	machine.I2C0.Configure(machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: freq})
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	println("=== i2cslow: PCF8563 right after an absent-address read, 100 vs 400 kHz, TinyGo:", tree, "===")
	for _, freq := range []uint32{100000, 400000} {
		configure(freq)
		ref = make([]byte, 2)
		err := machine.I2C0.Tx(0x51, []byte{0x00}, ref)
		println("--- machine.I2C0 at", freq/1000, "kHz | reference PCF8563:", errStr(err), hex32(uint32(ref[0])<<8|uint32(ref[1])))
		for _, p := range []int64{0, 20, 100, 1000} {
			configure(freq) // start every case from a clean controller
			run("  machine.I2C0", machine.I2C0, p, 20)
		}
	}

	println("--- i2cidf (ESP-IDF v4.4.8 port) at 100 kHz")
	i2cidf.ResetModule()
	idf := i2cidf.New(i2cidf.Config{SDA: machine.GPIO18, SCL: machine.GPIO17, SDAPullup: true, SCLPullup: true, ClkSpeed: 100000})
	idf.Timeout = 50 * time.Millisecond
	r0 := idf.FSMResets
	run("  i2cidf      ", idf, 0, 20)
	println("  i2cidf i2c_hw_fsm_reset during the 20 cycles:", idf.FSMResets-r0, "| errors counted by the driver:", idf.Errors)
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

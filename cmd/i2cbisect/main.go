// Test: which difference between the ESP-IDF v4.4.8 I2C driver (internal/i2cidf,
// works on this board) and machine.I2C of TinyGo 0.42.0 breaks the bus.
//
// Every variant starts from a reset I2C module (i2cidf.ResetModule) and runs
// the same sequence:
//
//	GT911 (first transaction after init), PCF8563, NACK on 0x42, PCF8563,
//	GT911, NACK on 0x42, GT911, GT911
//
// Round 1 (i2cbisect.bin): TinyGoInit → the GT911 NACKs the first transaction
// after init; Whole+MergeAddr → the controller stays stuck after an address
// NACK; timing and drive strength had no effect. Round 2 narrows both down:
//
//	V0 ESP-IDF as is
//	I1 InitPulse:   reset pulse on enable only
//	I2 InitFSMRst:  CTR.FSM_RST = 1 only
//	I3 InitClkRst:  9 SCL clocks (SCL_RST_SLV) only
//	I4 InitStretch: SLAVE_SCL_STRETCH_EN = 1 only
//	T1 Whole+MergeAddr+EndAfterAddr: END after the address+data WRITE
//	T2 Whole+EndAfterAddr:           END after the address WRITE only
//	T3 Whole+MergeAddr (round 1 V5, control)
//	V7 ESP-IDF as is again (control)
//
// Internal pull-ups are on in all variants.
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/i2cidf"
)

const (
	addrGT911   = 0x5D
	addrPCF8563 = 0x51
	addrAbsent  = 0x42
)

var (
	bus  *i2cidf.Bus
	reg0 = []byte{0x00}
	pid  = []byte{0x81, 0x40}
)

func hex32(v uint32) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		b[i] = hexd[v&15]
		v >>= 4
	}
	return string(b)
}

func hexBytes(buf []byte) string {
	const hexd = "0123456789abcdef"
	h := ""
	for _, b := range buf {
		h += string([]byte{hexd[b>>4], hexd[b&15]}) + " "
	}
	return h
}

type result struct {
	label string
	err   error
	data  []byte
	sr    uint32
	raw   uint32
}

func tx(label string, addr uint16, w []byte, n int) result {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 0xEE
	}
	err := bus.Tx(addr, w, buf)
	return result{label, err, buf, bus.LastSR, bus.LastRaw}
}

func variant(name string, cfg i2cidf.Config) {
	cfg.SDA, cfg.SCL = machine.GPIO18, machine.GPIO17
	cfg.SDAPullup, cfg.SCLPullup = true, true
	cfg.ClkSpeed = 400000

	i2cidf.ResetModule()
	bus = i2cidf.New(cfg)
	rs := []result{
		tx("GT911  ", addrGT911, pid, 4),
		tx("PCF8563", addrPCF8563, reg0, 2),
		tx("0x42   ", addrAbsent, pid, 4),
		tx("PCF8563", addrPCF8563, reg0, 2),
		tx("GT911  ", addrGT911, pid, 4),
		tx("0x42   ", addrAbsent, pid, 4),
		tx("GT911  ", addrGT911, pid, 4),
		tx("GT911  ", addrGT911, pid, 4),
	}
	println(name, "| fsm resets", bus.FSMResets)
	for _, r := range rs {
		s := "nil"
		info := ""
		if r.err != nil {
			s = r.err.Error()
			info = "| SR 0x" + hex32(r.sr) + " INT_RAW 0x" + hex32(r.raw)
		}
		println("  ", r.label, "| err:", s, "| data:", hexBytes(r.data), info)
	}
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	// Wake the GT911 as the badge does (INT high 20 + 50 ms, then input).
	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})

	println("=== i2cbisect round 2: ESP-IDF driver with one part switched to TinyGo ===")
	variant("V0 ESP-IDF      ", i2cidf.Config{})
	variant("I1 InitPulse    ", i2cidf.Config{InitPulse: true})
	variant("I2 InitFSMRst   ", i2cidf.Config{InitFSMRst: true})
	variant("I3 InitClkRst   ", i2cidf.Config{InitClkRst: true})
	variant("I4 InitStretch  ", i2cidf.Config{InitStretch: true})
	variant("T1 Merge+EndAddr", i2cidf.Config{Whole: true, MergeAddr: true, EndAfterAddr: true})
	variant("T2 Whole+EndAddr", i2cidf.Config{Whole: true, EndAfterAddr: true})
	variant("T3 Whole+Merge  ", i2cidf.Config{Whole: true, MergeAddr: true})
	variant("V7 ESP-IDF again", i2cidf.Config{})
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

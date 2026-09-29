// Test for the maintainer's question in PR #5774: does the first read right
// after a NACK on a data byte fail because the bus is still busy (BUS_BUSY)?
//
// Built with the PR branch (esp32xx-i2c-fix, with the FIFO reset commit). The
// PR code is not changed.
//
// One case:
//  1. machine.I2C0.Configure at the given speed, internal pull-ups on (for D:
//     i2cidf.ResetModule + i2cidf.New instead).
//  2. NACK on a data byte, made on purpose exactly as in cmd/i2cleftover
//     (mode B): [RSTART, WRITE 0x51+W, END], then [WRITE 3 x 0x84 with
//     ack_check_en and ack_exp = 1, STOP].
//  3. Right after the NACK flag, before anything else: SR, INT_RAW, SCL and
//     SDA levels.
//  4. Pause (A 0, B 20 us, C 1 ms; D 0), SR again, first PCF8563 read
//     (register 0x00, 2 bytes).
//  5. Second PCF8563 read.
//
// Variants A, B, C use machine.I2C0; D uses i2cidf (ESP-IDF v4.4.8
// port) and counts i2c_hw_fsm_reset during each read. 10 cases per variant,
// at 400 kHz and 100 kHz. Three builds (run1, run2, run3) differ only in the
// order of the variants and speeds.
//
// Run each build right after a full power cycle (USB and battery
// disconnected).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/i2cidf"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

const reps = 10

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

func hexBytes(buf []byte) string {
	const hexd = "0123456789abcdef"
	h := ""
	for _, b := range buf {
		h += string([]byte{hexd[b>>4], hexd[b&15]}) + " "
	}
	return h
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

type snap struct {
	nack     bool
	sr, raw  uint32
	scl, sda uint32
}

// provokeData is provokeData of cmd/i2cleftover, with the snapshot taken right
// after the NACK flag and before the flags are cleared.
func provokeData() snap {
	hw := esp.I2C0
	const write, ackEn, ackExp, end = 1 << 11, 1 << 8, 1 << 9, 4 << 11
	hw.INT_CLR.Set(0x3ffff)
	hw.DATA.Set(0x51 << 1)
	hw.COMD0.Set(6 << 11)           // RSTART
	hw.COMD1.Set(write | ackEn | 1) // WRITE address
	hw.COMD2.Set(end)               // END
	hw.SetCTR_CONF_UPGATE(1)
	hw.SetCTR_TRANS_START(1)
	start := time.Now()
	for hw.INT_RAW.Get()&esp.I2C_INT_RAW_END_DETECT_INT_RAW == 0 && time.Since(start) < 10*time.Millisecond {
	}
	hw.INT_CLR.Set(0x3ffff)
	for i := 0; i < 3; i++ {
		hw.DATA.Set(0x84)
	}
	hw.COMD0.Set(write | ackEn | ackExp | 3) // WRITE 3 data bytes, expect NACK
	hw.COMD1.Set(2 << 11)                    // STOP
	hw.SetCTR_CONF_UPGATE(1)
	hw.SetCTR_TRANS_START(1)
	var s snap
	start = time.Now()
	for time.Since(start) < 10*time.Millisecond {
		raw := hw.INT_RAW.Get()
		if raw&esp.I2C_INT_RAW_NACK_INT_RAW != 0 {
			s.nack = true
			break
		}
		if raw&esp.I2C_INT_RAW_TRANS_COMPLETE_INT_RAW != 0 {
			break
		}
	}
	s.sr = hw.SR.Get()
	s.raw = hw.INT_RAW.Get()
	in := esp.GPIO.IN.Get()
	s.scl, s.sda = (in>>17)&1, (in>>18)&1
	hw.INT_CLR.Set(0x3ffff)
	return s
}

func configure(freq uint32) {
	machine.I2C0.Configure(machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: freq})
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

var ref []byte

func readPCF(bus txer) ([]byte, error) {
	b := []byte{0xEE, 0xEE}
	return b, bus.Tx(0x51, []byte{0x00}, b)
}

type variant struct {
	name    string
	pauseUs int64
	idf     bool
}

func runVariant(v variant, freq uint32) {
	nacks, busyAfter, busyBefore, fail1, fail2, resets1, resets2 := 0, 0, 0, 0, 0, 0, 0
	errs1 := ""
	println("--- variant", v.name, "|", freq/1000, "kHz | pause", v.pauseUs, "us")
	for i := 0; i < reps; i++ {
		var bus txer
		var idf *i2cidf.Bus
		if v.idf {
			i2cidf.ResetModule()
			idf = i2cidf.New(i2cidf.Config{SDA: machine.GPIO18, SCL: machine.GPIO17, SDAPullup: true, SCLPullup: true, ClkSpeed: freq})
			idf.Timeout = 50 * time.Millisecond
			bus = idf
		} else {
			configure(freq)
			bus = machine.I2C0
		}
		s := provokeData()
		waitUs(v.pauseUs)
		srBefore := esp.I2C0.SR.Get()
		r0 := 0
		if idf != nil {
			r0 = idf.FSMResets
		}
		b1, e1 := readPCF(bus)
		r1 := 0
		if idf != nil {
			r1 = idf.FSMResets - r0
		}
		b2, e2 := readPCF(bus)
		r2 := 0
		if idf != nil {
			r2 = idf.FSMResets - r0 - r1
		}

		if s.nack {
			nacks++
		}
		if s.sr&esp.I2C_SR_BUS_BUSY != 0 {
			busyAfter++
		}
		if srBefore&esp.I2C_SR_BUS_BUSY != 0 {
			busyBefore++
		}
		ok1 := e1 == nil && string(b1) == string(ref)
		ok2 := e2 == nil && string(b2) == string(ref)
		if !ok1 {
			fail1++
			errs1 += errStr(e1) + "; "
		}
		if !ok2 {
			fail2++
		}
		resets1 += r1
		resets2 += r2
		println("  ", i+1, "| NACK", s.nack, "| after NACK: SR 0x"+hex32(s.sr), "BUS_BUSY", (s.sr>>4)&1, "INT_RAW 0x"+hex32(s.raw), "SCL", s.scl, "SDA", s.sda,
			"| before read 1: SR 0x"+hex32(srBefore), "BUS_BUSY", (srBefore>>4)&1,
			"| read 1:", errStr(e1), hexBytes(b1), "| read 2:", errStr(e2), hexBytes(b2), "| fsm resets r1/r2:", r1, r2)
	}
	println("SUMMARY", v.name, freq/1000, "kHz | forced NACK", nacks, "/", reps,
		"| BUS_BUSY right after NACK", busyAfter, "| BUS_BUSY before read 1", busyBefore,
		"| read 1 failed", fail1, "| read 2 failed", fail2, "| fsm resets during read 1/2:", resets1, resets2, "| read 1 errors:", errs1)
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	println("=== i2cdatanack:", runName, "| PR branch esp32xx-i2c-fix ===")
	all := map[byte]variant{
		'A': {"A machine.I2C0", 0, false},
		'B': {"B machine.I2C0", 20, false},
		'C': {"C machine.I2C0", 1000, false},
		'D': {"D i2cidf      ", 0, true},
	}
	for _, freq := range speeds {
		configure(freq)
		ref = []byte{0xEE, 0xEE}
		err := machine.I2C0.Tx(0x51, []byte{0x00}, ref)
		println("=== speed", freq/1000, "kHz | reference PCF8563 read:", errStr(err), hexBytes(ref))
		for i := 0; i < len(order); i++ {
			runVariant(all[order[i]], freq)
		}
	}
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

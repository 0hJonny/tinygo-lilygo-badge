// Reproduction of the TinyGo I2C problem on ESP32-S3 without the display, for an
// issue in tinygo-org/tinygo. Only the board's I2C bus is needed: the GT911 at
// 0x5D (present) and the free address 0x42 (nothing there).
//
// Run it right after a full power cycle (USB and battery disconnected), so the
// GT911 starts in a known state: in earlier runs it stopped acknowledging after
// the TinyGo tests, and an ESP32 reset alone does not reset it.
//
// Two builds, each to be run right after a full power cycle:
//
//	default     tests 6 → 7
//	-tags test8 tests 6 → 8a → 8b
//	-tags test9 tests 6 → 9 → 7
//
// because test 7 was observed to make the GT911 stop responding, which would
// make test 8 meaningless if run after it.
//
// Order: first the tests that need a responsive GT911 (only i2cfix, which
// reports NACKs), then the TinyGo tests, then i2cfix again. After each phase
// the GT911 is woken with a pulse on INT (W) and checked with a control read (C),
// to see whether a GT911 that stopped responding comes back in software.
//
// For every i2cfix error a snapshot is printed (D): which of the transaction's
// commands were executed (COMDi done bits), INT_RAW, and the bytes that reached
// the RX FIFO. For a read the commands are: 0 START, 1 WRITE address+W and
// register, 2 RESTART, 3 WRITE address+R, 4 READ n−1, 5 READ 1 (NACK), 6 STOP.
//
// Tests:
//  0. Idle SDA/SCL levels without pull-ups and with the internal pull-ups.
//  6. i2cfix with the ACK-check bit on READ (as in TinyGo): one read of the
//     GT911. If D shows that all READ commands were done and "911" is in the RX
//     FIFO, the NACK is raised by the ACK-check bit at the end of an otherwise
//     successful read — the hypothesis of why simply removing `!readLast` for
//     esp32xx was reverted (PR #5602).
//  7. i2cfix only: GT911 → empty address 0x42 → GT911 (NACKs no longer reset
//     the bus, as in IDF).
//  9. i2cfix only: write-only probes of the empty address 0x42 (START,
//     address+W, STOP), the way i2c_scanner() in the ESP-IDF badge project probes
//     addresses, then GT911 reads. Is it the empty address itself, or the shape
//     of the read transaction (write + RESTART + read), that stops the GT911?
//
// When a control read gets a NACK at 0x5D, the GT911 is also probed at its other
// address 0x14 (P lines): did it restart and latch the other address?
//  8. On a responding GT911: 8a re-initializes the I2C peripheral only
//     (machine.I2C.Configure, as TinyGo does between tests 3 and 4), 8b does a
//     full i2cfix recovery (software bus clear + re-init). A control read after
//     each shows which step, if any, makes the GT911 stop responding.
//  1. machine.I2C0.Tx: read the GT911.
//  2. machine.I2C0.Tx: read from the empty address 0x42, an error is expected.
//  3. machine.I2C0.Tx: write to the empty address 0x42, an error is expected.
//  4. machine.I2C0.Tx: read the GT911 right after test 2, is it corrupted.
//  5. i2cfix after the TinyGo tests: the empty address and the GT911.
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/i2cfix"
)

const (
	addrGT911    = 0x5D
	addrGT911Alt = 0x14 // GT911 address when INT is high at reset
	addrAbsent   = 0x42
	reps         = 3
)

var regProductID = []byte{0x81, 0x40}

func show(label string, err error, buf []byte) {
	s := "nil"
	if err != nil {
		s = err.Error()
	}
	h := ""
	for _, b := range buf {
		const hexd = "0123456789abcdef"
		h += string([]byte{hexd[b>>4], hexd[b&15]}) + " "
	}
	println(label, "| err:", s, "| data:", h)
}

func fill(buf []byte) {
	for i := range buf {
		buf[i] = 0xEE // marker: if nothing is written to the buffer, EE remains
	}
}

// pullUps enables the internal SCL/SDA pull-ups, like i2c_config_t
// (scl/sda_pullup_en) in the original; machine.I2C.Configure does not enable them.
func pullUps() {
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

func levels(label string, mode machine.PinMode) {
	machine.GPIO17.Configure(machine.PinConfig{Mode: mode})
	machine.GPIO18.Configure(machine.PinConfig{Mode: mode})
	time.Sleep(5 * time.Millisecond)
	println("0", label, "| SCL(IO17):", machine.GPIO17.Get(), "SDA(IO18):", machine.GPIO18.Get())
}

func hex2(b byte) string {
	const hexd = "0123456789abcdef"
	return string([]byte{hexd[b>>4], hexd[b&15]})
}

// showDiag prints the snapshot of the last failed i2cfix transaction.
func showDiag(bus *i2cfix.Bus) {
	d := bus.Last
	done := ""
	for i := 0; i < d.Cmds; i++ {
		if d.Done&(1<<i) != 0 {
			done += "1"
		} else {
			done += "0"
		}
	}
	rx := ""
	for i := 0; i < d.RxCount && i < len(d.Rx); i++ {
		rx += hex2(d.Rx[i]) + " "
	}
	raw := ""
	for sh := 28; sh >= 0; sh -= 4 {
		raw += string("0123456789abcdef"[(d.IntRaw>>uint(sh))&15])
	}
	println("D   cmds done (0..n-1):", done, "| INT_RAW: 0x"+raw, "| RX FIFO:", d.RxCount, "bytes:", rx,
		"| BUS_BUSY now:", d.SR&0x10 != 0, "at start:", d.BusyAtStart)
}

var intPin = machine.GPIO47

// wake is gt911.Wake: INT high for 20 + 50 ms, then an input without pulls.
func wake() {
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	// The display bus is not needed.
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	wake()

	println("=== Idle bus levels (true = high, expected true) ===")
	levels("no pull-ups            ", machine.PinInput)
	levels("internal pull-ups      ", machine.PinInputPullup)

	cfg := machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	buf := make([]byte, 4)

	bus := i2cfix.New(machine.I2C0, cfg, true)
	read := func(label string, addr uint16) {
		fill(buf)
		err := bus.Tx(addr, regProductID, buf)
		show(label, err, buf)
		if err != nil {
			showDiag(bus)
		}
	}
	control := func(label string) {
		fill(buf)
		err := bus.Tx(addrGT911, regProductID, buf)
		show("C read  GT911 0x5D "+label, err, buf)
		if err != nil {
			showDiag(bus)
			read("P read  GT911 0x14 (alt addr)", addrGT911Alt)
		}
	}
	wakeAndCheck := func(label string) {
		wake()
		println("W INT wake pulse")
		control(label)
	}

	// Start check: tests 6–8 only make sense on a responding GT911.
	alive := false
	for try := 0; try < 4 && !alive; try++ {
		if try > 0 {
			wake()
			println("W INT wake pulse (start check)")
		}
		fill(buf)
		err := bus.Tx(addrGT911, regProductID, buf)
		show("C read  GT911 0x5D (start)   ", err, buf)
		if err != nil {
			showDiag(bus)
		} else {
			alive = true
		}
	}
	if !alive {
		println("!!! GT911 is not responding at start: tests 6, 7, 8 are skipped (their results")
		println("!!! would be meaningless). Power the board off completely (USB and battery), then run again.")
	} else {
		test6(read, control, wakeAndCheck)
		switch mode {
		case "8":
			test8(bus, control, wakeAndCheck)
		case "9":
			test9(bus, read, control)
			test7(read, control)
		default:
			test7(read, control)
		}
	}
	println("i2cfix: errors", bus.Errors, "recoveries", bus.Recoveries, "transactions started with BUS_BUSY:", bus.BusyStarts)

	tinygoTests(cfg, buf)

	println("=== i2cfix after the TinyGo tests (test 5) ===")
	bus = i2cfix.New(machine.I2C0, cfg, true)
	control("(after TinyGo)")
	for i := 0; i < reps; i++ {
		read("5 read  0x42 (nothing there) ", addrAbsent)
	}
	for i := 0; i < reps; i++ {
		read("5 read  GT911 0x5D (present) ", addrGT911)
	}
	wakeAndCheck("(after W)   ")
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

// test6 needs a responding GT911.
func test6(read func(string, uint16), control, wakeAndCheck func(string)) {
	println("=== i2cfix, GT911 fresh: test 6 (ACK check on READ, as in TinyGo) ===")
	control("(before 6)  ")
	i2cfix.ReadAckCheck = true
	read("6 read  GT911 0x5D (present) ", addrGT911)
	i2cfix.ReadAckCheck = false
	control("(after 6)   ")
	wakeAndCheck("(after W)   ")
}

// test7: GT911 → empty 0x42 → GT911, only i2cfix.
func test7(read func(string, uint16), control func(string)) {
	println("=== i2cfix only, test 7: GT911 -> empty 0x42 -> GT911 ===")
	read("7 read  GT911 0x5D (before)  ", addrGT911)
	for i := 0; i < reps; i++ {
		read("7 read  0x42 (nothing there) ", addrAbsent)
	}
	for i := 0; i < reps; i++ {
		read("7 read  GT911 0x5D (after)   ", addrGT911)
	}
	control("(after 7)   ")
}

// test9: write-only probes of the empty address (like i2c_scanner in the ESP-IDF
// badge project), then GT911 reads.
func test9(bus *i2cfix.Bus, read func(string, uint16), control func(string)) {
	println("=== i2cfix only, test 9: write-only probe of empty 0x42 (START, addr+W, STOP) ===")
	for i := 0; i < reps; i++ {
		err := bus.Tx(addrAbsent, nil, nil)
		show("9 probe 0x42 (nothing there) ", err, nil)
		if err != nil {
			showDiag(bus)
		}
	}
	for i := 0; i < reps; i++ {
		read("9 read  GT911 0x5D (after)   ", addrGT911)
	}
	control("(after 9)   ")
}

// test8: peripheral re-init only (8a), then full recovery (8b), on a responding GT911.
func test8(bus *i2cfix.Bus, control, wakeAndCheck func(string)) {
	println("=== test 8: what makes a responding GT911 stop answering ===")
	bus.Reconfigure()
	println("8a peripheral re-init only (Configure)")
	control("(after 8a)  ")
	bus.Recover()
	println("8b full recovery (bus clear + re-init)")
	control("(after 8b)  ")
	wakeAndCheck("(after W)   ")
}

// tinygoTests runs tests 1–4 through machine.I2C0.Tx.
func tinygoTests(cfg machine.I2CConfig, buf []byte) {
	println("=== TinyGo machine.I2C0.Tx (ESP32-S3), internal pull-ups enabled ===")
	machine.I2C0.Configure(cfg)
	pullUps()
	for i := 0; i < reps; i++ {
		fill(buf)
		err := machine.I2C0.Tx(addrGT911, regProductID, buf)
		show("1 read  GT911 0x5D (present) ", err, buf)
	}
	for i := 0; i < reps; i++ {
		fill(buf)
		err := machine.I2C0.Tx(addrAbsent, regProductID, buf)
		show("2 read  0x42 (nothing there) ", err, buf)
	}
	for i := 0; i < reps; i++ {
		err := machine.I2C0.Tx(addrAbsent, regProductID, nil)
		show("3 write 0x42 (nothing there) ", err, nil)
	}
	machine.I2C0.Configure(cfg)
	pullUps()
	fill(buf)
	_ = machine.I2C0.Tx(addrAbsent, regProductID, buf) // a failed read…
	for i := 0; i < reps; i++ {
		fill(buf)
		err := machine.I2C0.Tx(addrGT911, regProductID, buf) // …and right away a valid one
		show("4 read  GT911 after test 2   ", err, buf)
	}
}

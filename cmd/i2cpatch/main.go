// Test for a locally patched TinyGo (machine_esp32xx_i2c.go: READ commands
// without the ACK-check bit, `&& !readLast` removed). Uses only machine.I2C0.
//
// Devices on the bus: GT911 at 0x5D, PCF8563 RTC at 0x51, nothing at 0x42.
// Run it right after a full power cycle (USB and battery disconnected).
//
// Step 2 of the patch (FIFO reset before each transaction) did not help: after
// the first NACK every following transaction failed. This version records
// register snapshots (D) to see where the following transactions stop.
//
// A later run showed that the failing state can also appear right after the
// first Configure, before any NACK, for both devices, and that a second
// Configure cleared it. This version compares, in the same controller state, a
// machine.I2C0 transaction and an i2cfix transaction (i2cfix.Wrap: no
// Configure), and prints SR.TXFIFO_CNT before every machine.I2C0 transaction.
//
// Tests (each machine.I2C0 transaction is followed by an i2cfix transaction to
// the same device):
//
//	A  GT911 Product ID.
//	R1 PCF8563 registers 0x00–0x01.
//	E1 One read from the empty address 0x42.
//	R2 PCF8563 again.
//	R3 machine.I2C0.Configure again, then PCF8563.
package main

import (
	"machine"
	"time"

	"device/esp"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/i2cfix"
)

const (
	addrGT911   = 0x5D
	addrPCF8563 = 0x51
	addrAbsent  = 0x42
	reps        = 3
)

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

// snap prints the I2C0 state after a transaction: done bits of COMD0–7 and
// the commands (op code << 11 | flags | byte count), SR, INT_RAW.
func snap() {
	hw := esp.I2C0
	regs := [8]uint32{hw.COMD0.Get(), hw.COMD1.Get(), hw.COMD2.Get(), hw.COMD3.Get(), hw.COMD4.Get(), hw.COMD5.Get(), hw.COMD6.Get(), hw.COMD7.Get()}
	done := ""
	cmds := ""
	for _, c := range regs {
		if c&(1<<31) != 0 {
			done += "1"
		} else {
			done += "0"
		}
		cmds += hex32(c&0x7fffffff) + " "
	}
	println("D  done:", done, "| SR: 0x"+hex32(hw.SR.Get()), "| INT_RAW: 0x"+hex32(hw.INT_RAW.Get()), "| COMD0-7:", cmds)
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

func tx(label string, addr uint16, w []byte, n int) {
	var buf []byte
	if n > 0 {
		buf = make([]byte, n)
		for i := range buf {
			buf[i] = 0xEE // marker: EE remains if nothing was written
		}
	}
	println(label, "| before: TXFIFO_CNT", (esp.I2C0.SR.Get()>>18)&0x3f, "BUS_BUSY", (esp.I2C0.SR.Get()>>4)&1)
	err := machine.I2C0.Tx(addr, w, buf)
	show(label, err, buf)
}

var fix *i2cfix.Bus

// fx does the same transaction through i2cfix (same controller, no re-init).
func fx(label string, addr uint16, w []byte, n int) {
	buf := make([]byte, n)
	for i := range buf {
		buf[i] = 0xEE
	}
	err := fix.Tx(addr, w, buf)
	show(label, err, buf)
	if err != nil {
		d := fix.Last
		done := ""
		for i := 0; i < d.Cmds; i++ {
			if d.Done&(1<<i) != 0 {
				done += "1"
			} else {
				done += "0"
			}
		}
		println("   i2cfix D done:", done, "| SR: 0x"+hex32(d.SR), "| INT_RAW: 0x"+hex32(d.IntRaw))
	}
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	// Wake the GT911 (like gt911.Wake).
	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})

	machine.I2C0.Configure(machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz})
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)

	println("=== patched TinyGo (steps 1+4 (TinyGo fork)): machine.I2C0 vs i2cfix on the same controller ===")
	cfg := machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	fix = i2cfix.Wrap(machine.I2C0, cfg, true)
	pid := []byte{0x81, 0x40}
	reg0 := []byte{0x00}

	tx("A  TinyGo read GT911 PID (4)  ", addrGT911, pid, 4)
	snap()
	fx("A  i2cfix read GT911 PID (4)  ", addrGT911, pid, 4)
	tx("R1 TinyGo read PCF8563 (2)    ", addrPCF8563, reg0, 2)
	snap()
	fx("R1 i2cfix read PCF8563 (2)    ", addrPCF8563, reg0, 2)
	tx("E1 TinyGo read 0x42 (empty)   ", addrAbsent, pid, 4)
	snap()
	tx("R2 TinyGo read PCF8563 (2)    ", addrPCF8563, reg0, 2)
	snap()
	fx("R2 i2cfix read PCF8563 (2)    ", addrPCF8563, reg0, 2)
	tx("R2 TinyGo read PCF8563 (2)    ", addrPCF8563, reg0, 2)
	snap()
	machine.I2C0.Configure(cfg)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	println("R3 machine.I2C0.Configure again")
	tx("R3 TinyGo read PCF8563 (2)    ", addrPCF8563, reg0, 2)
	snap()
	fx("R3 i2cfix read PCF8563 (2)    ", addrPCF8563, reg0, 2)
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

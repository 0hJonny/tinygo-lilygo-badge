// Test (review of the esp32xx I2C PR, point 2): does the GT911 NACK after the
// 9 SCL clocks of the old resetMaster() come from the clocks themselves, or
// from the missing STOP after them?
//
// Built with the PR branch, where Configure gives no clocks. The program adds
// them itself right after Configure, exactly as the old resetMaster() did
// (SCL_RST_SLV_NUM = 9, SCL_RST_SLV_EN = 1, wait until it clears). Variants:
//
//	A   Configure                                  (PR, control)
//	B   Configure, 9 clocks                        (old dev behaviour)
//	C   Configure, 9 clocks, STOP by GPIO, Configure again (no clocks)
//	C2  Configure, 9 clocks, STOP command of the controller
//	B2  Configure, 9 clocks, Configure again       (control for C: the second
//	    Configure without the STOP)
//
// Each variant: the setup, then the GT911 Product ID twice. The rounds run the
// variants in turn, so every variant follows a successful GT911 read.
//
// Run it right after a full power cycle (USB and battery disconnected).
package main

import (
	"device/esp"
	"machine"
	"time"

	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd"
)

var (
	cfg = machine.I2CConfig{SCL: machine.GPIO17, SDA: machine.GPIO18, Frequency: 400 * machine.KHz}
	i2c = machine.I2C0
	pid = []byte{0x81, 0x40}
)

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

func configure() {
	i2c.Configure(cfg)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
}

// lines returns the SCL (GPIO17) and SDA (GPIO18) levels.
func lines() (uint32, uint32) {
	in := esp.GPIO.IN.Get()
	return (in >> 17) & 1, (in >> 18) & 1
}

// clocks is the bus clear of the old resetMaster(): 9 SCL clocks (SCL_RST_SLV).
func clocks() {
	hw := esp.I2C0
	hw.SetSCL_SP_CONF_SCL_RST_SLV_NUM(9)
	hw.SetSCL_SP_CONF_SCL_RST_SLV_EN(1)
	hw.SetCTR_CONF_UPGATE(1)
	for hw.GetSCL_SP_CONF_SCL_RST_SLV_EN() != 0 {
	}
	hw.SetSCL_SP_CONF_SCL_RST_SLV_NUM(0)
}

func waitUs(us int64) {
	end := time.Now().Add(time.Duration(us) * time.Microsecond)
	for time.Now().Before(end) {
	}
}

// gpioStop generates a STOP with the pins as open-drain GPIOs (the end of
// i2c_master_clear_bus in ESP-IDF): SCL low, SDA low, SCL high, SDA high.
func gpioStop() {
	for _, p := range []machine.Pin{machine.GPIO17, machine.GPIO18} {
		p.Configure(machine.PinConfig{Mode: machine.PinOutput})
		p.High()
	}
	esp.GPIO.PIN17.SetBits(esp.GPIO_PIN_PAD_DRIVER)
	esp.GPIO.PIN18.SetBits(esp.GPIO_PIN_PAD_DRIVER)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	scl, sda := machine.GPIO17, machine.GPIO18
	scl.Low()
	waitUs(5)
	sda.Low()
	waitUs(5)
	scl.High()
	waitUs(5)
	sda.High()
	waitUs(5)
}

// gpioNoStop switches the pins to open-drain GPIOs held high and back,
// without any edge on the bus (control for gpioStop).
func gpioNoStop() {
	for _, p := range []machine.Pin{machine.GPIO17, machine.GPIO18} {
		p.Configure(machine.PinConfig{Mode: machine.PinOutput})
		p.High()
	}
	esp.GPIO.PIN17.SetBits(esp.GPIO_PIN_PAD_DRIVER)
	esp.GPIO.PIN18.SetBits(esp.GPIO_PIN_PAD_DRIVER)
	esp.IO_MUX.GPIO17.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	esp.IO_MUX.GPIO18.SetBits(esp.IO_MUX_GPIO_FUN_WPU)
	waitUs(20)
}

// ctrlStop runs a single STOP command on the controller.
func ctrlStop() (raw uint32) {
	hw := esp.I2C0
	hw.INT_CLR.Set(0x3ffff)
	hw.COMD0.Set(2 << 11) // STOP
	hw.SetCTR_CONF_UPGATE(1)
	hw.SetCTR_TRANS_START(1)
	start := time.Now()
	for time.Since(start) < 5*time.Millisecond {
		raw = hw.INT_RAW.Get()
		if raw&(esp.I2C_INT_RAW_TRANS_COMPLETE_INT_RAW|esp.I2C_INT_RAW_TIME_OUT_INT_RAW|esp.I2C_INT_RAW_END_DETECT_INT_RAW) != 0 {
			break
		}
	}
	hw.INT_CLR.Set(0x3ffff)
	return raw
}

func readPID() ([]byte, error) {
	b := []byte{0xEE, 0xEE, 0xEE, 0xEE}
	return b, i2c.Tx(0x5D, pid, b)
}

type variant struct {
	name  string
	setup func() string
}

func main() {
	// Project safety rule: clear the 74HCT4094 first (PWR_EN = 0).
	epd.UseDMA = false
	_ = epd.Init()

	time.Sleep(3 * time.Second)

	// Wake the GT911 (INT high 20 + 50 ms, then input).
	intPin := machine.GPIO47
	intPin.Configure(machine.PinConfig{Mode: machine.PinOutput})
	intPin.High()
	time.Sleep(70 * time.Millisecond)
	intPin.Configure(machine.PinConfig{Mode: machine.PinInput})
	time.Sleep(100 * time.Millisecond)

	println("=== i2cclocks 2: GT911 after 9 SCL clocks, with and without STOP (control before each, rotating order) ===")
	lvl := func() string {
		scl, sda := lines()
		return "SCL " + string(rune('0'+scl)) + " SDA " + string(rune('0'+sda))
	}
	variants := []variant{
		{"A  Configure                     ", func() string { configure(); return lvl() }},
		{"B  Configure, 9 clocks           ", func() string { configure(); clocks(); return lvl() }},
		{"C  Configure, 9 clocks, GPIO STOP, Configure", func() string {
			configure()
			clocks()
			l := lvl()
			gpioStop()
			configure()
			return "after clocks " + l + ", after STOP+Configure " + lvl()
		}},
		{"C2 Configure, 9 clocks, controller STOP", func() string {
			configure()
			clocks()
			l := lvl()
			raw := ctrlStop()
			return "after clocks " + l + ", STOP INT_RAW 0x" + hexBytes([]byte{byte(raw >> 24), byte(raw >> 16), byte(raw >> 8), byte(raw)}) + ", after " + lvl()
		}},
		{"B2 Configure, 9 clocks, Configure", func() string { configure(); clocks(); configure(); return lvl() }},
		{"C3 Configure, 9 clocks, GPIO no STOP, Configure", func() string {
			configure()
			clocks()
			gpioNoStop()
			configure()
			return lvl()
		}},
	}
	control := variants[0]

	// Every variant runs right after the control (A) read, and the order of
	// the variants B..C3 rotates from round to round.
	others := variants[1:]
	for round := 1; round <= 4; round++ {
		println("--- round", round)
		for k := range others {
			v := others[(k+round-1)%len(others)]
			control.setup()
			cb, ce := readPID()
			println("   (control A before it | err:", errStr(ce), "| data:", hexBytes(cb), ")")
			info := v.setup()
			b1, e1 := readPID()
			b2, e2 := readPID()
			println(v.name, "|", info)
			println("     1st PID | err:", errStr(e1), "| data:", hexBytes(b1), "|| 2nd PID | err:", errStr(e2), "| data:", hexBytes(b2))
		}
	}
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

// Diagnostic for the tinyhci xiao-esp32c3 I2C test (MPU6050 at 0x68 on the
// default I2C0 pins), for tinygo-org/tinygo PR #5774. It runs the same
// transactions as the test (mpu6050.Configure, then Connected), but prints the
// error and data of each one, the SCL/SDA levels before Configure, and an
// address scan with 1-byte reads. Only machine.I2C0 is used.
package main

import (
	"machine"
	"time"
)

const (
	addrMPU6050 = 0x68
	regPwrMgmt1 = 0x6B
	regWhoAmI   = 0x75
)

func hex8(b byte) string {
	const hexd = "0123456789abcdef"
	return string([]byte{hexd[b>>4], hexd[b&15]})
}

func errStr(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func whoAmI(label string) {
	data := []byte{0}
	err := machine.I2C0.Tx(addrMPU6050, []byte{regWhoAmI}, data)
	println(label, "| err:", errStr(err), "| data: 0x"+hex8(data[0]), "(0x68 expected)")
}

func main() {
	time.Sleep(3 * time.Second)
	println("=== hcidiag: MPU6050 on I2C0 ===")

	// Line levels before Configure, pins as plain inputs.
	machine.SCL_PIN.Configure(machine.PinConfig{Mode: machine.PinInput})
	machine.SDA_PIN.Configure(machine.PinConfig{Mode: machine.PinInput})
	println("before Configure | SCL", machine.SCL_PIN.Get(), "| SDA", machine.SDA_PIN.Get())

	machine.I2C0.Configure(machine.I2CConfig{})

	// mpu6050.Configure: write PWR_MGMT_1 = 0 (legacy.WriteRegister).
	err := machine.I2C0.Tx(addrMPU6050, []byte{regPwrMgmt1, 0x00}, nil)
	println("1 write PWR_MGMT_1 = 0 | err:", errStr(err))

	time.Sleep(500 * time.Millisecond)

	// mpu6050.Connected: read WHO_AM_I (legacy.ReadRegister).
	whoAmI("2 read WHO_AM_I")
	whoAmI("3 read WHO_AM_I again")
	whoAmI("4 read WHO_AM_I again")

	// Probe every 7-bit address with a 1-byte read (Tx with no w and no r
	// sends no address byte at all).
	found := ""
	probe := []byte{0}
	for a := uint16(0x08); a < 0x78; a++ {
		if machine.I2C0.Tx(a, nil, probe) == nil {
			found += "0x" + hex8(byte(a)) + " "
		}
	}
	println("5 scan, ACK from:", found)

	whoAmI("6 read WHO_AM_I after scan")

	// Once more after a second Configure.
	machine.I2C0.Configure(machine.I2CConfig{})
	err = machine.I2C0.Tx(addrMPU6050, []byte{regPwrMgmt1, 0x00}, nil)
	println("7 Configure again, write PWR_MGMT_1 = 0 | err:", errStr(err))
	whoAmI("8 read WHO_AM_I")
	println("=== done ===")
	for {
		time.Sleep(time.Second)
	}
}

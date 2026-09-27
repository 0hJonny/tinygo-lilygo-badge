// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.
// The LCD_CAM/GDMA register sequence follows ESP-IDF esp_lcd and gdma
// (SPDX-FileCopyrightText: 2021-2023 Espressif Systems (Shanghai) CO LTD,
// Apache-2.0; see LICENSES/Apache-2.0.txt).

package epd

// Data bus through the LCD_CAM peripheral (Intel 8080 mode) + GDMA.
//
// This is a register-level port of what the original does through esp_lcd:
// i2s_bus_init() (i2s_data_bus.c) calls esp_lcd_new_i80_bus() and
// esp_lcd_new_panel_io_i80(), and i2s_start_line_output() calls
// esp_lcd_panel_io_tx_color(io, 0, buffer, (960+32)/4). Reference: ESP-IDF
// v4.4.8, components/esp_lcd/src/esp_lcd_panel_io_i80.c,
// hal/esp32s3/include/hal/lcd_ll.h, gdma_ll.h, driver/gdma.c. The comments on
// each step name the corresponding IDF function.
//
// Difference from IDF: no interrupts. Transaction completion is detected by
// polling LC_DMA_INT_RAW.LCD_TRANS_DONE, and every wait has a timeout.

import (
	"device/esp"
	"errors"
	"github.com/0hJonny/tinygo-lilygo-badge/internal/epd/raster"
	"machine"
	"runtime/volatile"
	"unsafe"
)

// GPIO matrix signal numbers (soc/esp32s3/include/soc/gpio_sig_map.h) and the
// GDMA trigger (soc/gdma_channel.h).
const (
	sigLCDDataOut0 = 133 // LCD_DATA_OUT0_IDX … LCD_DATA_OUT7_IDX = 133…140
	sigLCDDC       = 153 // LCD_DC_IDX
	sigLCDPCLK     = 154 // LCD_PCLK_IDX
	gdmaTrigLCD    = 5   // SOC_GDMA_TRIG_PERIPH_LCD0
)

// Clocking: 160 MHz PLL source (lcd_clk_sel = 3), group divider 2
// (LCD_PERIPH_CLOCK_PRE_SCALE) → 80 MHz; pclk_hz = 10 MHz → prescale 8.
const (
	lcdClkSelPLL160M = 3
	lcdGroupDiv      = 2
	lcdPclkPrescale  = 80_000_000 / 10_000_000
)

// Register fields that device/esp does not provide as _Pos constants.
const (
	lcdClockClkcntNPos   = 0
	lcdClockDivNumPos    = 9
	lcdClockDivBPos      = 17
	lcdClockDivAPos      = 23
	lcdClockClkSelPos    = 29
	lcdUserDoutCycPos    = 0
	lcdUserDummyCycPos   = 29
	lcdMiscVfkCycPos     = 6
	lcdMiscVbkCycPos     = 12
	dmaOutConf0Rst       = 1 << 0
	dmaOutConf0AutoWrb   = 1 << 2
	dmaOutConf0DscrBurst = 1 << 4
	dmaOutConf0DataBurst = 1 << 5
	dmaOutConf1CheckOwn  = 1 << 12
	dmaOutConf1BkSizeMsk = 0x3 << 13
	dmaOutLinkAddrMsk    = 0xfffff
	dmaOutLinkStart      = 1 << 21
	dmaMiscConfClkEn     = 1 << 4
)

// dmaDescriptor is dma_descriptor_t (hal/dma_types.h), 12 bytes:
// dw0: size[11:0], length[23:12], err_eof[28], suc_eof[30], owner[31].
type dmaDescriptor struct {
	dw0    uint32
	buffer uint32
	next   uint32
}

const (
	dmaDw0SucEOF   = 1 << 30
	dmaDw0OwnerDMA = 1 << 31
)

var (
	lcdDesc dmaDescriptor
	lcdBusy bool // a transaction was started and not yet confirmed

	errLCDTimeout = errors.New("epd: LCD_CAM/GDMA timeout")
)

func setField(r *volatile.Register32, mask, pos, val uint32) {
	r.Set(r.Get()&^mask | (val<<pos)&mask)
}

func setBit(r *volatile.Register32, bit uint32, on bool) {
	if on {
		r.SetBits(bit)
	} else {
		r.ClearBits(bit)
	}
}

// lcdInit is esp_lcd_new_i80_bus() + the first part of esp_lcd_new_panel_io_i80()
// for the configuration from i2s_bus_init(): 8 bits, WR = CKH, DC = STH, 10 MHz,
// lcd_cmd_bits = 10, dc_levels {idle 0, cmd 1, dummy 0, data 0}.
func lcdInit() error {
	lcd := esp.LCD_CAM

	// periph_module_enable(PERIPH_LCD_CAM_MODULE): enable the clock, release reset.
	esp.SYSTEM.PERIP_CLK_EN1.SetBits(esp.SYSTEM_PERIP_CLK_EN1_LCD_CAM_CLK_EN)
	esp.SYSTEM.PERIP_RST_EN1.ClearBits(esp.SYSTEM_PERIP_RST_EN1_LCD_CAM_RST)

	// lcd_ll_reset, lcd_ll_fifo_reset (self-clearing bits), lcd_ll_enable_clock.
	lcd.LCD_USER.SetBits(esp.LCD_CAM_LCD_USER_LCD_RESET)
	lcd.LCD_MISC.SetBits(esp.LCD_CAM_LCD_MISC_LCD_AFIFO_RESET)
	lcd.LCD_CLOCK.SetBits(esp.LCD_CAM_LCD_CLOCK_CLK_EN)

	// lcd_i80_select_periph_clock: lcd_ll_select_clk_src(PLL160M),
	// lcd_ll_set_group_clock_coeff(2, 0, 0).
	setField(&lcd.LCD_CLOCK, esp.LCD_CAM_LCD_CLOCK_LCD_CLK_SEL_Msk, lcdClockClkSelPos, lcdClkSelPLL160M)
	setField(&lcd.LCD_CLOCK, esp.LCD_CAM_LCD_CLOCK_LCD_CLKM_DIV_NUM_Msk, lcdClockDivNumPos, lcdGroupDiv)
	setField(&lcd.LCD_CLOCK, esp.LCD_CAM_LCD_CLOCK_LCD_CLKM_DIV_A_Msk, lcdClockDivAPos, 0)
	setField(&lcd.LCD_CLOCK, esp.LCD_CAM_LCD_CLOCK_LCD_CLKM_DIV_B_Msk, lcdClockDivBPos, 0)

	// Interrupts are not used: disable and clear them, as IDF does before installing the ISR.
	lcd.LC_DMA_INT_ENA.ClearBits(0x3)
	lcd.LC_DMA_INT_CLR.Set(0x3)

	gdmaInit()

	// lcd_ll_enable_rgb_mode(false), lcd_ll_set_data_width(8),
	// lcd_ll_enable_output_always_on(true).
	lcd.LCD_CTRL.ClearBits(esp.LCD_CAM_LCD_CTRL_LCD_RGB_MODE_EN)
	lcd.LCD_USER.ClearBits(esp.LCD_CAM_LCD_USER_LCD_2BYTE_EN)
	lcd.LCD_USER.SetBits(esp.LCD_CAM_LCD_USER_LCD_ALWAYS_OUT_EN)

	// lcd_periph_trigger_quick_trans_done_event: an empty transaction (1 dummy
	// cycle). It also checks that the peripheral works at all.
	lcdSetPhaseCycles(0, 1, 0)
	lcdStart()
	if err := lcdWaitTransDone(); err != nil {
		return err
	}

	// lcd_i80_bus_configure_gpio: data, DC and WR through the GPIO matrix.
	for i, p := range busPins {
		connectOutSignal(p, sigLCDDataOut0+uint32(i))
	}
	connectOutSignal(pinSTH, sigLCDDC)
	connectOutSignal(pinCKH, sigLCDPCLK)

	// lcd_i80_switch_devices on the first transaction: PCLK divider, levels.
	// lcd_ll_set_pixel_clock_prescale(8): clkcnt_n = 7, clk_equ_sysclk = 0.
	setField(&lcd.LCD_CLOCK, esp.LCD_CAM_LCD_CLOCK_LCD_CLKCNT_N_Msk, lcdClockClkcntNPos, lcdPclkPrescale-1)
	lcd.LCD_CLOCK.ClearBits(esp.LCD_CAM_LCD_CLOCK_LCD_CLK_EQU_SYSCLK)
	// lcd_ll_set_clock_idle_level(!pclk_idle_low = 1), lcd_ll_set_pixel_clock_edge(0).
	lcd.LCD_CLOCK.SetBits(esp.LCD_CAM_LCD_CLOCK_LCD_CK_IDLE_EDGE)
	lcd.LCD_CLOCK.ClearBits(esp.LCD_CAM_LCD_CLOCK_LCD_CK_OUT_EDGE)
	// lcd_ll_set_dc_level(idle 0, cmd 1, dummy 0, data 0): *_set = (phase != idle).
	lcd.LCD_MISC.ClearBits(esp.LCD_CAM_LCD_MISC_LCD_CD_IDLE_EDGE)
	lcd.LCD_MISC.SetBits(esp.LCD_CAM_LCD_MISC_LCD_CD_CMD_SET)
	lcd.LCD_MISC.ClearBits(esp.LCD_CAM_LCD_MISC_LCD_CD_DUMMY_SET)
	lcd.LCD_MISC.ClearBits(esp.LCD_CAM_LCD_MISC_LCD_CD_DATA_SET)

	lcdBusy = false
	return nil
}

// gdmaInit — gdma_new_channel(TX) + gdma_connect(LCD) + gdma_apply_strategy
// + gdma_set_transfer_ability for pair 0 (the first free one).
func gdmaInit() {
	dma := esp.DMA

	// periph_module_enable(PERIPH_GDMA_MODULE), gdma_ll_enable_clock(true).
	esp.SYSTEM.PERIP_CLK_EN1.SetBits(esp.SYSTEM_PERIP_CLK_EN1_DMA_CLK_EN)
	esp.SYSTEM.PERIP_RST_EN1.ClearBits(esp.SYSTEM_PERIP_RST_EN1_DMA_RST)
	dma.MISC_CONF.SetBits(dmaMiscConfClkEn)

	// gdma_ll_tx_enable_interrupt(all, false), gdma_ll_tx_clear_interrupt_status(all).
	dma.OUT_INT_ENA_CH0.Set(0)
	dma.OUT_INT_CLR_CH0.Set(0xffffffff)

	// gdma_connect: gdma_ll_tx_reset_channel, gdma_ll_tx_connect_to_periph(LCD).
	dma.OUT_CONF0_CH0.SetBits(dmaOutConf0Rst)
	dma.OUT_CONF0_CH0.ClearBits(dmaOutConf0Rst)
	dma.OUT_PERI_SEL_CH0.Set(gdmaTrigLCD)

	// gdma_apply_strategy{auto_update_desc: true, owner_check: true}.
	dma.OUT_CONF1_CH0.SetBits(dmaOutConf1CheckOwn)
	dma.OUT_CONF0_CH0.SetBits(dmaOutConf0AutoWrb)

	// gdma_set_transfer_ability: for TX, burst for data and descriptors,
	// PSRAM block size 16 bytes (index 0).
	dma.OUT_CONF0_CH0.SetBits(dmaOutConf0DataBurst | dmaOutConf0DscrBurst)
	dma.OUT_CONF1_CH0.ClearBits(dmaOutConf1BkSizeMsk)
}

// connectOutSignal — gpio_set_direction(OUTPUT) + esp_rom_gpio_connect_out_signal
// + gpio_hal_iomux_func_sel(PIN_FUNC_GPIO). Configure sets up IO_MUX and output
// enable, then the pin output is switched to the peripheral signal
// (FUNCn_OUT_SEL_CFG = signal number, no inversion, OEN from the peripheral —
// the same as machine.Pin.configure does in TinyGo for SPI).
func connectOutSignal(p machine.Pin, sig uint32) {
	p.Configure(machine.PinConfig{Mode: machine.PinOutput})
	reg := (*volatile.Register32)(unsafe.Add(unsafe.Pointer(&esp.GPIO.FUNC0_OUT_SEL_CFG), uintptr(p)*4))
	reg.Set(sig)
}

// lcdSetPhaseCycles — lcd_ll_set_phase_cycles.
func lcdSetPhaseCycles(cmd, dummy, data uint32) {
	u := &esp.LCD_CAM.LCD_USER
	setBit(u, esp.LCD_CAM_LCD_USER_LCD_CMD, cmd > 0)
	setBit(u, esp.LCD_CAM_LCD_USER_LCD_DUMMY, dummy > 0)
	setBit(u, esp.LCD_CAM_LCD_USER_LCD_DOUT, data > 0)
	setBit(u, esp.LCD_CAM_LCD_USER_LCD_CMD_2_CYCLE_EN, cmd > 1)
	// dummy_cycles − 1 and data_cycles − 1 with wrap-around, as in C: the field
	// gets the low bits (all ones for 0 cycles; the phase is disabled anyway).
	setField(u, esp.LCD_CAM_LCD_USER_LCD_DUMMY_CYCLELEN_Msk, lcdUserDummyCycPos, dummy-1)
	setField(u, esp.LCD_CAM_LCD_USER_LCD_DOUT_CYCLELEN_Msk, lcdUserDoutCycPos, data-1)
}

// lcdStart is lcd_ll_start: first update (apply parameters), then start.
func lcdStart() {
	esp.LCD_CAM.LCD_USER.SetBits(esp.LCD_CAM_LCD_USER_LCD_UPDATE)
	esp.LCD_CAM.LCD_USER.SetBits(esp.LCD_CAM_LCD_USER_LCD_START)
}

// lcdTimeoutCycles is 1 ms; a row (2 command cycles + 248 bytes at 10 MHz) takes ≈ 25 µs.
const lcdTimeoutCycles = 1000 * cpuMHz

func lcdWaitTransDone() error {
	start := ccount()
	for esp.LCD_CAM.LC_DMA_INT_RAW.Get()&esp.LCD_CAM_LC_DMA_INT_RAW_LCD_TRANS_DONE_INT_RAW == 0 {
		if ccount()-start > lcdTimeoutCycles {
			return errLCDTimeout
		}
	}
	return nil
}

// lcdWaitIdle is the equivalent of while (i2s_is_busy()) in epd_output_row():
// wait for the previous row to finish.
func lcdWaitIdle() error {
	if !lcdBusy {
		return nil
	}
	if err := lcdWaitTransDone(); err != nil {
		return err
	}
	lcdBusy = false
	return nil
}

// lcdSendRow starts output of a row (esp_lcd_panel_io_tx_color with
// lcd_cmd = 0): what panel_io_i80_tx_color, lcd_default_isr_handler and
// lcd_start_transaction do. It does not wait for completion; lcdWaitIdle checks
// that.
func lcdSendRow(row *raster.BusRow) {
	lcd := esp.LCD_CAM
	dma := esp.DMA

	// ISR: lcd_ll_clear_interrupt_status, reverse_bit_order(false),
	// swap_byte_order(8 bits, false).
	lcd.LC_DMA_INT_CLR.Set(0x3)
	lcd.LCD_USER.ClearBits(esp.LCD_CAM_LCD_USER_LCD_BIT_ORDER)
	lcd.LCD_USER.ClearBits(esp.LCD_CAM_LCD_USER_LCD_8BITS_ORDER | esp.LCD_CAM_LCD_USER_LCD_BYTE_ORDER)

	// lcd_com_mount_dma_data: a single descriptor (248 < 4095), end of chain.
	volatile.StoreUint32(&lcdDesc.buffer, uint32(uintptr(unsafe.Pointer(&row.B[0]))))
	volatile.StoreUint32(&lcdDesc.next, 0)
	volatile.StoreUint32(&lcdDesc.dw0, raster.BusRowBytes|raster.BusRowBytes<<12|dmaDw0SucEOF|dmaDw0OwnerDMA)

	// lcd_start_transaction: lcd_cmd_bits = 10 on an 8-bit bus → 2 command
	// cycles (DC = 1, i.e. STH high), command value 0
	// (lcd_ll_set_command for 8 bits: (0 & 0xFF) | (0 & 0xFF00) << 8 = 0).
	lcd.LCD_CMD_VAL.Set(0)
	lcdSetPhaseCycles(2, 0, 1)
	// lcd_ll_set_blank_cycles(1, 1).
	lcd.LCD_MISC.SetBits(esp.LCD_CAM_LCD_MISC_LCD_BK_EN)
	setField(&lcd.LCD_MISC, esp.LCD_CAM_LCD_MISC_LCD_VFK_CYCLELEN_Msk, lcdMiscVfkCycPos, 0)
	setField(&lcd.LCD_MISC, esp.LCD_CAM_LCD_MISC_LCD_VBK_CYCLELEN_Msk, lcdMiscVbkCycPos, 0)

	// gdma_start: gdma_ll_tx_set_desc_addr, gdma_ll_tx_start.
	setField(&dma.OUT_LINK_CH0, dmaOutLinkAddrMsk, 0, uint32(uintptr(unsafe.Pointer(&lcdDesc))))
	dma.OUT_LINK_CH0.SetBits(dmaOutLinkStart)
	// "delay 1us is sufficient for DMA to pass data to LCD FIFO".
	waitMicros(1)
	lcdStart()
	lcdBusy = true
}

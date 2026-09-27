# tinygo-lilygo-badge

Pure-Go ([TinyGo](https://tinygo.org)) drivers for the **LILYGO T5 4.7" E-Paper S3** board: the ED047TC1 e-paper panel and the GT911 capacitive touch controller. They run without ESP-IDF or FreeRTOS. The goal is a base for an e-paper badge written in Go.

The display driver is a port of the vendor C driver [LilyGo-EPD47](https://github.com/Xinyuan-LilyGO/LilyGo-EPD47/tree/esp32s3) (`esp32s3` branch).

## Status

Tested on one board, LILYGO T5-4.7-S3 **rev V2.4** (ESP32-S3-WROOM-1-N16R8). Other revisions have not been tested.

| Part | State |
|---|---|
| Panel power, clear, 16-level grayscale | works |
| Partial (area) updates | works, the area does not shift |
| Text and graphics via `tinyfont` / `drivers.Displayer` | works |
| Data bus via LCD_CAM + GDMA (DMA) | works |
| GT911 touch: points, sleep, wake | works (see known issues) |
| API in the style of `tinygo-org/drivers` | in progress |

Comparison with the original C driver on the same board and the same test image (TinyGo 0.41.1). The image is identical:

| | Original (C, ESP-IDF 4.4) | This port (Go) |
|---|---|---|
| Full clear | 1122 ms | 1009 ms |
| 16-level grayscale image | 686 ms | 269 ms (282 ms on TinyGo 0.42.0) |

The frame logic is checked byte for byte against the original C code: `epd_driver.c` is compiled on the host with stubs, and its output is compared with the port's.

## Layout

```
internal/epd          panel driver: power (74HCT4094), CKV timing, LCD_CAM + GDMA bus, Display type
internal/epd/raster   hardware-independent frame generator + host tests against the original C code
internal/gt911        GT911 touch driver (implements touch.Pointer)
internal/i2cfix       ESP-IDF-style I2C transactions for ESP32-S3 (works around a TinyGo I2C issue)
cmd/                  test programs: hello, clear, gray, area, contrast, touch, i2cbug, cfgprobe
test/idf-original-gray  reference ESP-IDF 4.4 project: the same test through the original C driver
```

## Build and flash

Requires TinyGo 0.42.0 (0.41.1 also works).

```sh
tinygo build -target=esp32s3-generic -o build/area.bin ./cmd/area
esptool --chip esp32s3 -p <port> write-flash 0x0 build/area.bin
```

Output goes to the board's USB-C port (USB-Serial/JTAG).

Host tests (need `gcc`):

```sh
cd internal/epd/raster && go test ./...
```

## Example

```go
var display epd.Display // 253 KB framebuffer: keep it global

func main() {
	epd.Init() // always first: puts the panel power register into a safe state

	display.ClearBuffer()
	tinyfont.WriteLine(&display, &freesans.Bold24pt7b, 40, 90, "Hello, e-paper", color.RGBA{A: 255})
	display.Display() // power on, clear, draw, power off

	display.FB.FillRect(300, 300, 200, 60, 0)
	display.DisplayArea(epd.Rect{X: 300, Y: 300, W: 200, H: 60}) // update only this area
}
```

## Notes and known issues

- **Panel power on rev V2.4.** On this revision the panel high voltage is switched only by the 74HCT4094 output that the original driver calls `ep_scan_direction` (PWR_EN in the schematic). The original `epd_poweroff()` leaves it on. `epd.PowerOff()` clears the whole register.
- **Area clear.** The original `epd_push_pixels()` calls `reorder_line_buffer()`, which on S3 shifts the edges of a cleared area by ±8 pixels. The port does not do this.
- **TinyGo I2C on ESP32-S3/C3.** A read from a device that does not acknowledge returns `nil` and garbage. This is the esp32xx part of [tinygo#5584](https://github.com/tinygo-org/tinygo/issues/5584); the fix for these chips was reverted in [#5602](https://github.com/tinygo-org/tinygo/pull/5602). It still happens in TinyGo 0.42.0. `internal/i2cfix` works around it.
- **GT911** sometimes stops acknowledging its I2C address, and an ESP32 reset does not bring it back. The cause is not known yet.
- **Memory.** TinyGo has no PSRAM support here, so the 253 KB framebuffer lives in internal SRAM. `tinygo -size` over-reports RAM for this target.
- **Dark gray levels** are hard to tell apart. The original driver behaves the same way (same contrast table).

## License

Different parts of the repository use different licenses:

| Path | License | Why |
|---|---|---|
| `internal/epd/` (incl. `raster/`) | **GPL-3.0-only**, see [internal/epd/LICENSE](internal/epd/LICENSE) | a port of LilyGo-EPD47, which is GPL-3.0 |
| `test/idf-original-gray/components/epd_driver/` | **GPL-3.0**, see its [LICENSE](test/idf-original-gray/components/epd_driver/LICENSE) | a copy of the original driver (plus one `#include <math.h>` for ESP-IDF 4.4), used only for tests |
| everything else (`internal/gt911`, `internal/i2cfix`, `cmd/`, the reference test program) | **MIT**, see [LICENSE](LICENSE) | original code |

`internal/epd/lcd.go` and `internal/i2cfix` follow register sequences from ESP-IDF (Copyright Espressif Systems, Apache-2.0, see [LICENSES/Apache-2.0.txt](LICENSES/Apache-2.0.txt)).

Firmware that imports `internal/epd` (for example the programs in `cmd/`) is a combined work under GPL-3.0 when distributed. The MIT-licensed packages can be used on their own without that restriction.

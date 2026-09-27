# Reference test on the original LilyGo driver

Not part of the Go project. It exists only for a fair A/B comparison with the TinyGo port: the same image (16 gray bands, a border, orientation marks) and the same sequence `init → poweron → clear → draw_grayscale → poweroff`, but through the original C driver.

- `components/epd_driver/` is a copy of the display output files of the original driver (LilyGo-EPD47, `esp32s3` branch, **GPL-3.0**, see its `LICENSE`). The only change is an added `#include <math.h>` in `epd_driver.c`: the original includes it only for ESP-IDF 5+, and without it the project does not build on 4.4. The Go tests in `internal/epd/raster` use the same copy to compare the port with the original byte for byte.
- `main/main.c` is the test. Its Go counterpart is `cmd/gray/main.go`.
- `sdkconfig.defaults` sets 240 MHz, a 1 kHz FreeRTOS tick (as in Arduino, where LilyGo tests the driver), the framebuffer in PSRAM (like `ps_calloc` in the LilyGo examples), and the console on USB-Serial/JTAG.

## Build (ESP-IDF 4.4)

```sh
cd test/idf-original-gray
. $IDF_PATH/export.sh
idf.py set-target esp32s3
idf.py build
idf.py -p /dev/cu.usbmodemXXXX flash monitor
```

To go back to the TinyGo firmware, write it at address 0x0 again (`esptool … write-flash 0x0 build/gray.bin`).

## Differences from the port's conditions

- The framebuffer is in PSRAM (the port keeps it in internal SRAM, because TinyGo does not support PSRAM here).
- `epd_poweroff_all()` is called right after `epd_init()`: on board rev V2.4 `epd_init()` turns on the panel high voltage. This does not affect the output.

/*
 * SPDX-License-Identifier: MIT
 * Copyright (c) 2026 0hJonny
 *
 * Reference test for comparison with the TinyGo port (cmd/gray):
 * the same image and the same sequence, but through the original
 * LilyGo-EPD47 C driver (components/epd_driver, GPL-3.0).
 */
#include <stdio.h>
#include <string.h>

#include "esp_heap_caps.h"
#include "esp_timer.h"
#include "freertos/FreeRTOS.h"
#include "freertos/task.h"

#include "epd_driver.h"

static uint8_t *fb;

// Gray level 0–15. epd_draw_pixel takes the high nibble for odd x
// and (color >> 4) for even x, so the level goes into both nibbles.
static void fill_rect(int32_t x, int32_t y, int32_t w, int32_t h, uint8_t gray)
{
    epd_fill_rect(x, y, w, h, gray * 0x11, fb);
}

// The same as drawTestPattern() in cmd/gray/main.go.
static void draw_test_pattern(void)
{
    memset(fb, 0xFF, EPD_WIDTH * EPD_HEIGHT / 2); // white

    // 16 vertical bands, 60 pixels each: black (0) on the left, white (15) on the right.
    const int32_t band_w = EPD_WIDTH / 16;
    for (int32_t g = 0; g < 16; g++) {
        fill_rect(g * band_w, 120, band_w, 300, (uint8_t)g);
    }

    // A 4-pixel border around the screen.
    fill_rect(0, 0, EPD_WIDTH, 4, 0);
    fill_rect(0, EPD_HEIGHT - 4, EPD_WIDTH, 4, 0);
    fill_rect(0, 0, 4, EPD_HEIGHT, 0);
    fill_rect(EPD_WIDTH - 4, 0, 4, EPD_HEIGHT, 0);

    // Orientation marks: a black 60×60 square in the top-left corner
    // and a gray (level 8) 30×30 square in the bottom-right corner.
    fill_rect(20, 20, 60, 60, 0);
    fill_rect(EPD_WIDTH - 50, EPD_HEIGHT - 50, 30, 30, 8);
}

void app_main(void)
{
    epd_init();
    // epd_init() → epd_base_init() sets ep_scan_direction = 1, and on board
    // rev V2.4 that is PWR_EN: the high voltage is already on at this point.
    // Power off right away; epd_poweron() sets the needed bits again before drawing.
    epd_poweroff_all();

    fb = heap_caps_malloc(EPD_WIDTH * EPD_HEIGHT / 2, MALLOC_CAP_SPIRAM);
    if (fb == NULL) {
        printf("orig: no memory for the framebuffer (is PSRAM enabled?)\n");
        return;
    }

    vTaskDelay(pdMS_TO_TICKS(3000));
    printf("orig: preparing the framebuffer\n");
    draw_test_pattern();

    epd_poweron();
    printf("orig: power on, clearing\n");
    int64_t t0 = esp_timer_get_time();
    epd_clear();
    int64_t t1 = esp_timer_get_time();
    printf("orig: clear %lld ms\n", (t1 - t0) / 1000);

    epd_draw_grayscale_image(epd_full_screen(), fb);
    int64_t t2 = esp_timer_get_time();
    printf("orig: draw %lld ms\n", (t2 - t1) / 1000);

    // On V2.4 epd_poweroff() does not clear PWR_EN, so epd_poweroff_all() is needed.
    epd_poweroff();
    epd_poweroff_all();
    printf("orig: power off\n");

    for (int i = 0;; i++) {
        printf("done, tick %d\n", i);
        vTaskDelay(pdMS_TO_TICKS(1000));
    }
}

/*
 * SPDX-License-Identifier: GPL-3.0-only
 * Copyright (c) 2026 0hJonny
 * Test harness that compiles the original LilyGo-EPD47 epd_driver.c (GPL-3.0).
 */
/*
 * Host harness for the original epd_driver.c (LilyGo-EPD47, esp32s3 branch):
 * low-level calls (ed047tc1.c / i2s_data_bus.c) are written to stdout and
 * FreeRTOS is replaced with synchronous stubs. Used by the Go test of package
 * raster to compare the port with the original byte for byte.
 *
 * Output format: "S" is epd_start_frame, "R <ticks> <hex of 240 bytes>" is
 * epd_output_row (the row that starts shifting out onto the bus),
 * "K" is epd_skip, "E" is epd_end_frame.
 *
 * Commands in the input file (one per line):
 *   push x y w h time color
 *   image x y w h mode <hex of the image data>
 */
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "epd_driver.h"
#include "ed047tc1.h"
#include "freertos/queue.h"
#include "freertos/semphr.h"
#include "freertos/task.h"

// ---- low level, as on S3 (a single row buffer, switch is a no-op) ----

static uint8_t buffer[(960 + 32) / 4];

void epd_base_init(uint32_t epd_row_width) { (void)epd_row_width; }
void epd_poweron(void) {}
void epd_poweroff(void) {}
void epd_poweroff_all(void) {}
void epd_start_frame(void) { puts("S"); }
void epd_end_frame(void) { puts("E"); }
void epd_skip(void) { puts("K"); }
void epd_switch_buffer(void) {}
uint8_t *epd_get_current_buffer(void) { return buffer; }

void epd_output_row(uint32_t output_time_dus)
{
    printf("R %u ", output_time_dus);
    for (int i = 0; i < 960 / 4; i++) {
        printf("%02x", buffer[i]);
    }
    putchar('\n');
}

// ---- FreeRTOS: an unbounded queue, tasks run immediately ----

typedef struct {
    uint8_t *data;
    uint32_t item, head, tail, cap;
} queue_t;

QueueHandle_t xQueueCreate(uint32_t len, uint32_t item_size)
{
    queue_t *q = calloc(1, sizeof(queue_t));
    q->item = item_size;
    q->cap = len;
    q->data = malloc((size_t)len * item_size);
    return q;
}

BaseType_t xQueueSendToBack(QueueHandle_t h, const void *item, TickType_t wait)
{
    (void)wait;
    queue_t *q = h;
    if (q->tail == q->cap) {
        q->cap *= 2;
        q->data = realloc(q->data, (size_t)q->cap * q->item);
    }
    memcpy(q->data + (size_t)q->tail * q->item, item, q->item);
    q->tail++;
    return pdTRUE;
}

BaseType_t xQueueReceive(QueueHandle_t h, void *item, TickType_t wait)
{
    (void)wait;
    queue_t *q = h;
    if (q->head == q->tail) {
        fprintf(stderr, "harness: read from an empty queue\n");
        exit(2);
    }
    memcpy(item, q->data + (size_t)q->head * q->item, q->item);
    q->head++;
    if (q->head == q->tail) {
        q->head = q->tail = 0;
    }
    return pdTRUE;
}

SemaphoreHandle_t xSemaphoreCreateBinary(void) { return (void *)1; }
BaseType_t xSemaphoreGive(SemaphoreHandle_t s) { (void)s; return pdTRUE; }
BaseType_t xSemaphoreTake(SemaphoreHandle_t s, TickType_t w) { (void)s; (void)w; return pdTRUE; }
void vSemaphoreDelete(SemaphoreHandle_t s) { (void)s; }

// provide_out is created first and fills the whole queue, then feed_display
// drains it; the row order is the same as with two cores.
BaseType_t xTaskCreatePinnedToCore(void (*fn)(void *), const char *name, uint32_t stack,
                                   void *param, uint32_t prio, TaskHandle_t *handle, int core)
{
    (void)name; (void)stack; (void)prio; (void)core;
    *handle = NULL;
    fn(param);
    return pdTRUE;
}
void vTaskDelete(TaskHandle_t t) { (void)t; }
void vTaskDelay(TickType_t ticks) { (void)ticks; }

// ---- commands ----

static int hexval(int c)
{
    return c <= '9' ? c - '0' : (c | 0x20) - 'a' + 10;
}

int main(int argc, char **argv)
{
    if (argc != 2) {
        fprintf(stderr, "usage: harness <commands>\n");
        return 2;
    }
    FILE *f = fopen(argv[1], "r");
    if (!f) {
        perror("fopen");
        return 2;
    }
    epd_init();

    static char line[1 << 20];
    while (fgets(line, sizeof line, f)) {
        char cmd[16];
        Rect_t a;
        int n = 0;
        if (sscanf(line, "%15s %d %d %d %d%n", cmd, &a.x, &a.y, &a.width, &a.height, &n) != 5) {
            continue;
        }
        printf("# %s\n", cmd);
        if (strcmp(cmd, "push") == 0) {
            int time, color;
            sscanf(line + n, "%d %d", &time, &color);
            epd_push_pixels(a, (int16_t)time, color);
        } else if (strcmp(cmd, "image") == 0) {
            int mode, m = 0;
            sscanf(line + n, "%d %n", &mode, &m);
            const char *hex = line + n + m;
            size_t len = strcspn(hex, "\r\n") / 2;
            uint8_t *data = malloc(len ? len : 1);
            for (size_t i = 0; i < len; i++) {
                data[i] = (uint8_t)(hexval(hex[2 * i]) << 4 | hexval(hex[2 * i + 1]));
            }
            epd_draw_image(a, data, (DrawMode_t)mode);
            free(data);
        }
    }
    return 0;
}

#pragma once
#include "freertos/FreeRTOS.h"
BaseType_t xTaskCreatePinnedToCore(void (*fn)(void *), const char *name, uint32_t stack,
                                   void *param, uint32_t prio, TaskHandle_t *handle, int core);
void vTaskDelete(TaskHandle_t t);
void vTaskDelay(TickType_t ticks);

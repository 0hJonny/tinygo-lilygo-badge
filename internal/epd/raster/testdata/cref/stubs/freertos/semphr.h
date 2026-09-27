#pragma once
#include "freertos/FreeRTOS.h"
SemaphoreHandle_t xSemaphoreCreateBinary(void);
BaseType_t xSemaphoreGive(SemaphoreHandle_t s);
BaseType_t xSemaphoreTake(SemaphoreHandle_t s, TickType_t wait);
void vSemaphoreDelete(SemaphoreHandle_t s);

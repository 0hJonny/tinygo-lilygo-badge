#pragma once
#include "freertos/FreeRTOS.h"
QueueHandle_t xQueueCreate(uint32_t len, uint32_t item_size);
BaseType_t xQueueSendToBack(QueueHandle_t q, const void *item, TickType_t wait);
BaseType_t xQueueReceive(QueueHandle_t q, void *item, TickType_t wait);

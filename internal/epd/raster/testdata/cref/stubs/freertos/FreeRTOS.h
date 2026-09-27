#pragma once
#include <stdint.h>
typedef void *QueueHandle_t;
typedef void *SemaphoreHandle_t;
typedef void *TaskHandle_t;
typedef uint32_t TickType_t;
typedef int BaseType_t;
#define portMAX_DELAY 0xffffffffu
#define pdTRUE 1
#define pdFALSE 0

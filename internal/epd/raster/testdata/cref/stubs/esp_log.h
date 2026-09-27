#pragma once
#include <stdio.h>
#define ESP_LOGW(tag, ...) (fprintf(stderr, __VA_ARGS__), fputc('\n', stderr))
#define ESP_LOGE(tag, ...) ESP_LOGW(tag, __VA_ARGS__)
#define ESP_LOGI(tag, ...) ESP_LOGW(tag, __VA_ARGS__)

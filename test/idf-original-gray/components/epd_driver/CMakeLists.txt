# LilyGo-EPD47 panel driver (esp32s3 branch), GPL-3.0: a copy of the upstream
# sources with one added #include <math.h> in epd_driver.c. Display output only:
# font.c (needs zlib) and touch.cpp (needs Arduino) are not included.
idf_component_register(SRCS "ed047tc1.c" "epd_driver.c" "i2s_data_bus.c" "rmt_pulse.c"
                       INCLUDE_DIRS "."
                       REQUIRES driver esp_lcd)

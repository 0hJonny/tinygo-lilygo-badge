//go:build !bitbang

package main

// busMode is the data bus: LCD_CAM + GDMA by default.
// Building with -tags bitbang gives the software bus for comparison.
const busMode = "dma"

//go:build !run2 && !run3

package main

const runName = "run1 (order A B C D, 400 then 100 kHz)"

var order = "ABCD"
var speeds = []uint32{400000, 100000}

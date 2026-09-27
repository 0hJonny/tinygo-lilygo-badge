// SPDX-License-Identifier: GPL-3.0-only
// Copyright (c) 2026 0hJonny
// Based on LilyGo-EPD47 (https://github.com/Xinyuan-LilyGO/LilyGo-EPD47), GPL-3.0.

package raster

import (
	"bufio"
	"bytes"
	"encoding/hex"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Comparison with the original: epd_driver.c (copy in test/idf-original-gray)
// is compiled on the host with ESP-IDF/FreeRTOS stubs (testdata/cref); its
// low-level calls are recorded and compared with what the port produces.

const origDriverDir = "../../../test/idf-original-gray/components/epd_driver"

// recorder is a Sink that records calls in the testdata/cref/harness.c format.
type recorder struct{ out []string }

func (r *recorder) StartFrame() { r.out = append(r.out, "S") }
func (r *recorder) EndFrame()   { r.out = append(r.out, "E") }
func (r *recorder) Skip()       { r.out = append(r.out, "K") }
func (r *recorder) Poll()       {}
func (r *recorder) OutputRow(ticks uint32, row *BusRow) {
	r.out = append(r.out, fmt.Sprintf("R %d %s", ticks, hex.EncodeToString(row.B[:LineBytes])))
}

type command struct {
	push  bool
	a     Rect
	time  int16 // push
	white bool  // push
	mode  Mode  // image
	data  []byte
}

var cMode = map[Mode]int{BlackOnWhite: 1, WhiteOnWhite: 2, WhiteOnBlack: 4}

func buildHarness(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("gcc"); err != nil {
		t.Skip("gcc not found")
	}
	if _, err := os.Stat(filepath.Join(origDriverDir, "epd_driver.c")); err != nil {
		t.Skip("original driver source not found:", err)
	}
	bin := filepath.Join(t.TempDir(), "harness")
	cmd := exec.Command("gcc", "-std=gnu11", "-O1", "-w", "-DCONFIG_IDF_TARGET_ESP32S3=1",
		"-I", "testdata/cref/stubs", "-I", origDriverDir, "-o", bin,
		"testdata/cref/harness.c", filepath.Join(origDriverDir, "epd_driver.c"))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building the harness: %v\n%s", err, out)
	}
	return bin
}

func runC(t *testing.T, bin string, cmds []command) []string {
	t.Helper()
	var in bytes.Buffer
	for _, c := range cmds {
		if c.push {
			color := 0
			if c.white {
				color = 1
			}
			fmt.Fprintf(&in, "push %d %d %d %d %d %d\n", c.a.X, c.a.Y, c.a.W, c.a.H, c.time, color)
		} else {
			fmt.Fprintf(&in, "image %d %d %d %d %d %s\n", c.a.X, c.a.Y, c.a.W, c.a.H, cMode[c.mode], hex.EncodeToString(c.data))
		}
	}
	file := filepath.Join(t.TempDir(), "cmds.txt")
	if err := os.WriteFile(file, in.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command(bin, file).Output()
	if err != nil {
		t.Fatalf("harness: %v", err)
	}
	var lines []string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1<<16), 1<<20)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}
	return undoPushReorder(t, lines)
}

// undoPushReorder undoes reorder_line_buffer() in epd_push_pixels rows: the
// port deliberately does not do it (see Frame.PushPixels). Swapping the halves
// of a 32-bit word is an involution, and it does not change zero rows.
func undoPushReorder(t *testing.T, lines []string) []string {
	inPush := false
	for i, l := range lines {
		if strings.HasPrefix(l, "# ") {
			inPush = l == "# push"
			continue
		}
		if !inPush || !strings.HasPrefix(l, "R ") {
			continue
		}
		f := strings.Fields(l)
		b, err := hex.DecodeString(f[2])
		if err != nil {
			t.Fatal(err)
		}
		for j := 0; j+3 < len(b); j += 4 {
			b[j], b[j+1], b[j+2], b[j+3] = b[j+2], b[j+3], b[j], b[j+1]
		}
		lines[i] = f[0] + " " + f[1] + " " + hex.EncodeToString(b)
	}
	return lines
}

func runGo(cmds []command) []string {
	var f Frame
	f.Reset()
	r := &recorder{}
	for _, c := range cmds {
		if c.push {
			r.out = append(r.out, "# push")
			f.PushPixels(r, c.a, c.time, c.white)
		} else {
			r.out = append(r.out, "# image")
			f.DrawImage(r, c.a, c.data, c.mode)
		}
	}
	return r.out
}

func compare(t *testing.T, want, got []string) {
	t.Helper()
	n := min(len(want), len(got))
	for i := 0; i < n; i++ {
		if want[i] != got[i] {
			t.Fatalf("mismatch at record %d:\n original: %.120s\n port:     %.120s", i, want[i], got[i])
		}
	}
	if len(want) != len(got) {
		t.Fatalf("length differs: original %d records, port %d", len(want), len(got))
	}
}

func randomImage(rng *rand.Rand, a Rect) []byte {
	b := make([]byte, ImageStride(a.W)*a.H)
	rng.Read(b)
	return b
}

func TestPushPixelsMatchesOriginal(t *testing.T) {
	bin := buildHarness(t)
	areas := []Rect{
		FullScreen,
		{100, 10, 30, 3},
		{0, 0, 1, 1},
		{1, 0, 2, 540},
		{3, 537, 5, 3},
		{957, 200, 3, 100},
		{400, 539, 17, 1},
		{17, 50, 900, 0},
	}
	var cmds []command
	for i, a := range areas {
		cmds = append(cmds, command{push: true, a: a, time: 50, white: i%2 == 0})
	}
	// A full area clear, like epd_clear_area.
	for i := 0; i < 4; i++ {
		cmds = append(cmds, command{push: true, a: Rect{123, 45, 67, 89}, time: 50})
	}
	compare(t, runC(t, bin, cmds), runGo(cmds))
}

func TestDrawImageMatchesOriginal(t *testing.T) {
	bin := buildHarness(t)
	rng := rand.New(rand.NewSource(1))
	areas := []Rect{
		FullScreen,
		{0, 0, 960, 7},     // full width, short
		{10, 20, 30, 5},    // even x and width
		{11, 20, 30, 5},    // odd x
		{10, 20, 31, 5},    // odd width
		{11, 533, 31, 7},   // odd x and width, down to the bottom edge
		{900, 100, 100, 4}, // past the right edge
		{-20, 30, 50, 3},   // negative even x
		{-21, 30, 50, 3},   // negative odd x
		{40, -3, 20, 6},    // negative y
		{0, 0, 959, 2},     // almost full width
	}
	for _, mode := range []Mode{BlackOnWhite, WhiteOnWhite, WhiteOnBlack} {
		for _, a := range areas {
			t.Run(fmt.Sprintf("mode%d_%d_%d_%d_%d", mode, a.X, a.Y, a.W, a.H), func(t *testing.T) {
				cmds := []command{{a: a, mode: mode, data: randomImage(rng, a)}}
				compare(t, runC(t, bin, cmds), runGo(cmds))
			})
		}
	}
}

// The skipping counter is shared across calls: a frame that ends with skips
// affects the next call. Check chains of commands.
func TestSequenceMatchesOriginal(t *testing.T) {
	bin := buildHarness(t)
	rng := rand.New(rand.NewSource(2))
	a1 := Rect{50, 60, 70, 8}
	a2 := Rect{0, 0, 960, 3}
	a3 := Rect{101, 2, 33, 4}
	cmds := []command{
		{push: true, a: FullScreen, time: 50},
		{a: a1, mode: BlackOnWhite, data: randomImage(rng, a1)},
		{a: a2, mode: WhiteOnBlack, data: randomImage(rng, a2)},
		{push: true, a: a3, time: 50, white: true},
		{a: a3, mode: WhiteOnWhite, data: randomImage(rng, a3)},
	}
	compare(t, runC(t, bin, cmds), runGo(cmds))
}

// DrawFramebufferArea must produce the same as DrawImage with the area image cut
// out of the framebuffer (and DrawImage is checked against the original).
func TestFramebufferAreaMatchesImage(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	var fb Framebuffer
	rng.Read(fb[:])
	areas := []Rect{FullScreen, {10, 20, 30, 5}, {11, 20, 30, 5}, {10, 20, 31, 5}, {11, 533, 31, 7}, {959, 0, 1, 540}, {0, 0, 1, 1}}
	for _, mode := range []Mode{BlackOnWhite, WhiteOnBlack} {
		for _, a := range areas {
			img := make([]byte, ImageStride(a.W)*a.H)
			for y := 0; y < a.H; y++ {
				for x := 0; x < a.W; x++ {
					i := y*a.W + x
					if a.W%2 == 1 {
						i += y // odd width: one extra nibble at the end of each row
					}
					sx, sy := a.X+x, a.Y+y
					v := fb[sy*FBLineBytes+sx/2]
					if sx%2 == 1 {
						v >>= 4
					}
					v &= 0x0F
					if i%2 == 1 {
						img[i/2] |= v << 4
					} else {
						img[i/2] |= v
					}
				}
			}
			var f1, f2 Frame
			r1, r2 := &recorder{}, &recorder{}
			f1.DrawImage(r1, a, img, mode)
			f2.DrawFramebufferArea(r2, &fb, a, mode)
			t.Run(fmt.Sprintf("mode%d_%d_%d_%d_%d", mode, a.X, a.Y, a.W, a.H), func(t *testing.T) {
				compare(t, r1.out, r2.out)
			})
		}
	}
}

// A custom contrast table changes only row times, not data.
func TestSetContrast(t *testing.T) {
	a := Rect{10, 20, 30, 5}
	img := randomImage(rand.New(rand.NewSource(4)), a)
	var f1, f2 Frame
	r1, r2 := &recorder{}, &recorder{}
	c := Contrast{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}
	f2.SetContrast(&c, nil)
	f1.DrawImage(r1, a, img, BlackOnWhite)
	f2.DrawImage(r2, a, img, BlackOnWhite)
	if len(r1.out) != len(r2.out) {
		t.Fatalf("length differs: %d and %d", len(r1.out), len(r2.out))
	}
	frame := -1
	for i := range r1.out {
		if r1.out[i] == "S" {
			frame++
		}
		if !strings.HasPrefix(r1.out[i], "R ") {
			if r1.out[i] != r2.out[i] {
				t.Fatalf("record %d: %q and %q", i, r1.out[i], r2.out[i])
			}
			continue
		}
		f1s, f2s := strings.Fields(r1.out[i]), strings.Fields(r2.out[i])
		if f1s[2] != f2s[2] {
			t.Fatalf("record %d: row data differs", i)
		}
		want := fmt.Sprint(c[frame])
		if f1s[1] == fmt.Sprint(DefaultContrast[frame]) && f2s[1] != want && f2s[1] != fmt.Sprint(uint8(c[frame])) {
			t.Fatalf("record %d (frame %d): time %s, expected %s", i, frame, f2s[1], want)
		}
	}
}

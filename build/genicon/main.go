// Command genicon draws the application icon and writes build/windows/icon.ico.
// Run from the repo root: go run ./build/genicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

var (
	bgTop    = color.RGBA{R: 0x24, G: 0x3b, B: 0x55, A: 255}
	bgBottom = color.RGBA{R: 0x14, G: 0x22, B: 0x33, A: 255}
	arch     = color.RGBA{R: 0xf2, G: 0xf5, B: 0xf8, A: 255}
	light    = color.RGBA{R: 0x2e, G: 0xb8, B: 0x4c, A: 255}
)

// shade returns the colour at (x, y) in unit coordinates (0..1), or alpha 0 outside the icon.
func shade(x, y float64) color.RGBA {
	const r = 0.2
	cx, cy := math.Max(r-x, math.Max(0, x-(1-r))), math.Max(r-y, math.Max(0, y-(1-r)))
	if cx*cx+cy*cy > r*r {
		return color.RGBA{}
	}
	if dx, dy := x-0.5, y-0.64; dx*dx+dy*dy <= 0.085*0.085 {
		return light
	}
	const ax, ay, outer, inner, foot = 0.5, 0.56, 0.32, 0.19, 0.84
	dx, dy := x-ax, y-ay
	d := math.Hypot(dx, dy)
	if (dy <= 0 && d <= outer && d >= inner) || (dy > 0 && y <= foot && math.Abs(dx) <= outer && math.Abs(dx) >= inner) {
		return arch
	}
	t := y
	mix := func(a, b uint8) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t) }
	return color.RGBA{R: mix(bgTop.R, bgBottom.R), G: mix(bgTop.G, bgBottom.G), B: mix(bgTop.B, bgBottom.B), A: 255}
}

func render(size int) *image.NRGBA {
	const ss = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := range size {
		for px := range size {
			var r, g, b, a float64
			for sy := range ss {
				for sx := range ss {
					c := shade((float64(px)+(float64(sx)+0.5)/ss)/float64(size), (float64(py)+(float64(sy)+0.5)/ss)/float64(size))
					w := float64(c.A) / 255
					r, g, b, a = r+float64(c.R)*w, g+float64(c.G)*w, b+float64(c.B)*w, a+w
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{R: uint8(r / a), G: uint8(g / a), B: uint8(b / a), A: uint8(a / ss / ss * 255)})
		}
	}
	return img
}

func main() {
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	var images [][]byte
	for _, s := range sizes {
		var buf bytes.Buffer
		if err := png.Encode(&buf, render(s)); err != nil {
			log.Fatal(err)
		}
		images = append(images, buf.Bytes())
	}
	var out bytes.Buffer
	le := func(v any) {
		if err := binary.Write(&out, binary.LittleEndian, v); err != nil {
			log.Fatal(err)
		}
	}
	le([3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		dim := uint8(s % 256)
		le([4]uint8{dim, dim, 0, 0})
		le([2]uint16{1, 32})
		le([2]uint32{uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, img := range images {
		out.Write(img)
	}
	if err := os.WriteFile("build/windows/icon.ico", out.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile("build/appicon.png", images[len(images)-1], 0o644); err != nil {
		log.Fatal(err)
	}
}

package main

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"

	"ssh-tunnel-manager/internal/manager"
)

var iconColors = map[manager.Icon]color.RGBA{
	manager.IconIdle:  {R: 128, G: 128, B: 128, A: 255},
	manager.IconUp:    {R: 46, G: 184, B: 76, A: 255},
	manager.IconBusy:  {R: 240, G: 200, B: 20, A: 255},
	manager.IconError: {R: 220, G: 50, B: 47, A: 255},
}

// generateIcons renders the 32x32 filled-circle tray icons, one per state.
func generateIcons() (map[manager.Icon][]byte, error) {
	const size = 32
	const c, r = 15.5, 14.0
	out := make(map[manager.Icon][]byte, len(iconColors))
	for icon, col := range iconColors {
		img := image.NewRGBA(image.Rect(0, 0, size, size))
		for y := range size {
			for x := range size {
				dx, dy := float64(x)-c, float64(y)-c
				if dx*dx+dy*dy <= r*r {
					img.SetRGBA(x, y, col)
				}
			}
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			return nil, fmt.Errorf("encode %s icon: %w", icon, err)
		}
		out[icon] = buf.Bytes()
	}
	return out, nil
}

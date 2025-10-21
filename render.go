package main

import (
	"encoding/hex"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"net/http"
	"strings"
)

func writePNG(w http.ResponseWriter, sim SimulationData) error {
	img := image.NewRGBA(image.Rect(0, 0, sim.CanvasWidth, sim.CanvasHeight))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.White}, image.Point{}, draw.Src)

	for _, p := range sim.Points {
		col := parseHexColor(p.Color)
		px := p.PixelWidth
		if px <= 0 {
			px = 3
		}
		r := px / 2
		for i := 1; i < len(p.Path); i++ {
			x0 := int(math.Round(p.Path[i-1].X))
			y0 := int(math.Round(p.Path[i-1].Y))
			x1 := int(math.Round(p.Path[i].X))
			y1 := int(math.Round(p.Path[i].Y))
			drawThickLine(img, x0, y0, x1, y1, r, col)
		}
	}
	return png.Encode(w, img)
}

func drawThickLine(img *image.RGBA, x0, y0, x1, y1, r int, col color.RGBA) {
	dx := x1 - x0
	dy := y1 - y0
	dist := math.Hypot(float64(dx), float64(dy))
	if dist == 0 {
		drawCircle(img, x0, y0, r, col)
		return
	}
	step := math.Max(1.0, float64(r)/2.0)
	steps := int(math.Ceil(dist / step))
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		x := int(math.Round(float64(x0) + t*float64(dx)))
		y := int(math.Round(float64(y0) + t*float64(dy)))
		drawCircle(img, x, y, r, col)
	}
}
func drawCircle(img *image.RGBA, cx, cy, r int, col color.RGBA) {
	if r <= 0 {
		if image.Pt(cx, cy).In(img.Bounds()) {
			img.SetRGBA(cx, cy, col)
		}
		return
	}
	rsq := r * r
	minx, maxx := cx-r, cx+r
	miny, maxy := cy-r, cy+r
	b := img.Bounds()
	for y := miny; y <= maxy; y++ {
		if y < b.Min.Y || y >= b.Max.Y {
			continue
		}
		for x := minx; x <= maxx; x++ {
			if x < b.Min.X || x >= b.Max.X {
				continue
			}
			dx := x - cx
			dy := y - cy
			if dx*dx+dy*dy <= rsq {
				img.SetRGBA(x, y, col)
			}
		}
	}
}
func parseHexColor(s string) color.RGBA {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return color.RGBA{0, 0, 0, 255}
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 3 {
		return color.RGBA{0, 0, 0, 255}
	}
	return color.RGBA{R: b[0], G: b[1], B: b[2], A: 255}
}

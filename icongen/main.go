// icongen draws the ClaudeBar app icon and writes app/AppIcon.icns (and a PNG preview).
// Run from the module folder: go run ./icongen
//
// The picture is the panel's day strip: a dark macOS-style rounded square with 7 day
// cells (two spent, one partly spent with an orange overspend stretch, four full) and
// the green clock lines under them. Shapes are drawn with signed distances, so edges are
// antialiased without supersampling.
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"

	"github.com/mitranim/gg"
)

func main() {
	defer gg.Fatal()

	big := draw(1024)
	var icns bytes.Buffer
	// PNG-backed icns entries; macOS scales from the nearest one.
	for _, entry := range []struct {
		kind string
		size int
	}{{"ic07", 128}, {"ic08", 256}, {"ic09", 512}, {"ic10", 1024}, {"icp5", 32}, {"icp6", 64}} {
		img := big
		if entry.size != 1024 {
			img = draw(entry.size)
		}
		var buf bytes.Buffer
		gg.Try(png.Encode(&buf, img))
		icns.WriteString(entry.kind)
		gg.Try(binary.Write(&icns, binary.BigEndian, uint32(8+buf.Len())))
		icns.Write(buf.Bytes())
	}

	var out bytes.Buffer
	out.WriteString("icns")
	gg.Try(binary.Write(&out, binary.BigEndian, uint32(8+icns.Len())))
	out.Write(icns.Bytes())
	gg.Try(os.WriteFile("app/AppIcon.icns", out.Bytes(), 0o644))

	var preview bytes.Buffer
	gg.Try(png.Encode(&preview, draw(512)))
	gg.Try(os.WriteFile("app/AppIcon.png", preview.Bytes(), 0o644))
}

type rgba struct{ r, g, b, a float64 }

func hex(val uint32) rgba {
	return rgba{float64(val>>16&0xff) / 255, float64(val>>8&0xff) / 255, float64(val&0xff) / 255, 1}
}

// A rounded rectangle in 1024-unit coordinates, filled with one color or a vertical
// gradient from `top` to `bottom`.
type shape struct {
	x0, y0, x1, y1, radius float64
	top, bottom            rgba
}

func draw(size int) *image.NRGBA {
	scale := float64(size) / 1024
	var shapes []shape

	// Body: Apple's icon grid, 824 of 1024 with a 185 radius.
	shapes = append(shapes, shape{100, 100, 924, 924, 185, hex(0x24324A), hex(0x0E1420)})

	const (
		left, right = 172.0, 852.0
		gap         = 16.0
		cellTop     = 392.0
		cellBottom  = 592.0
		lineTop     = 624.0
		lineBottom  = 652.0
	)
	cell := (right - left - 6*gap) / 7
	spent, blue, orange := hex(0x3A475C), hex(0x3B82F6), hex(0xF59E0B)
	green, greenTrack := hex(0x22C55E), hex(0x2A3546)

	for ind := range 7 {
		x0 := left + float64(ind)*(cell+gap)
		x1 := x0 + cell
		switch {
		case ind < 2:
			shapes = append(shapes, solid(x0, cellTop, x1, cellBottom, 20, spent))
			shapes = append(shapes, solid(x0, lineTop, x1, lineBottom, 14, greenTrack))
		case ind == 2:
			// Spent from the left, an orange overspend stretch, blue what is left.
			// The orange stretch goes on top of blue's rounded left end, so the cell
			// reads as one piece.
			shapes = append(shapes, solid(x0, cellTop, x1, cellBottom, 20, spent))
			shapes = append(shapes, solid(x0+cell*0.45, cellTop, x1, cellBottom, 20, blue))
			shapes = append(shapes, solid(x0+cell*0.25, cellTop, x0+cell*0.55, cellBottom, 0, orange))
			shapes = append(shapes, solid(x0, lineTop, x1, lineBottom, 14, greenTrack))
			shapes = append(shapes, solid(x0+cell*0.35, lineTop, x1, lineBottom, 14, green))
		default:
			shapes = append(shapes, solid(x0, cellTop, x1, cellBottom, 20, blue))
			shapes = append(shapes, solid(x0, lineTop, x1, lineBottom, 14, green))
		}
	}

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := range size {
		for px := range size {
			// Pixel center in 1024 units; one pixel is 1/scale units wide.
			x := (float64(px) + 0.5) / scale
			y := (float64(py) + 0.5) / scale
			var acc rgba
			for _, sh := range shapes {
				cover := coverage(sh, x, y, scale)
				if cover <= 0 {
					continue
				}
				t := (y - sh.y0) / (sh.y1 - sh.y0)
				col := mix(sh.top, sh.bottom, math.Min(1, math.Max(0, t)))
				acc = over(acc, col, cover)
			}
			img.SetNRGBA(px, py, toNRGBA(acc))
		}
	}
	return img
}

func solid(x0, y0, x1, y1, radius float64, col rgba) shape {
	return shape{x0, y0, x1, y1, radius, col, col}
}

// Share of the pixel inside the shape: from the signed distance to its edge, in pixels.
func coverage(sh shape, x, y, scale float64) float64 {
	cx, cy := (sh.x0+sh.x1)/2, (sh.y0+sh.y1)/2
	hx, hy := (sh.x1-sh.x0)/2-sh.radius, (sh.y1-sh.y0)/2-sh.radius
	dx := math.Abs(x-cx) - hx
	dy := math.Abs(y-cy) - hy
	outside := math.Hypot(math.Max(dx, 0), math.Max(dy, 0))
	inside := math.Min(math.Max(dx, dy), 0)
	dist := (outside + inside - sh.radius) * scale
	return math.Min(1, math.Max(0, 0.5-dist))
}

func mix(one, two rgba, t float64) rgba {
	return rgba{one.r + (two.r-one.r)*t, one.g + (two.g-one.g)*t, one.b + (two.b-one.b)*t, 1}
}

// `top` with coverage `cover` over `base`, premultiplied inside.
func over(base, top rgba, cover float64) rgba {
	a := cover + base.a*(1-cover)
	if a == 0 {
		return rgba{}
	}
	return rgba{
		(top.r*cover + base.r*base.a*(1-cover)) / a,
		(top.g*cover + base.g*base.a*(1-cover)) / a,
		(top.b*cover + base.b*base.a*(1-cover)) / a,
		a,
	}
}

func toNRGBA(col rgba) color.NRGBA {
	conv := func(val float64) uint8 { return uint8(math.Round(math.Min(1, math.Max(0, val)) * 255)) }
	return color.NRGBA{conv(col.r), conv(col.g), conv(col.b), conv(col.a)}
}

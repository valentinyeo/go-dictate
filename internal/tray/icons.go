package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// getIconData generates a multi-size ICO with a microphone icon.
func getIconData(colorName string) []byte {
	light := isLightTaskbar()
	var col color.RGBA
	var style string

	switch colorName {
	case "blue": // Speaking
		col = color.RGBA{0x10, 0xB9, 0x81, 0xFF}
		style = "filled"
	case "red": // Paused
		col = color.RGBA{0xEF, 0x44, 0x44, 0xFF}
		style = "slash"
	case "media": // Media detected
		col = color.RGBA{0xF5, 0x9E, 0x0B, 0xFF}
		style = "outline"
	default: // Standby / Active (grey)
		if light {
			col = color.RGBA{0x20, 0x20, 0x20, 0xFF} // Near-black for light taskbar
		} else {
			col = color.RGBA{0xD0, 0xD0, 0xD0, 0xFF} // Light grey for dark taskbar
		}
		style = "outline"
	}

	// Render at multiple sizes for crisp display at any DPI
	sizes := []int{16, 20, 24, 32, 48}
	images := make([]*image.RGBA, len(sizes))
	for i, sz := range sizes {
		images[i] = renderMic(sz, style, col)
	}
	return buildICO(images)
}

// isLightTaskbar checks Windows registry for light taskbar theme.
func isLightTaskbar() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	val, _, err := k.GetIntegerValue("SystemUsesLightTheme")
	if err != nil {
		return false
	}
	return val == 1
}

// --- SDF-based microphone icon rendering ---
//
// All geometry is in normalized [0,1] coordinates for resolution independence.
// Signed distance fields give smooth anti-aliased edges at any size.

func renderMic(size int, style string, col color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	s := float64(size)
	aa := 1.5 / s

	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px := (float64(x) + 0.5) / s
			py := (float64(y) + 0.5) / s

			a := micAlpha(px, py, style, aa)
			if a > 0.003 {
				img.SetRGBA(x, y, color.RGBA{
					R: col.R, G: col.G, B: col.B,
					A: uint8(clamp01(a) * 255),
				})
			}
		}
	}
	return img
}

func micAlpha(px, py float64, style string, aa float64) float64 {
	// Mic capsule
	capD := stadiumSDF(px, py, 0.50, 0.28, 0.125, 0.19)

	var a float64
	const t = 0.032 // outline thickness

	// Capsule body
	if style == "filled" {
		a = fill(capD, aa)
	} else {
		a = stroke(capD, t, aa)
	}

	// U-shaped holder (outline of larger stadium, clipped to bottom)
	holdD := stadiumSDF(px, py, 0.50, 0.38, 0.185, 0.28)
	hA := stroke(holdD, t, aa)
	hA *= fill(-(py - 0.33), aa)                   // only below y=0.33
	hA *= clamp01(0.5 + (capD-t*0.8)/aa)           // hide inside capsule
	a = math.Max(a, hA)

	// Stand (vertical)
	stD := boxSDF(px, py, 0.50, 0.73, 0.028, 0.07)
	a = math.Max(a, fill(stD, aa))

	// Base (horizontal)
	bD := boxSDF(px, py, 0.50, 0.82, 0.15, 0.028)
	a = math.Max(a, fill(bD, aa))

	// Slash for paused
	if style == "slash" {
		slD := segSDF(px, py, 0.15, 0.80, 0.85, 0.06, 0.038)
		a = math.Max(a, fill(slD, aa))
	}

	return clamp01(a)
}

// --- SDF primitives ---

// stadiumSDF: vertical capsule (rectangle with semicircular caps).
func stadiumSDF(px, py, cx, cy, hw, hh float64) float64 {
	// Clamp to straight section of capsule
	nearY := math.Max(cy-hh+hw, math.Min(py, cy+hh-hw))
	dx := px - cx
	dy := py - nearY
	return math.Sqrt(dx*dx+dy*dy) - hw
}

// boxSDF: axis-aligned rectangle.
func boxSDF(px, py, cx, cy, hw, hh float64) float64 {
	dx := math.Abs(px-cx) - hw
	dy := math.Abs(py-cy) - hh
	outside := math.Sqrt(math.Max(dx, 0)*math.Max(dx, 0) + math.Max(dy, 0)*math.Max(dy, 0))
	inside := math.Min(math.Max(dx, dy), 0)
	return -(outside + inside) // negate: negative outside, positive inside
}

// segSDF: line segment with half-thickness.
func segSDF(px, py, x1, y1, x2, y2, ht float64) float64 {
	dx, dy := x2-x1, y2-y1
	t := math.Max(0, math.Min(((px-x1)*dx+(py-y1)*dy)/(dx*dx+dy*dy), 1))
	ex := px - (x1 + t*dx)
	ey := py - (y1 + t*dy)
	return -(math.Sqrt(ex*ex+ey*ey) - ht) // negate: negative outside, positive inside
}

// fill: alpha for filled shape. Positive SDF = inside.
func fill(d, aa float64) float64 {
	return clamp01(0.5 + d/aa)
}

// stroke: alpha for outline of shape at distance d with thickness t.
func stroke(d, t, aa float64) float64 {
	return fill(-(math.Abs(d) - t), aa)
}

func clamp01(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// --- Multi-size ICO builder ---

func buildICO(images []*image.RGBA) []byte {
	n := len(images)
	bmps := make([][]byte, n)
	for i, img := range images {
		bmps[i] = bmpData(img)
	}

	var buf bytes.Buffer
	// Header
	binary.Write(&buf, binary.LittleEndian, [3]uint16{0, 1, uint16(n)})

	// Directory entries
	off := 6 + n*16
	for i, img := range images {
		w := img.Bounds().Dx()
		h := img.Bounds().Dy()
		bw, bh := byte(w), byte(h)
		if w >= 256 {
			bw = 0
		}
		if h >= 256 {
			bh = 0
		}
		buf.Write([]byte{bw, bh, 0, 0})
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(32))
		binary.Write(&buf, binary.LittleEndian, uint32(len(bmps[i])))
		binary.Write(&buf, binary.LittleEndian, uint32(off))
		off += len(bmps[i])
	}

	for _, b := range bmps {
		buf.Write(b)
	}
	return buf.Bytes()
}

func getExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

func bmpData(img *image.RGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	var buf bytes.Buffer
	// BITMAPINFOHEADER
	binary.Write(&buf, binary.LittleEndian, uint32(40))
	binary.Write(&buf, binary.LittleEndian, int32(w))
	binary.Write(&buf, binary.LittleEndian, int32(h*2))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	buf.Write(make([]byte, 24)) // rest of header zeros

	// Pixels (BGRA, bottom-up)
	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}

	// AND mask
	rowBytes := ((w + 31) / 32) * 4
	buf.Write(make([]byte, rowBytes*h))

	return buf.Bytes()
}

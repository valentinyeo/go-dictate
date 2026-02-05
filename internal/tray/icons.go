package tray

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

func getIconData(colorName string) []byte {
	var iconFile string
	var tintColor color.RGBA

	switch colorName {
	case "blue":
		iconFile = "mic-on.svg" // Filled mic - Speaking
		tintColor = color.RGBA{0x10, 0xB9, 0x81, 0xFF} // Green
	case "red":
		iconFile = "mic-off.svg" // Prohibited mic - Paused
		tintColor = color.RGBA{0xEF, 0x44, 0x44, 0xFF} // Red
	default: // grey
		iconFile = "mic-standby.svg" // Outline mic - Standby
		tintColor = color.RGBA{0x9C, 0xA3, 0xAF, 0xFF} // Grey
	}

	// Try to load from multiple paths
	possiblePaths := []string{
		filepath.Join("icons", iconFile),
		filepath.Join("..", "..", "icons", iconFile),
		filepath.Join(getExeDir(), "icons", iconFile),
		filepath.Join(getExeDir(), "..", "icons", iconFile),
	}

	for _, path := range possiblePaths {
		if data := loadSVGIcon(path, tintColor); data != nil {
			return data
		}
	}

	// Fallback
	return createFallbackIcon(tintColor)
}

func loadSVGIcon(path string, tintColor color.RGBA) []byte {
	// Read SVG file
	svgData, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	// Parse SVG
	icon, err := oksvg.ReadIconStream(bytes.NewReader(svgData))
	if err != nil {
		return nil
	}

	// Create 16x16 image
	w, h := 16, 16
	icon.SetTarget(0, 0, float64(w), float64(h))

	// Create RGBA image
	img := image.NewRGBA(image.Rect(0, 0, w, h))

	// Render SVG
	scanner := rasterx.NewScannerGV(w, h, img, img.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)
	icon.Draw(raster, 1.0)

	// Apply color tint (replace black with tint color)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			// If pixel has color (not transparent)
			if c.A > 0 {
				// Replace with tint color
				img.SetRGBA(x, y, tintColor)
			}
		}
	}

	// Convert to ICO
	return convertToICO(img)
}

func convertToICO(img *image.RGBA) []byte {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width > 256 || height > 256 {
		return nil
	}

	var buf bytes.Buffer

	// ICO header
	buf.Write([]byte{0x00, 0x00, 0x01, 0x00, 0x01, 0x00})

	// Image directory
	buf.WriteByte(byte(width))
	buf.WriteByte(byte(height))
	buf.Write([]byte{0x00, 0x00, 0x01, 0x00, 0x20, 0x00})

	sizeOffset := buf.Len()
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00, 0x16, 0x00, 0x00, 0x00})

	// BMP header
	bmpStart := buf.Len()
	buf.Write([]byte{0x28, 0x00, 0x00, 0x00})
	buf.WriteByte(byte(width))
	buf.Write([]byte{0x00, 0x00, 0x00})
	buf.WriteByte(byte(height * 2))
	buf.Write([]byte{0x00, 0x00, 0x00, 0x01, 0x00, 0x20, 0x00})
	buf.Write(make([]byte, 20)) // Zeros for compression, size, etc.

	// Pixel data (bottom-up)
	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			c := img.RGBAAt(x, y)
			buf.WriteByte(c.B)
			buf.WriteByte(c.G)
			buf.WriteByte(c.R)
			buf.WriteByte(c.A)
		}
	}

	// AND mask
	rowSize := ((width + 31) / 32) * 4
	buf.Write(make([]byte, rowSize*height))

	// Update size
	imageSize := buf.Len() - bmpStart
	result := buf.Bytes()
	result[sizeOffset] = byte(imageSize)
	result[sizeOffset+1] = byte(imageSize >> 8)
	result[sizeOffset+2] = byte(imageSize >> 16)
	result[sizeOffset+3] = byte(imageSize >> 24)

	return result
}

func createFallbackIcon(c color.RGBA) []byte {
	size := 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Simple mic
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if (y >= 2 && y <= 7 && x >= 6 && x <= 9) ||
				(y >= 8 && y <= 10 && x >= 7 && x <= 8) {
				img.SetRGBA(x, y, c)
			}
		}
	}

	return convertToICO(img)
}

func getExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

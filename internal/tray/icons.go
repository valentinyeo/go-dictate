package tray

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
)

func getIconData(colorName string) []byte {
	var iconFile string
	var tintColor color.RGBA

	switch colorName {
	case "blue":
		iconFile = "mic-on.png" // Filled mic - Speaking/dictation active
		tintColor = color.RGBA{0x10, 0xB9, 0x81, 0xFF} // #10B981 Green
	case "red":
		iconFile = "mic-off.png" // Prohibited mic - Paused/muted
		tintColor = color.RGBA{0xEF, 0x44, 0x44, 0xFF} // #EF4444 Red
	default: // grey
		iconFile = "mic-standby.png" // Outline mic - Standby/waiting
		tintColor = color.RGBA{0x9C, 0xA3, 0xAF, 0xFF} // #9CA3AF Grey
	}

	// Try to load icon from multiple possible locations
	possiblePaths := []string{
		filepath.Join("icons", iconFile),
		filepath.Join("..", "..", "icons", iconFile),
		filepath.Join(getExeDir(), "icons", iconFile),
		filepath.Join(getExeDir(), "..", "icons", iconFile),
	}

	for _, path := range possiblePaths {
		if data := loadAndConvertIcon(path, tintColor); data != nil {
			return data
		}
	}

	// Fallback to programmatic icon if files not found
	return createFallbackIcon(tintColor)
}

func loadAndConvertIcon(path string, tintColor color.RGBA) []byte {
	// Read PNG file
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	// Decode image
	img, err := png.Decode(file)
	if err != nil {
		return nil
	}

	// Convert to RGBA
	bounds := img.Bounds()
	rgba := image.NewRGBA(bounds)
	draw.Draw(rgba, bounds, img, bounds.Min, draw.Src)

	// Invert and tint: black pixels → tinted color, white pixels → transparent
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			oldColor := rgba.RGBAAt(x, y)

			// Calculate brightness (0 = black, 255 = white)
			brightness := (uint32(oldColor.R) + uint32(oldColor.G) + uint32(oldColor.B)) / 3

			if brightness < 128 {
				// Dark pixel → apply tint color with full opacity
				rgba.SetRGBA(x, y, tintColor)
			} else {
				// Light pixel → make transparent
				rgba.SetRGBA(x, y, color.RGBA{0, 0, 0, 0})
			}
		}
	}

	// Convert to ICO format
	return convertToICO(rgba)
}

func convertToICO(img *image.RGBA) []byte {
	bounds := img.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()

	if width > 256 || height > 256 {
		return nil // ICO format limitation
	}

	var buf bytes.Buffer

	// ICO header
	buf.Write([]byte{0x00, 0x00}) // Reserved
	buf.Write([]byte{0x01, 0x00}) // Type: ICO
	buf.Write([]byte{0x01, 0x00}) // Number of images

	// Image directory entry
	buf.WriteByte(byte(width))  // Width
	buf.WriteByte(byte(height)) // Height
	buf.WriteByte(0x00)         // Color palette
	buf.WriteByte(0x00)         // Reserved
	buf.Write([]byte{0x01, 0x00}) // Color planes
	buf.Write([]byte{0x20, 0x00}) // Bits per pixel (32-bit RGBA)

	// Placeholder for image size (will update later)
	sizeOffset := buf.Len()
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00})

	// Offset to image data
	buf.Write([]byte{0x16, 0x00, 0x00, 0x00}) // 22 bytes (6 + 16)

	// BMP header
	bmpStart := buf.Len()
	buf.Write([]byte{0x28, 0x00, 0x00, 0x00}) // Header size (40 bytes)

	// Width
	buf.WriteByte(byte(width))
	buf.Write([]byte{0x00, 0x00, 0x00})

	// Height (doubled for AND mask)
	buf.WriteByte(byte(height * 2))
	buf.Write([]byte{0x00, 0x00, 0x00})

	buf.Write([]byte{0x01, 0x00}) // Planes
	buf.Write([]byte{0x20, 0x00}) // Bits per pixel
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // Compression
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // Image size
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // X pixels per meter
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // Y pixels per meter
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // Colors used
	buf.Write([]byte{0x00, 0x00, 0x00, 0x00}) // Important colors

	// Pixel data (bottom-up BMP format)
	for y := height - 1; y >= 0; y-- {
		for x := 0; x < width; x++ {
			c := img.RGBAAt(x, y)
			buf.WriteByte(c.B) // Blue
			buf.WriteByte(c.G) // Green
			buf.WriteByte(c.R) // Red
			buf.WriteByte(c.A) // Alpha
		}
	}

	// AND mask (all zeros for full alpha support)
	rowSize := ((width + 31) / 32) * 4
	for y := 0; y < height; y++ {
		for x := 0; x < rowSize; x++ {
			buf.WriteByte(0x00)
		}
	}

	// Update image size in directory entry
	imageSize := buf.Len() - bmpStart
	result := buf.Bytes()
	result[sizeOffset] = byte(imageSize & 0xFF)
	result[sizeOffset+1] = byte((imageSize >> 8) & 0xFF)
	result[sizeOffset+2] = byte((imageSize >> 16) & 0xFF)
	result[sizeOffset+3] = byte((imageSize >> 24) & 0xFF)

	return result
}

func createFallbackIcon(c color.RGBA) []byte {
	// Simple 16x16 fallback icon
	size := 16
	img := image.NewRGBA(image.Rect(0, 0, size, size))

	// Draw a simple mic shape
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			if (y >= 2 && y <= 7 && x >= 6 && x <= 9) || // Capsule
			   (y >= 8 && y <= 10 && x >= 7 && x <= 8) { // Stand
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

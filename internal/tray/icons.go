package tray

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
	"golang.org/x/sys/windows/registry"
)

func getIconData(colorName string) []byte {
	light := isLightTaskbar()
	var iconFile string
	var tintColor color.RGBA

	switch colorName {
	case "blue":
		iconFile = "mic-on.png"
		tintColor = color.RGBA{0x10, 0xB9, 0x81, 0xFF}
	case "red":
		iconFile = "mic-off.png"
		tintColor = color.RGBA{0xEF, 0x44, 0x44, 0xFF}
	case "media":
		iconFile = "mic-on.png"
		tintColor = color.RGBA{0xF5, 0x9E, 0x0B, 0xFF}
	default: // standby
		iconFile = "mic-on.png"
		if light {
			tintColor = color.RGBA{0x20, 0x20, 0x20, 0xFF}
		} else {
			tintColor = color.RGBA{0xD0, 0xD0, 0xD0, 0xFF}
		}
	}

	possiblePaths := []string{
		filepath.Join("icons", iconFile),
		filepath.Join("..", "..", "icons", iconFile),
		filepath.Join(getExeDir(), "icons", iconFile),
		filepath.Join(getExeDir(), "..", "icons", iconFile),
	}

	for _, path := range possiblePaths {
		if data := loadAndBuildICO(path, tintColor); data != nil {
			return data
		}
	}

	return createFallbackIcon(tintColor)
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

// loadAndBuildICO loads a PNG, scales to multiple sizes, tints, and builds a multi-size ICO.
func loadAndBuildICO(path string, tintColor color.RGBA) []byte {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()

	srcImg, err := png.Decode(file)
	if err != nil {
		return nil
	}

	// Generate multiple sizes for crisp display at any DPI
	sizes := []int{16, 20, 24, 32, 48}
	images := make([]*image.RGBA, len(sizes))

	for i, sz := range sizes {
		scaled := image.NewRGBA(image.Rect(0, 0, sz, sz))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), srcImg, srcImg.Bounds(), draw.Over, nil)

		// Tint: replace color, preserve alpha
		for y := 0; y < sz; y++ {
			for x := 0; x < sz; x++ {
				c := scaled.RGBAAt(x, y)
				if c.A > 0 {
					scaled.SetRGBA(x, y, color.RGBA{
						R: tintColor.R,
						G: tintColor.G,
						B: tintColor.B,
						A: c.A,
					})
				}
			}
		}
		images[i] = scaled
	}

	return buildMultiSizeICO(images)
}

// buildMultiSizeICO creates an ICO file with multiple image sizes.
func buildMultiSizeICO(images []*image.RGBA) []byte {
	n := len(images)
	bmps := make([][]byte, n)
	for i, img := range images {
		bmps[i] = imageToBMP(img)
	}

	var buf bytes.Buffer

	// ICO header (6 bytes)
	binary.Write(&buf, binary.LittleEndian, uint16(0)) // reserved
	binary.Write(&buf, binary.LittleEndian, uint16(1)) // type: ICO
	binary.Write(&buf, binary.LittleEndian, uint16(n)) // count

	// Directory entries (16 bytes each)
	dataOffset := 6 + n*16
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
		buf.Write([]byte{bw, bh, 0, 0})                               // width, height, palette, reserved
		binary.Write(&buf, binary.LittleEndian, uint16(1))             // planes
		binary.Write(&buf, binary.LittleEndian, uint16(32))            // bpp
		binary.Write(&buf, binary.LittleEndian, uint32(len(bmps[i])))  // data size
		binary.Write(&buf, binary.LittleEndian, uint32(dataOffset))    // data offset
		dataOffset += len(bmps[i])
	}

	for _, b := range bmps {
		buf.Write(b)
	}
	return buf.Bytes()
}

// imageToBMP converts an RGBA image to ICO-compatible BMP data.
func imageToBMP(img *image.RGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	var buf bytes.Buffer

	// BITMAPINFOHEADER (40 bytes)
	binary.Write(&buf, binary.LittleEndian, uint32(40))  // header size
	binary.Write(&buf, binary.LittleEndian, int32(w))     // width
	binary.Write(&buf, binary.LittleEndian, int32(h*2))   // height (doubled for ICO)
	binary.Write(&buf, binary.LittleEndian, uint16(1))    // planes
	binary.Write(&buf, binary.LittleEndian, uint16(32))   // bpp
	buf.Write(make([]byte, 24))                           // rest zeros

	// Pixel data (BGRA, bottom-up)
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

func createFallbackIcon(c color.RGBA) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for y := 0; y < 16; y++ {
		for x := 0; x < 16; x++ {
			if (y >= 2 && y <= 7 && x >= 6 && x <= 9) ||
				(y >= 8 && y <= 10 && x >= 7 && x <= 8) {
				img.SetRGBA(x, y, c)
			}
		}
	}
	return buildMultiSizeICO([]*image.RGBA{img})
}

func getExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

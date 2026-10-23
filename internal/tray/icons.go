package tray

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/image/draw"
	"golang.org/x/sys/windows/registry"
)

var (
	advapi32                    = syscall.NewLazyDLL("advapi32.dll")
	procRegNotifyChangeKeyValue = advapi32.NewProc("RegNotifyChangeKeyValue")
)

const regNotifyChangeLastSet = 0x00000004

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
	default:
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

func watchThemeChanges(ctx context.Context, changed chan<- struct{}) {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\CurrentVersion\Themes\Personalize`,
		registry.QUERY_VALUE|registry.NOTIFY)
	if err != nil {
		return
	}
	defer k.Close()

	for {
		ret, _, _ := procRegNotifyChangeKeyValue.Call(
			uintptr(k),
			0,
			regNotifyChangeLastSet,
			0,
			0,
		)
		if ret != 0 {
			return
		}

		select {
		case <-ctx.Done():
			return
		default:
		}

		select {
		case changed <- struct{}{}:
		default:
		}
	}
}

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

	sizes := []int{16, 20, 24, 32, 48}
	images := make([]*image.RGBA, len(sizes))

	for i, sz := range sizes {
		scaled := image.NewRGBA(image.Rect(0, 0, sz, sz))
		draw.CatmullRom.Scale(scaled, scaled.Bounds(), srcImg, srcImg.Bounds(), draw.Over, nil)

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

func buildMultiSizeICO(images []*image.RGBA) []byte {
	n := len(images)
	bmps := make([][]byte, n)
	for i, img := range images {
		bmps[i] = imageToBMP(img)
	}

	var buf bytes.Buffer

	binary.Write(&buf, binary.LittleEndian, uint16(0))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(n))

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
		buf.Write([]byte{bw, bh, 0, 0})
		binary.Write(&buf, binary.LittleEndian, uint16(1))
		binary.Write(&buf, binary.LittleEndian, uint16(32))
		binary.Write(&buf, binary.LittleEndian, uint32(len(bmps[i])))
		binary.Write(&buf, binary.LittleEndian, uint32(dataOffset))
		dataOffset += len(bmps[i])
	}

	for _, b := range bmps {
		buf.Write(b)
	}
	return buf.Bytes()
}

func imageToBMP(img *image.RGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()

	var buf bytes.Buffer

	binary.Write(&buf, binary.LittleEndian, uint32(40))
	binary.Write(&buf, binary.LittleEndian, int32(w))
	binary.Write(&buf, binary.LittleEndian, int32(h*2))
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	buf.Write(make([]byte, 24))

	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.RGBAAt(x, y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}

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

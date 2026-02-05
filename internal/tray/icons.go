package tray

func getIconData(color string) []byte {
	switch color {
	case "blue":
		return createMicIcon(0x10, 0xB9, 0x81, "speaking") // #10B981 Green (actively transcribing)
	case "red":
		return createMicIcon(0xEF, 0x44, 0x44, "paused") // #EF4444 Red (manually paused)
	default: // grey
		return createMicIcon(0x9C, 0xA3, 0xAF, "standby") // #9CA3AF Grey (waiting for speech)
	}
}

// Create a 16x16 ICO with a microphone icon matching the Fluent UI PNG designs
func createMicIcon(r, g, b byte, style string) []byte {
	const size = 16
	const headerSize = 22

	// ICO file header
	icon := []byte{
		0x00, 0x00, // Reserved
		0x01, 0x00, // Type: ICO
		0x01, 0x00, // Number of images: 1

		// Image directory entry
		byte(size), // Width
		byte(size), // Height
		0x00,       // Color palette (0 = no palette)
		0x00,       // Reserved
		0x01, 0x00, // Color planes
		0x20, 0x00, // Bits per pixel (32-bit RGBA)
		0x00, 0x00, 0x00, 0x00, // Size of image data (filled below)
		byte(headerSize), 0x00, 0x00, 0x00, // Offset to image data
	}

	// BMP header
	bmpHeader := []byte{
		0x28, 0x00, 0x00, 0x00, // Header size (40 bytes)
		byte(size), 0x00, 0x00, 0x00, // Width
		byte(size * 2), 0x00, 0x00, 0x00, // Height (double for AND mask)
		0x01, 0x00, // Color planes
		0x20, 0x00, // Bits per pixel
		0x00, 0x00, 0x00, 0x00, // Compression (none)
		0x00, 0x00, 0x00, 0x00, // Image size (0 for uncompressed)
		0x00, 0x00, 0x00, 0x00, // X pixels per meter
		0x00, 0x00, 0x00, 0x00, // Y pixels per meter
		0x00, 0x00, 0x00, 0x00, // Colors used
		0x00, 0x00, 0x00, 0x00, // Important colors
	}

	icon = append(icon, bmpHeader...)

	// Create pixel data (16x16 RGBA) - Bottom-up BMP format
	for y := size - 1; y >= 0; y-- {
		for x := 0; x < size; x++ {
			var pixel [4]byte
			isMic := false

			// Simple microphone shape matching Fluent UI style
			// Mic capsule (rounded rectangle)
			if y >= 2 && y <= 7 && x >= 6 && x <= 9 {
				isMic = true
			}
			// Rounded top
			if y == 1 && x >= 7 && x <= 8 {
				isMic = true
			}
			// Side curves
			if (y >= 3 && y <= 6) && (x == 5 || x == 10) {
				isMic = true
			}

			// Mic stand (vertical line)
			if y >= 8 && y <= 11 && x >= 7 && x <= 8 {
				isMic = true
			}

			// Stand arc (U-shape at bottom)
			if y == 9 && x >= 4 && x <= 11 {
				isMic = true
			}
			if y == 8 && (x >= 4 && x <= 5 || x >= 10 && x <= 11) {
				isMic = true
			}

			// Base line
			if y == 13 && x >= 6 && x <= 9 {
				isMic = true
			}

			// Add overlays based on style
			isOverlay := false

			if style == "paused" {
				// Diagonal slash for paused
				if (x == y || x == y+1 || x == y-1) {
					isOverlay = true
				}
			} else if style == "standby" {
				// Small mute circle in bottom-right
				dx := float64(x) - 11.5
				dy := float64(y) - 11.5
				distSq := dx*dx + dy*dy
				// Circle outline
				if distSq >= 3 && distSq <= 5 {
					isOverlay = true
				}
				// Slash through circle
				if distSq <= 4 && ((x-y >= -2 && x-y <= 0)) {
					isOverlay = true
				}
			}

			if isOverlay || isMic {
				// Draw in specified color (BGRA format for BMP)
				pixel = [4]byte{b, g, r, 0xFF}
			} else {
				// Transparent background
				pixel = [4]byte{0x00, 0x00, 0x00, 0x00}
			}

			icon = append(icon, pixel[:]...)
		}
	}

	// AND mask (all zeros for full alpha channel transparency)
	rowSize := (size + 31) / 32 * 4
	for y := 0; y < size; y++ {
		for x := 0; x < rowSize; x++ {
			icon = append(icon, 0x00)
		}
	}

	// Update size in header
	imageSize := len(icon) - headerSize
	icon[14] = byte(imageSize & 0xFF)
	icon[15] = byte((imageSize >> 8) & 0xFF)
	icon[16] = byte((imageSize >> 16) & 0xFF)
	icon[17] = byte((imageSize >> 24) & 0xFF)

	return icon
}

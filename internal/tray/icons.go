package tray

import (
	"os"
	"path/filepath"
)

func getIconData(color string) []byte {
	var iconName string
	switch color {
	case "blue":
		iconName = "mic-on.png" // Speaking (actively transcribing)
	case "red":
		iconName = "mic-off.png" // Paused (manually paused)
	default: // grey
		iconName = "mic-standby.png" // Standby (waiting for speech)
	}

	// Try to load icon from multiple possible locations
	possiblePaths := []string{
		filepath.Join("icons", iconName),                    // Running from project root
		filepath.Join("..", "..", "icons", iconName),        // Running from internal/tray
		filepath.Join(getExeDir(), "icons", iconName),       // Icons next to exe
		filepath.Join(getExeDir(), "..", "icons", iconName), // Icons in parent of exe dir
	}

	for _, path := range possiblePaths {
		if data, err := os.ReadFile(path); err == nil {
			return data
		}
	}

	// If no icon found, return empty byte slice (will show no icon)
	return []byte{}
}

func getExeDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	return filepath.Dir(exe)
}

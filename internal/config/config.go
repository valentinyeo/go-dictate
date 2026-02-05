package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Deepgram DeepgramConfig `toml:"deepgram"`
	Audio    AudioConfig    `toml:"audio"`
	VAD      VADConfig      `toml:"vad"`
}

type DeepgramConfig struct {
	APIKey       string   `toml:"api_key"`
	Language     string   `toml:"language"`
	Model        string   `toml:"model"`
	Punctuate    bool     `toml:"punctuate"`
	FillerWords  bool     `toml:"filler_words"`
	Keywords     []string `toml:"keywords"`
}

type AudioConfig struct {
	SilenceThresholdMs uint64  `toml:"silence_threshold_ms"`
	DuckVolume         float32 `toml:"duck_volume"`
	AutoPauseOnMedia   bool    `toml:"auto_pause_on_media"`
}

type VADConfig struct {
	EnergyThreshold float32 `toml:"energy_threshold"`
}

func Default() Config {
	return Config{
		Deepgram: DeepgramConfig{
			APIKey:      "",
			Language:    "en",
			Model:       "nova-3",
			Punctuate:   true,
			FillerWords: true,
			Keywords:    []string{},
		},
		Audio: AudioConfig{
			SilenceThresholdMs: 3000,
			DuckVolume:         0.2,
			AutoPauseOnMedia:   true,
		},
		VAD: VADConfig{
			EnergyThreshold: 0.02,
		},
	}
}

func LoadOrCreate() (*Config, error) {
	configPath, err := GetConfigPath()
	if err != nil {
		return nil, fmt.Errorf("failed to get config path: %w", err)
	}

	// Check if config exists
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create directory if it doesn't exist
		if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create config directory: %w", err)
		}

		// Create default config
		defaultCfg := Default()
		if err := defaultCfg.Save(); err != nil {
			return nil, fmt.Errorf("failed to save default config: %w", err)
		}
		return &defaultCfg, nil
	}

	// Load existing config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	var cfg Config
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) Save() error {
	configPath, err := GetConfigPath()
	if err != nil {
		return fmt.Errorf("failed to get config path: %w", err)
	}

	data, err := toml.Marshal(c)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Add comments
	commented := fmt.Sprintf(`# Dictation App Configuration
# Edit this file to configure the application
# Get your Deepgram API key from: https://console.deepgram.com/

%s

# Deepgram settings
# model: nova-3 (latest), nova-2 (previous version)
# language: en (English), de (German) - toggle with Ctrl+Alt+Shift+L
# punctuate: Enable automatic punctuation and capitalization (true/false)
# filler_words: Remove "um", "uh", "like", etc. (true/false)
# keywords: List of words to boost recognition (e.g., ["GitHub", "API", "SQL"])

# Audio settings
# silence_threshold_ms: Auto-pause after this many milliseconds of silence
# duck_volume: Volume level (0.0-1.0) for other apps during dictation

# VAD settings
# energy_threshold: RMS threshold for speech detection (0.0-1.0)

# Global Hotkeys
# Ctrl+Alt+Shift+L: Toggle language (EN ↔ DE)
`, string(data))

	if err := os.WriteFile(configPath, []byte(commented), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (c *Config) Validate() error {
	if c.Deepgram.APIKey == "" {
		return fmt.Errorf("API key not set. Please edit config.toml and add your Deepgram API key")
	}
	return nil
}

func GetConfigPath() (string, error) {
	appData := os.Getenv("APPDATA")
	if appData == "" {
		return "", fmt.Errorf("APPDATA environment variable not set")
	}
	return filepath.Join(appData, "dictation", "config.toml"), nil
}

func OpenInEditor() error {
	configPath, err := GetConfigPath()
	if err != nil {
		return err
	}

	// Windows: use cmd.exe to open with default text editor
	proc := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
	}
	_, err = os.StartProcess("C:\\Windows\\System32\\cmd.exe", []string{"/C", "start", "", configPath}, proc)
	return err
}

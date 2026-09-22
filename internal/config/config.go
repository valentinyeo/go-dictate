package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pelletier/go-toml/v2"
)

type Config struct {
	Profile     string            `toml:"profile"`
	Deepgram    DeepgramConfig    `toml:"deepgram"`
	Audio       AudioConfig       `toml:"audio"`
	VAD         VADConfig         `toml:"vad"`
	Diagnostics DiagnosticsConfig `toml:"diagnostics"`
	Keyboard    KeyboardConfig    `toml:"keyboard"`
}

const (
	ProfileTerminalAgent = "terminal_agent"
	ProfileEmailWriting  = "email_writing"
	ProfileCustom        = "custom"

	NoiseModeNormal = "normal"
	NoiseModeTrain  = "train"
)

type DeepgramConfig struct {
	APIKey      string   `toml:"api_key"`
	Language    string   `toml:"language"`
	Model       string   `toml:"model"`
	Punctuate   bool     `toml:"punctuate"`
	SmartFormat bool     `toml:"smart_format"`
	FillerWords bool     `toml:"filler_words"`
	Holdback    bool     `toml:"holdback"`
	Keyterms    []string `toml:"keyterms"`
}

type AudioConfig struct {
	PushToTalk         bool    `toml:"push_to_talk"`
	SilenceThresholdMs uint64  `toml:"silence_threshold_ms"`
	DuckVolume         float32 `toml:"duck_volume"`
	DuckEnabled        bool    `toml:"duck_enabled"`
	AutoPauseOnMedia   bool    `toml:"auto_pause_on_media"`
	DeviceName         string  `toml:"device_name"`
	ZeroLatency        bool    `toml:"zero_latency"`
	BatteryBudgetSaver bool    `toml:"battery_budget_saver"`
}

type VADConfig struct {
	EnergyThreshold   float32 `toml:"energy_threshold"`
	AdaptiveNoise     bool    `toml:"adaptive_noise"`
	NoiseMultiplier   float32 `toml:"noise_multiplier"`
	MinStartThreshold float32 `toml:"min_start_threshold"`
	SpeechStartChunks int     `toml:"speech_start_chunks"`
	NoiseMode         string  `toml:"noise_mode"`
}

type DiagnosticsConfig struct {
	SaveFailedAudio bool `toml:"save_failed_audio"`
}

type KeyboardConfig struct {
	// Injection method for transcribed text:
	//   "paste" (default) - put text on the clipboard and send Ctrl+V. The
	//     terminal emulator converts the paste to plain input bytes, so it
	//     works identically in cmd.exe, Windows Terminal, and tmux/SSH inside
	//     them. Immune to the dropped-space bug: terminals (ConPTY) silently
	//     drop space key-events from injected keystroke batches.
	//   "type" - legacy per-character SendInput key events. Kept as an escape
	//     hatch for targets where Ctrl+V does not paste.
	// Paste automatically falls back to typing if the clipboard is unavailable.
	Injection string `toml:"injection"`
}

func Default() Config {
	return Config{
		Profile: ProfileTerminalAgent,
		Deepgram: DeepgramConfig{
			APIKey:      "",
			Language:    "en",
			Model:       "nova-3",
			Punctuate:   true,
			SmartFormat: false,
			FillerWords: false,
			Holdback:    true,
			Keyterms:    []string{"Claude Code", "exe", "GitHub", "Deepgram", "Valentin Yeo", "Hypertask", "Inne"},
		},
		Audio: AudioConfig{
			SilenceThresholdMs: 3000,
			DuckVolume:         0.05,
			DuckEnabled:        false,
			AutoPauseOnMedia:   true,
			ZeroLatency:        true,
			BatteryBudgetSaver: false,
		},
		VAD: VADConfig{
			EnergyThreshold:   0.012,
			AdaptiveNoise:     true,
			NoiseMultiplier:   3.0,
			MinStartThreshold: 0.012,
			SpeechStartChunks: 2,
			NoiseMode:         NoiseModeNormal,
		},
		Diagnostics: DiagnosticsConfig{
			SaveFailedAudio: false,
		},
		Keyboard: KeyboardConfig{
			Injection: "paste",
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

	// Existing configs predate [keyboard]; default to paste injection.
	if cfg.Keyboard.Injection == "" {
		cfg.Keyboard.Injection = "paste"
	}

	migrated := false
	if !hasTOMLKey(data, "punctuate") {
		cfg.Deepgram.Punctuate = true
		migrated = true
	}
	if !hasTOMLKey(data, "smart_format") {
		// Previous versions used the Punctuation tray item to send
		// smart_format=true, so preserve that behavior for old configs.
		cfg.Deepgram.SmartFormat = cfg.Deepgram.Punctuate
		migrated = true
	}
	if !hasTOMLKey(data, "filler_words") {
		cfg.Deepgram.FillerWords = false
		migrated = true
	}
	if !hasTOMLKey(data, "save_failed_audio") {
		cfg.Diagnostics.SaveFailedAudio = false
		migrated = true
	}
	if !hasTOMLKey(data, "battery_budget_saver") {
		cfg.Audio.BatteryBudgetSaver = false
		migrated = true
	}
	if !hasTOMLKey(data, "adaptive_noise") {
		cfg.VAD.AdaptiveNoise = true
		migrated = true
	}
	if !hasTOMLKey(data, "noise_multiplier") || cfg.VAD.NoiseMultiplier <= 0 {
		cfg.VAD.NoiseMultiplier = 3.0
		migrated = true
	}
	if !hasTOMLKey(data, "min_start_threshold") || cfg.VAD.MinStartThreshold <= 0 {
		cfg.VAD.MinStartThreshold = 0.012
		migrated = true
	}
	if !hasTOMLKey(data, "speech_start_chunks") || cfg.VAD.SpeechStartChunks <= 0 {
		cfg.VAD.SpeechStartChunks = 2
		migrated = true
	}
	if !hasTOMLKey(data, "noise_mode") {
		cfg.VAD.NoiseMode = NoiseModeNormal
		migrated = true
	} else {
		normalized := NormalizeNoiseMode(cfg.VAD.NoiseMode)
		if normalized != cfg.VAD.NoiseMode {
			cfg.VAD.NoiseMode = normalized
			migrated = true
		}
	}
	if cfg.VAD.EnergyThreshold <= 0 || cfg.VAD.EnergyThreshold < 0.01 {
		// Values below ~0.01 caused fans, keyboard taps, and wireless mic noise
		// to trigger endless empty utterances. Lift old configs to the safer floor.
		cfg.VAD.EnergyThreshold = 0.012
		migrated = true
	}
	if cfg.Profile == ProfileTerminalAgent && cfg.Audio.ZeroLatency && !cfg.Deepgram.Holdback {
		// Type all but the newest unstable interim word. This is still fast, but it
		// prevents many duplicated/missing word endings from Deepgram corrections.
		cfg.Deepgram.Holdback = true
		migrated = true
	}
	if !hasTOMLKey(data, "profile") {
		cfg.Profile = cfg.InferProfile()
		migrated = true
	} else {
		normalized := NormalizeProfile(cfg.Profile)
		if normalized != cfg.Profile {
			cfg.Profile = normalized
			migrated = true
		}
	}

	if cfg.Deepgram.Model != "nova-3" {
		cfg.Deepgram.Model = "nova-3"
		migrated = true
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	if migrated {
		if err := cfg.Save(); err != nil {
			return nil, fmt.Errorf("failed to save migrated config: %w", err)
		}
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

# profile: terminal_agent, email_writing, or custom

%s

# Deepgram settings
# model: nova-3 (the only supported online dictation model)
# language: en (English), de (German) - toggle with Ctrl+Alt+Shift+L
# punctuate: Enable automatic punctuation and capitalization (true/false)
# smart_format: Format numbers, dates, currency, etc. Deepgram also enables punctuation when this is true.
# filler_words: Include filler words such as "um" and "uh" in transcripts (true/false)
# holdback: Experimental. In Ultra Fast mode, hold back the newest interim word until it stabilizes (true/false)
# keyterms: List of words/phrases for Nova-3 keyterm prompting (e.g., ["Claude Code", "exe"])

# Audio settings
# silence_threshold_ms: Auto-pause after this many milliseconds of silence
# duck_volume: Volume level (0.0-1.0) for other apps during dictation
# duck_enabled: Enable/disable audio ducking (true/false)
# device_name: Microphone to use (empty = system default, or exact device name)
# zero_latency: Ultra-fast mode - keeps Deepgram connection open while still using the configured silence threshold
# push_to_talk: Hold Ctrl+Space to record; release to finish. Microphone is closed while idle.
# battery_budget_saver: Close Deepgram after each utterance, shorten billable silence, and skip paid keyterms.

# VAD settings
# energy_threshold: Base RMS threshold for speech detection (0.0-1.0)
# adaptive_noise: Raise the effective threshold above the learned room/fan noise floor.
# noise_multiplier: Speech must be this many times louder than the ambient noise floor.
# min_start_threshold: Minimum threshold required to start a new utterance.
# speech_start_chunks: Consecutive above-threshold chunks needed before speech starts.
# noise_mode: normal uses the configured values; train adds rumble filtering and stricter speech gating.

# Diagnostics
# save_failed_audio: Save WAV files for utterances that triggered VAD but produced no transcript.
# Files are written to %%APPDATA%%\dictation\failed-utterances. Disabled by default for privacy.

# Global Hotkeys
# Ctrl+Alt+X: Pause/Resume dictation
# Ctrl+Alt+Shift+L: Toggle language (EN ↔ DE)
`, string(data))

	if err := os.WriteFile(configPath, []byte(commented), 0644); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	return nil
}

func (c *Config) Validate() error {
	c.Profile = NormalizeProfile(c.Profile)
	c.VAD.NoiseMode = NormalizeNoiseMode(c.VAD.NoiseMode)
	c.Deepgram.Model = "nova-3"
	if c.Deepgram.APIKey == "" {
		return fmt.Errorf("API key not set. Please edit config.toml and add your Deepgram API key")
	}
	return nil
}

func NormalizeNoiseMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "train", "travel", "noisy", "noisy_travel", "noisy-travel":
		return NoiseModeTrain
	default:
		return NoiseModeNormal
	}
}

// Effective returns the VAD settings to use for the selected environment.
// Train mode only raises minimums, so stricter user-provided values remain intact.
func (v VADConfig) Effective() VADConfig {
	v.NoiseMode = NormalizeNoiseMode(v.NoiseMode)
	if v.NoiseMode != NoiseModeTrain {
		return v
	}

	v.AdaptiveNoise = true
	if v.EnergyThreshold < 0.018 {
		v.EnergyThreshold = 0.018
	}
	if v.NoiseMultiplier < 4.0 {
		v.NoiseMultiplier = 4.0
	}
	if v.MinStartThreshold < 0.020 {
		v.MinStartThreshold = 0.020
	}
	if v.SpeechStartChunks < 4 {
		v.SpeechStartChunks = 4
	}
	return v
}

func NormalizeProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "terminal", "terminal_agent", "terminal-agent", "agent":
		return ProfileTerminalAgent
	case "email", "email_writing", "email-writing", "writing":
		return ProfileEmailWriting
	case "custom":
		return ProfileCustom
	default:
		return ProfileCustom
	}
}

func (c *Config) ApplyProfile(profile string) {
	profile = NormalizeProfile(profile)
	c.Profile = profile

	switch profile {
	case ProfileEmailWriting:
		c.Deepgram.Model = "nova-3"
		c.Audio.ZeroLatency = false
		c.Deepgram.Punctuate = true
		c.Deepgram.SmartFormat = true
		c.Deepgram.FillerWords = false
		c.Deepgram.Holdback = false
	case ProfileTerminalAgent:
		c.Deepgram.Model = "nova-3"
		c.Audio.ZeroLatency = true
		c.Deepgram.Punctuate = true
		c.Deepgram.SmartFormat = false
		c.Deepgram.FillerWords = false
		c.Deepgram.Holdback = true
	default:
		c.Profile = ProfileCustom
	}
}

func (c Config) DisplayProfile() string {
	profile := NormalizeProfile(c.Profile)
	if profile == ProfileCustom {
		return ProfileCustom
	}
	if c.MatchesProfile(profile) {
		return profile
	}
	return ProfileCustom
}

func (c Config) InferProfile() string {
	if c.MatchesProfile(ProfileTerminalAgent) {
		return ProfileTerminalAgent
	}
	if c.MatchesProfile(ProfileEmailWriting) {
		return ProfileEmailWriting
	}
	return ProfileCustom
}

func (c Config) MatchesProfile(profile string) bool {
	switch NormalizeProfile(profile) {
	case ProfileTerminalAgent:
		return c.Deepgram.Model == "nova-3" &&
			c.Audio.ZeroLatency &&
			c.Deepgram.Punctuate &&
			!c.Deepgram.SmartFormat &&
			!c.Deepgram.FillerWords &&
			c.Deepgram.Holdback
	case ProfileEmailWriting:
		return c.Deepgram.Model == "nova-3" &&
			!c.Audio.ZeroLatency &&
			c.Deepgram.Punctuate &&
			c.Deepgram.SmartFormat &&
			!c.Deepgram.FillerWords &&
			!c.Deepgram.Holdback
	default:
		return false
	}
}

func hasTOMLKey(data []byte, key string) bool {
	for _, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "[") {
			continue
		}
		if !strings.HasPrefix(trimmed, key) {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(trimmed, key))
		if strings.HasPrefix(rest, "=") {
			return true
		}
	}
	return false
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

	// Windows: use cmd.exe to open with default text editor. CREATE_NO_WINDOW
	// keeps the helper cmd.exe from flashing a terminal window (HideWindow is
	// ignored when Windows Terminal is the default console host).
	proc := &os.ProcAttr{
		Files: []*os.File{os.Stdin, os.Stdout, os.Stderr},
		Sys: &syscall.SysProcAttr{
			HideWindow:    true,
			CreationFlags: 0x08000000, // CREATE_NO_WINDOW
		},
	}
	_, err = os.StartProcess("C:\\Windows\\System32\\cmd.exe", []string{"/C", "start", "", configPath}, proc)
	return err
}

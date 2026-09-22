package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultNoiseModeIsNormal(t *testing.T) {
	if got := Default().VAD.NoiseMode; got != NoiseModeNormal {
		t.Fatalf("default noise mode = %q, want %q", got, NoiseModeNormal)
	}
}

func TestDefaultBatteryBudgetSaverIsOff(t *testing.T) {
	if Default().Audio.BatteryBudgetSaver {
		t.Fatal("battery and budget saver should be opt-in")
	}
}

func TestBatteryBudgetSaverSurvivesProfileChanges(t *testing.T) {
	cfg := Default()
	cfg.Audio.BatteryBudgetSaver = true

	for _, profile := range []string{ProfileEmailWriting, ProfileTerminalAgent} {
		cfg.ApplyProfile(profile)
		if !cfg.Audio.BatteryBudgetSaver {
			t.Fatalf("profile %q disabled battery and budget saver", profile)
		}
	}
}

func TestValidatePinsDeepgramToNova3(t *testing.T) {
	cfg := Default()
	cfg.Deepgram.APIKey = "test-key"
	cfg.Deepgram.Model = "flux-general-en"

	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() returned error: %v", err)
	}
	if cfg.Deepgram.Model != "nova-3" {
		t.Fatalf("model = %q, want nova-3", cfg.Deepgram.Model)
	}
}

func TestNormalizeNoiseMode(t *testing.T) {
	tests := map[string]string{
		"normal":         NoiseModeNormal,
		"TRAIN":          NoiseModeTrain,
		" noisy-travel ": NoiseModeTrain,
		"unknown":        NoiseModeNormal,
	}
	for input, want := range tests {
		if got := NormalizeNoiseMode(input); got != want {
			t.Errorf("NormalizeNoiseMode(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestTrainModeRaisesVADMinimums(t *testing.T) {
	settings := VADConfig{
		EnergyThreshold:   0.012,
		AdaptiveNoise:     false,
		NoiseMultiplier:   3.0,
		MinStartThreshold: 0.012,
		SpeechStartChunks: 2,
		NoiseMode:         NoiseModeTrain,
	}.Effective()

	if !settings.AdaptiveNoise || settings.EnergyThreshold != 0.018 ||
		settings.NoiseMultiplier != 4.0 || settings.MinStartThreshold != 0.020 ||
		settings.SpeechStartChunks != 4 {
		t.Fatalf("unexpected train settings: %+v", settings)
	}
}

func TestTrainModePreservesStricterUserValues(t *testing.T) {
	settings := VADConfig{
		EnergyThreshold:   0.030,
		AdaptiveNoise:     true,
		NoiseMultiplier:   5.0,
		MinStartThreshold: 0.035,
		SpeechStartChunks: 6,
		NoiseMode:         NoiseModeTrain,
	}.Effective()

	if settings.EnergyThreshold != 0.030 || settings.NoiseMultiplier != 5.0 ||
		settings.MinStartThreshold != 0.035 || settings.SpeechStartChunks != 6 {
		t.Fatalf("train mode reduced stricter settings: %+v", settings)
	}
}

func TestPushToTalkPreservedByProfiles(t *testing.T) {
	cfg := Default()
	if cfg.Audio.PushToTalk {
		t.Fatal("existing continuous mode must remain the default")
	}
	cfg.Audio.PushToTalk = true
	for _, profile := range []string{ProfileEmailWriting, ProfileTerminalAgent} {
		cfg.ApplyProfile(profile)
		if !cfg.Audio.PushToTalk {
			t.Fatalf("profile %q disabled push-to-talk", profile)
		}
	}
}

func TestPushToTalkConfigRoundTrip(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	cfg := Default()
	cfg.Deepgram.APIKey = "test-key"
	cfg.Audio.PushToTalk = true
	path, _ := GetConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Audio.PushToTalk {
		t.Fatal("restart lost push-to-talk preference")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	legacy := strings.ReplaceAll(string(data), "push_to_talk = true", "")
	if err := os.WriteFile(path, []byte(legacy), 0600); err != nil {
		t.Fatal(err)
	}
	loaded, err = LoadOrCreate()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Audio.PushToTalk {
		t.Fatal("legacy config unexpectedly enabled push-to-talk")
	}
}

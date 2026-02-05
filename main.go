package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/valen/go-dictate/internal/audio"
	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/deepgram"
	"github.com/valen/go-dictate/internal/hotkey"
	"github.com/valen/go-dictate/internal/keyboard"
	"github.com/valen/go-dictate/internal/logger"
	"github.com/valen/go-dictate/internal/state"
	"github.com/valen/go-dictate/internal/tray"
)

func main() {
	fmt.Printf("=== Dictation App v%s (%s) ===\n", Version, VersionName)

	// Initialize logger
	if err := logger.Init(); err != nil {
		fmt.Printf("[Logger] Warning: Failed to initialize logger: %v\n", err)
	}
	defer logger.Close()

	logger.Info("=== Dictation App v%s (%s) Starting ===", Version, VersionName)

	// Load configuration
	cfg, err := config.LoadOrCreate()
	if err != nil {
		logger.Error("Config error: %v", err)
		fmt.Printf("[Config] Error: %v\n", err)
		fmt.Println("[Config] Please edit config.toml and add your Deepgram API key")
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}

	configPath, _ := config.GetConfigPath()
	logger.Info("Config loaded from: %s", configPath)
	logger.Info("Punctuation enabled: %v", cfg.Deepgram.Punctuate)
	fmt.Printf("[Config] Loaded from: %s\n", configPath)

	// Create state manager
	stateManager := state.NewManager()

	// Create channels
	audioChunks := make(chan []int16, 100)  // Buffered to prevent drops
	transcriptText := make(chan string, 10) // Buffered for smooth delivery

	// Start audio capture
	fmt.Println("[Main] Starting audio capture...")
	audioCapture, err := audio.NewCapture(audioChunks)
	if err != nil {
		fmt.Printf("[Main] Failed to start audio capture: %v\n", err)
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}
	defer audioCapture.Stop()

	// Start keyboard simulator
	fmt.Println("[Main] Starting keyboard simulator...")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sim := keyboard.NewSimulator()
	go sim.Run(ctx, transcriptText)

	// Start VAD processor and Deepgram client manager
	go vadAndDeepgramManager(ctx, audioChunks, transcriptText, stateManager, cfg)

	// Register global hotkey for language toggle
	hotkeyManager := hotkey.NewManager()
	err = hotkeyManager.RegisterLanguageToggle(func() {
		// Toggle language between en and de
		if cfg.Deepgram.Language == "en" {
			cfg.Deepgram.Language = "de"
		} else {
			cfg.Deepgram.Language = "en"
		}
		if err := cfg.Save(); err != nil {
			logger.Error("Failed to save language change: %v", err)
		}
		logger.Info("Language switched to: %s (Ctrl+Alt+Shift+L)", cfg.Deepgram.Language)
		fmt.Printf("[Hotkey] Language switched to: %s\n", cfg.Deepgram.Language)
	})
	if err != nil {
		logger.Error("Failed to register hotkey: %v", err)
		fmt.Printf("[Hotkey] Warning: Could not register Ctrl+Alt+Shift+L: %v\n", err)
	}
	defer hotkeyManager.Stop()

	// Create and run system tray (blocks on main thread)
	fmt.Println("[Main] Creating system tray...")
	trayManager := tray.NewManager(stateManager, cfg)

	fmt.Println("[Main] Dictation app ready!")
	fmt.Println("[Main] - Left-click tray icon to pause/resume")
	fmt.Println("[Main] - Right-click for menu options")

	// Run tray (blocks until exit)
	trayManager.Run()
}

func vadAndDeepgramManager(
	ctx context.Context,
	audioRx <-chan []int16,
	textTx chan<- string,
	stateManager *state.Manager,
	cfg *config.Config,
) {
	vad := audio.NewVAD(cfg.VAD.EnergyThreshold, cfg.Audio.SilenceThresholdMs)
	ducker := audio.NewAudioDucker(cfg.Audio.DuckVolume)

	var deepgramCtx context.Context
	var deepgramCancel context.CancelFunc
	var deepgramAudioTx chan []int16

	for {
		select {
		case <-ctx.Done():
			if deepgramCancel != nil {
				deepgramCancel()
			}
			return

		case audioChunk, ok := <-audioRx:
			if !ok {
				return
			}

			currentState := stateManager.Get()

			// Skip processing if paused
			if currentState == state.Paused || currentState == state.MicConflict {
				continue
			}

			vadEvent := vad.Process(audioChunk)

			switch vadEvent {
			case audio.SpeechStarted:
				logger.Info("[VAD] Speech started - opening Deepgram connection")
				fmt.Println("[VAD] Speech started")
				stateManager.Set(state.Speaking)

				// Duck audio
				_ = ducker.Duck()

				// Start Deepgram connection
				client := deepgram.NewClient(
					cfg.Deepgram.APIKey,
					cfg.Deepgram.Language,
					cfg.Deepgram.Model,
					cfg.Deepgram.Punctuate,
					cfg.Deepgram.FillerWords,
					cfg.Deepgram.Keywords,
				)

				deepgramAudioTx = make(chan []int16, 100)
				deepgramCtx, deepgramCancel = context.WithCancel(ctx)

				go func(dgCtx context.Context, dgAudioRx <-chan []int16) {
					if err := client.StartStreaming(dgCtx, dgAudioRx, textTx); err != nil {
						fmt.Printf("[Deepgram] Error: %v\n", err)
					}
				}(deepgramCtx, deepgramAudioTx)

				// Send this chunk
				select {
				case deepgramAudioTx <- audioChunk:
				default:
				}

			case audio.Speaking:
				if currentState == state.Speaking {
					// Forward audio to Deepgram
					if deepgramAudioTx != nil {
						select {
						case deepgramAudioTx <- audioChunk:
						default:
						}
					}
				}

			case audio.SilenceDetected:
				logger.Info("[VAD] Silence detected - closing Deepgram connection")
				fmt.Println("[VAD] Silence detected")
				stateManager.Set(state.AutoPaused)

				// Restore audio
				_ = ducker.Restore()

				// Close Deepgram connection
				if deepgramCancel != nil {
					deepgramCancel()
					deepgramCancel = nil
				}
				if deepgramAudioTx != nil {
					close(deepgramAudioTx)
					deepgramAudioTx = nil
				}

				vad.Reset()

			case audio.Silence:
				// Continue listening
			}
		}
	}
}

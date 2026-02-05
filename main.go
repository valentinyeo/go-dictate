package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"golang.design/x/hotkey"
	"golang.design/x/hotkey/mainthread"

	"github.com/valen/go-dictate/internal/audio"
	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/deepgram"
	"github.com/valen/go-dictate/internal/keyboard"
	"github.com/valen/go-dictate/internal/logger"
	"github.com/valen/go-dictate/internal/state"
	"github.com/valen/go-dictate/internal/tray"
)

var (
	cfg           *config.Config
	stateManager  *state.Manager
	audioChunks   chan []int16
	transcriptText chan string
	ctx           context.Context
	cancel        context.CancelFunc
)

func main() {
	// Run on main thread for hotkey support
	mainthread.Init(run)
}

func run() {
	fmt.Printf("=== Dictation App v%s (%s) ===\n", Version, VersionName)

	// Initialize logger
	if err := logger.Init(); err != nil {
		fmt.Printf("[Logger] Warning: Failed to initialize logger: %v\n", err)
	}
	defer logger.Close()

	logger.Info("=== Dictation App v%s (%s) Starting ===", Version, VersionName)

	// Load configuration
	var err error
	cfg, err = config.LoadOrCreate()
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
	stateManager = state.NewManager()

	// Create channels
	audioChunks = make(chan []int16, 100)
	transcriptText = make(chan string, 10)

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
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	sim := keyboard.NewSimulator()
	go sim.Run(ctx, transcriptText)

	// Start VAD processor and Deepgram client manager
	go vadAndDeepgramManager(ctx, audioChunks, transcriptText, stateManager, cfg)

	// Create and run system tray
	fmt.Println("[Main] Creating system tray...")
	trayManager := tray.NewManager(stateManager, cfg)

	// Register hotkeys after tray is created
	go registerHotkeys()

	fmt.Printf("[Main] Dictation app v%s ready!\n", Version)
	fmt.Println("[Main] - Ctrl+Alt+X: Pause/Resume")
	fmt.Println("[Main] - Ctrl+Alt+Shift+L: Toggle language")

	// Run tray (blocks until exit)
	trayManager.Run()
}

func registerHotkeys() {
	// Give tray time to initialize
	time.Sleep(500 * time.Millisecond)

	logger.Info("[Hotkey] Registering Ctrl+Alt+X for pause/resume...")

	// Pause/Resume: Ctrl+Alt+X
	hkPause := hotkey.New([]hotkey.Modifier{hotkey.ModCtrl, hotkey.ModAlt}, hotkey.KeyX)
	if err := hkPause.Register(); err != nil {
		logger.Error("[Hotkey] FAILED to register Ctrl+Alt+X: %v", err)
		log.Printf("[Hotkey] FAILED to register Ctrl+Alt+X: %v", err)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Alt+X")
		log.Println("[Hotkey] Successfully registered Ctrl+Alt+X")
		go func() {
			for range hkPause.Keydown() {
				logger.Info("[Hotkey] *** CTRL+ALT+X PRESSED *** Toggling pause...")
				log.Println("[Hotkey] Ctrl+Alt+X pressed!")
				newState := stateManager.TogglePause()
				logger.Info("[Hotkey] Pause toggled to: %s", newState)
				log.Printf("[Hotkey] Toggled to: %s\n", newState)
			}
		}()
	}

	// Language toggle: Ctrl+Alt+Shift+L
	hkLanguage := hotkey.New([]hotkey.Modifier{hotkey.ModCtrl, hotkey.ModAlt, hotkey.ModShift}, hotkey.KeyL)
	if err := hkLanguage.Register(); err != nil {
		logger.Error("[Hotkey] Failed to register Ctrl+Alt+Shift+L: %v", err)
		log.Printf("[Hotkey] Failed to register Ctrl+Alt+Shift+L: %v", err)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Alt+Shift+L")
		log.Println("[Hotkey] Successfully registered Ctrl+Alt+Shift+L")
		go func() {
			for range hkLanguage.Keydown() {
				logger.Info("[Hotkey] Ctrl+Alt+Shift+L pressed - toggling language")
				// Toggle language
				if cfg.Deepgram.Language == "en" {
					cfg.Deepgram.Language = "de"
				} else {
					cfg.Deepgram.Language = "en"
				}
				if err := cfg.Save(); err != nil {
					logger.Error("Failed to save language change: %v", err)
				}
				logger.Info("Language switched to: %s", cfg.Deepgram.Language)
			}
		}()
	}
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

			// Update media detection visual indicator (does not block transcription)
			if audio.IsMediaPlaying() {
				if currentState == state.Active {
					stateManager.Set(state.MediaDetected)
				}
			} else if currentState == state.MediaDetected {
				stateManager.Set(state.Active)
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

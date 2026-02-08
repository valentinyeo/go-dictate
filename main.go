package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"syscall"
	"time"
	"unsafe"

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
	cfg               *config.Config
	stateManager      *state.Manager
	audioChunks       chan []int16
	transcriptText    chan string
	settingsChangedCh chan struct{}
	ctx               context.Context
	cancel            context.CancelFunc
)

func main() {
	// Run on main thread for hotkey support
	mainthread.Init(run)
}

// enforceSingleInstance uses a Windows named mutex to ensure only one instance runs.
// Returns a handle that must be kept alive for the process lifetime.
func enforceSingleInstance() (uintptr, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")
	name, _ := syscall.UTF16PtrFromString("Global\\GoDictateAppMutex")
	handle, _, err := createMutex.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return 0, err
	}
	// ERROR_ALREADY_EXISTS = 183
	if errno, ok := err.(syscall.Errno); ok && errno == 183 {
		syscall.CloseHandle(syscall.Handle(handle))
		return 0, fmt.Errorf("another instance is already running")
	}
	return handle, nil
}

func run() {
	// Enforce single instance
	mutexHandle, singleErr := enforceSingleInstance()
	if singleErr != nil {
		fmt.Println("[Main] Another instance is already running. Exiting.")
		os.Exit(0)
	}
	defer syscall.CloseHandle(syscall.Handle(mutexHandle))

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

	// Enumerate microphone devices
	micDevices, err := audio.ListCaptureDevices()
	if err != nil {
		logger.Error("[Main] Failed to list microphones: %v", err)
		fmt.Printf("[Main] Warning: Failed to list microphones: %v\n", err)
	}
	var micNames []string
	for _, d := range micDevices {
		micNames = append(micNames, d.Name)
		fmt.Printf("[Main] Found microphone: %s\n", d.Name)
	}

	// Find the configured device
	var selectedDevice *audio.CaptureDevice
	if cfg.Audio.DeviceName != "" {
		for i, d := range micDevices {
			if d.Name == cfg.Audio.DeviceName {
				selectedDevice = &micDevices[i]
				break
			}
		}
		if selectedDevice == nil {
			fmt.Printf("[Main] Warning: Configured mic %q not found, using system default\n", cfg.Audio.DeviceName)
		}
	}

	// Start audio capture
	fmt.Println("[Main] Starting audio capture...")
	audioCapture, err := audio.NewCapture(audioChunks, selectedDevice)
	if err != nil {
		fmt.Printf("[Main] Failed to start audio capture: %v\n", err)
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}
	defer func() { audioCapture.Stop() }()

	// Start keyboard simulator
	fmt.Println("[Main] Starting keyboard simulator...")
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	sim := keyboard.NewSimulator()
	go sim.Run(ctx, transcriptText)

	// Start VAD processor and Deepgram client manager (settingsChangedCh wired after tray creation)
	settingsChangedCh = make(chan struct{}, 1)
	go vadAndDeepgramManager(ctx, audioChunks, transcriptText, stateManager, cfg, settingsChangedCh)

	// Create and run system tray
	fmt.Println("[Main] Creating system tray...")
	trayManager := tray.NewManager(stateManager, cfg, micNames, cfg.Audio.DeviceName)

	// Forward settings changes from tray to VAD manager
	go func() {
		for range trayManager.SettingsChangedCh {
			select {
			case settingsChangedCh <- struct{}{}:
			default:
			}
		}
	}()

	// Handle microphone changes from tray menu
	go func() {
		for deviceName := range trayManager.MicChangedCh {
			displayName := deviceName
			if displayName == "" {
				displayName = "System Default"
			}
			logger.Info("[Main] Switching microphone to: %s", displayName)

			// Find device
			var newDevice *audio.CaptureDevice
			if deviceName != "" {
				for i, d := range micDevices {
					if d.Name == deviceName {
						newDevice = &micDevices[i]
						break
					}
				}
			}

			// Restart audio capture with new device
			audioCapture.Stop()
			newCapture, err := audio.NewCapture(audioChunks, newDevice)
			if err != nil {
				logger.Error("[Main] Failed to switch microphone: %v", err)
				fmt.Printf("[Main] Failed to switch microphone: %v\n", err)
				// Try to restart with system default
				newCapture, err = audio.NewCapture(audioChunks, nil)
				if err != nil {
					logger.Error("[Main] Failed to restart with default mic: %v", err)
					fmt.Printf("[Main] CRITICAL: Failed to restart audio capture: %v\n", err)
					continue
				}
			}
			audioCapture = newCapture
			fmt.Printf("[Main] Microphone switched to: %s\n", displayName)
		}
	}()

	// Register hotkeys after tray is created
	go registerHotkeys()

	fmt.Printf("[Main] Dictation app v%s ready!\n", Version)
	fmt.Println("[Main] - Ctrl+Alt+X: Pause/Resume")
	fmt.Println("[Main] - Ctrl+Alt+L: Toggle language (EN ↔ DE)")

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

	// Language toggle: Ctrl+Alt+L
	hkLanguage := hotkey.New([]hotkey.Modifier{hotkey.ModCtrl, hotkey.ModAlt}, hotkey.KeyL)
	if err := hkLanguage.Register(); err != nil {
		logger.Error("[Hotkey] Failed to register Ctrl+Alt+L: %v", err)
		log.Printf("[Hotkey] Failed to register Ctrl+Alt+L: %v", err)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Alt+L")
		log.Println("[Hotkey] Successfully registered Ctrl+Alt+L")
		go func() {
			for range hkLanguage.Keydown() {
				logger.Info("[Hotkey] Ctrl+Alt+L pressed - toggling language")
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
				select {
				case settingsChangedCh <- struct{}{}:
				default:
				}
			}
		}()
	}

	// Removed Ctrl+Alt+Q - user prefers to use tray Exit button
}

func vadAndDeepgramManager(
	ctx context.Context,
	audioRx <-chan []int16,
	textTx chan<- string,
	stateManager *state.Manager,
	cfg *config.Config,
	settingsChangedCh <-chan struct{},
) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("[VAD] PANIC recovered: %v", r)
			fmt.Printf("[VAD] PANIC recovered: %v\n", r)
		}
	}()

	vad := audio.NewVAD(cfg.VAD.EnergyThreshold, cfg.Audio.SilenceThresholdMs)

	// Per-app volume ducking for Chrome/Edge (master volume doesn't work on this system)
	logger.Info("[AppVolumeDucker] Initializing with duck volume: %.0f%%", cfg.Audio.DuckVolume*100)
	ducker := audio.NewAppVolumeDucker(cfg.Audio.DuckVolume)

	var deepgramCtx context.Context
	var deepgramCancel context.CancelFunc
	var deepgramAudioTx chan []int16

	for {
		// When paused, disable audio processing entirely (zero CPU)
		// Set audioRx to nil in select to block until state changes
		var activeAudioRx <-chan []int16
		if s := stateManager.Get(); s != state.Paused && s != state.MicConflict {
			activeAudioRx = audioRx
		}

		select {
		case <-ctx.Done():
			if deepgramCancel != nil {
				deepgramCancel()
			}
			return

		case <-stateManager.Changed():
			// State changed (e.g. unpaused) - re-evaluate on next loop iteration
			continue

		case <-settingsChangedCh:
			// Settings changed (language, punctuation, etc.) - close active Deepgram connection
			// so the next speech burst picks up the new settings
			if deepgramCancel != nil {
				logger.Info("[VAD] Settings changed - closing active Deepgram connection")
				fmt.Println("[VAD] Settings changed - reconnecting with new settings")
				deepgramCancel()
				deepgramCancel = nil
			}
			if deepgramAudioTx != nil {
				close(deepgramAudioTx)
				deepgramAudioTx = nil
			}
			vad.Reset()
			stateManager.Set(state.AutoPaused)

		case audioChunk, ok := <-activeAudioRx:
			if !ok {
				return
			}

			currentState := stateManager.Get()

			// Auto-pause ONLY when in an active meeting (NOT for media playback)
			meetingActive := audio.IsMeetingActive()
			mediaPlaying := audio.IsMediaPlaying()

			if meetingActive {
				// Meeting detected - pause to avoid transcribing meeting audio
				if currentState == state.Active || currentState == state.AutoPaused {
					logger.Info("[Meeting] Active call detected - auto-pausing")
					fmt.Println("🔴 [Meeting] In a call - pausing dictation")
					stateManager.Set(state.MediaPaused)
				} else if currentState == state.MediaPaused {
					// Stay paused during meeting
					continue
				}
			} else {
				// No meeting - resume if we were paused
				if currentState == state.MediaPaused {
					logger.Info("[Meeting] Call ended - resuming")
					fmt.Println("✅ [Meeting] Call ended - resuming dictation")
					stateManager.Set(state.Active)
				}
			}

			// Media detection: Just for visual indicator (amber icon), doesn't pause
			// Auto-ducking will handle the volume when you speak
			if mediaPlaying && currentState == state.Active {
				// Optional: Set visual indicator (not implemented yet)
				// Could add state.MediaDetected for amber icon without pausing
			}

			vadEvent := vad.Process(audioChunk)

			switch vadEvent {
			case audio.SpeechStarted:
				logger.Info("[VAD] Speech started - opening Deepgram connection")
				fmt.Println("[VAD] Speech started")

				// Duck Chrome/Edge volume
				if !meetingActive && cfg.Audio.DuckEnabled {
					if err := ducker.Duck(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to duck: %v", err)
					}
				}

				stateManager.Set(state.Speaking)

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
					defer func() {
						if r := recover(); r != nil {
							logger.Error("[Deepgram] PANIC recovered: %v", r)
							fmt.Printf("[Deepgram] PANIC recovered: %v\n", r)
						}
					}()
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
				// Forward audio to Deepgram if connection is open
				// (Don't check currentState - it might be stale if we just transitioned from MediaPaused)
				if deepgramAudioTx != nil {
					select {
					case deepgramAudioTx <- audioChunk:
					default:
					}
				}

			case audio.SilenceDetected:
				logger.Info("[VAD] Silence detected - closing Deepgram connection")
				fmt.Println("[VAD] Silence detected")

				// Restore Chrome/Edge volume
				if err := ducker.Restore(); err != nil {
					logger.Error("[AppVolumeDucker] Failed to restore: %v", err)
				}

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

				// Return to MediaPaused if media is still playing, otherwise AutoPaused
				if audio.IsMediaPlaying() {
					logger.Info("[VAD] Media still playing - returning to MediaPaused")
					stateManager.Set(state.MediaPaused)
				} else {
					stateManager.Set(state.AutoPaused)
				}

			case audio.Silence:
				// Continue listening
			}
		}
	}
}

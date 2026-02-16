package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
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
	"github.com/valen/go-dictate/internal/stt"
	"github.com/valen/go-dictate/internal/state"
	"github.com/valen/go-dictate/internal/tray"
	"github.com/valen/go-dictate/internal/vosk"
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

	// Redirect stderr to crash.log so panics are captured (no console with -H windowsgui)
	logger.RedirectStderr()

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

	// Start audio capture (protected by mutex for watchdog + mic change access)
	var audioMu sync.Mutex
	fmt.Println("[Main] Starting audio capture...")
	audioCapture, err := audio.NewCapture(audioChunks, selectedDevice)
	if err != nil {
		fmt.Printf("[Main] Failed to start audio capture: %v\n", err)
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}
	defer func() { audioMu.Lock(); audioCapture.Stop(); audioMu.Unlock() }()

	// Start keyboard simulator
	fmt.Println("[Main] Starting keyboard simulator...")
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	sim := keyboard.NewSimulator()
	go sim.Run(ctx, transcriptText)

	// Start VAD processor and STT engine manager (settingsChangedCh wired after tray creation)
	settingsChangedCh = make(chan struct{}, 1)
	go vadAndSTTManager(ctx, audioChunks, transcriptText, stateManager, cfg, settingsChangedCh)

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
			audioMu.Lock()
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
					audioMu.Unlock()
					continue
				}
			}
			audioCapture = newCapture
			audioMu.Unlock()
			fmt.Printf("[Main] Microphone switched to: %s\n", displayName)
		}
	}()

	// Audio watchdog: detect dead audio devices (e.g., after sleep/wake)
	// and automatically restart capture. Also detects "alive but dead" mics
	// where the callback fires but audio is silent/flat (broken driver state).
	go func() {
		const watchdogInterval = 5 * time.Second
		const watchdogTimeout = 10 * time.Second
		// If peak RMS stays below this for flatLineMax consecutive checks,
		// the mic is probably in a bad state (delivering noise but no speech).
		// 0.005 is well below any real speech but above pure digital silence.
		const flatLineRMSThreshold = 0.005
		const flatLineMax = 12 // 12 * 5s = 60s of flat audio before restart
		flatLineCount := 0

		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				audioMu.Lock()
				alive := audioCapture.IsAlive(watchdogTimeout)
				peakRMS := audioCapture.PeakRMS()

				needsRestart := false
				reason := ""

				if !alive {
					needsRestart = true
					reason = fmt.Sprintf("callback silent for >%v", watchdogTimeout)
				} else if peakRMS < flatLineRMSThreshold {
					flatLineCount++
					if flatLineCount >= flatLineMax {
						needsRestart = true
						reason = fmt.Sprintf("flat audio (peak RMS %.6f) for %ds", peakRMS, flatLineCount*5)
						flatLineCount = 0
					}
				} else {
					flatLineCount = 0
				}

				// Periodic RMS logging (every ~30s when not flat)
				if flatLineCount == 0 || flatLineCount%6 == 0 {
					logger.Debug("[Watchdog] Peak RMS: %.6f (flat count: %d/%d)", peakRMS, flatLineCount, flatLineMax)
				}

				if needsRestart {
					logger.Info("[Watchdog] Audio device problem: %s - restarting capture", reason)
					fmt.Printf("[Watchdog] %s - restarting...\n", reason)
					audioCapture.Stop()
					time.Sleep(1 * time.Second)

					newCapture, err := audio.NewCapture(audioChunks, selectedDevice)
					if err != nil {
						logger.Error("[Watchdog] Failed to restart with selected device: %v", err)
						// Fall back to system default
						newCapture, err = audio.NewCapture(audioChunks, nil)
						if err != nil {
							logger.Error("[Watchdog] Failed to restart with default device: %v", err)
							audioMu.Unlock()
							continue
						}
						logger.Info("[Watchdog] Fell back to system default mic")
					}
					audioCapture = newCapture
					flatLineCount = 0
					logger.Info("[Watchdog] Audio capture restarted successfully")
					fmt.Println("[Watchdog] Audio capture restarted")
				}
				audioMu.Unlock()
			}
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

func vadAndSTTManager(
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

	// Pre-roll ring buffer: keeps last ~500ms of audio so first words aren't lost.
	// VAD needs audio to detect speech - by the time it fires SpeechStarted,
	// the beginning of the utterance has already passed. Pre-roll captures it.
	const preRollSize = 25 // ~500ms at ~20ms/chunk
	preRoll := make([][]int16, 0, preRollSize)

	// Per-app volume ducking for Chrome/Edge
	logger.Info("[AppVolumeDucker] Initializing with duck volume: %.0f%%", cfg.Audio.DuckVolume*100)
	ducker := audio.NewAppVolumeDucker(cfg.Audio.DuckVolume)

	// Ghost speech detection: if VAD says "speaking" but Deepgram returns no
	// actual text for this long, background noise is triggering the VAD.
	// Force-close the STT connection so the icon goes back to standby.
	const noTextTimeout = 15 * time.Second
	var lastTextNano atomic.Int64 // tracks when real text was last received

	// After ghost speech is detected, suppress VAD for a cooldown period
	// to prevent immediate re-triggering from the same background noise.
	const ghostCooldown = 30 * time.Second
	var ghostCooldownUntil time.Time

	var sttCtx context.Context
	var sttCancel context.CancelFunc
	var sttAudioTx chan []int16
	var textBridge chan string // intermediary channel to track text timing
	var voskEngine *vosk.Engine

	defer func() {
		if voskEngine != nil {
			voskEngine.Close()
		}
	}()

	for {
		// When paused, disable audio processing entirely (zero CPU)
		var activeAudioRx <-chan []int16
		if s := stateManager.Get(); s != state.Paused && s != state.MicConflict {
			activeAudioRx = audioRx
		}

		select {
		case <-ctx.Done():
			if sttCancel != nil {
				sttCancel()
			}
			return

		case <-stateManager.Changed():
			continue

		case <-settingsChangedCh:
			// Settings changed - close active STT connection
			if sttCancel != nil {
				logger.Info("[VAD] Settings changed - closing active STT connection")
				fmt.Println("[VAD] Settings changed - reconnecting with new settings")
				sttCancel()
				sttCancel = nil
			}
			if sttAudioTx != nil {
				close(sttAudioTx)
				sttAudioTx = nil
			}
			textBridge = nil // don't close - bridge goroutine exits via sttCtx
			// Clear vosk engine so it reloads with new language/model
			if voskEngine != nil {
				voskEngine.Close()
				voskEngine = nil
			}
			vad.Reset()
			stateManager.Set(state.AutoPaused)

		case audioChunk, ok := <-activeAudioRx:
			if !ok {
				return
			}

			currentState := stateManager.Get()

			// Auto-pause during active meetings or screen recordings
			meetingActive := audio.IsMeetingActive()
			recordingActive := audio.IsRecordingActive()
			mediaPlaying := audio.IsMediaPlaying()

			if meetingActive || recordingActive {
				if currentState == state.Active || currentState == state.AutoPaused {
					if recordingActive {
						logger.Info("[Recording] Screen recording detected - auto-pausing")
						fmt.Println("[Recording] Screen recording active - pausing dictation")
					} else {
						logger.Info("[Meeting] Active call detected - auto-pausing")
						fmt.Println("[Meeting] In a call - pausing dictation")
					}
					stateManager.Set(state.MediaPaused)
				} else if currentState == state.MediaPaused {
					continue
				}
			} else {
				if currentState == state.MediaPaused {
					logger.Info("[Meeting] Call/recording ended - resuming")
					fmt.Println("[Meeting] Call/recording ended - resuming dictation")
					stateManager.Set(state.Active)
				}
			}

			if mediaPlaying && currentState == state.Active {
				// Visual indicator only
			}

			// Skip VAD during ghost speech cooldown
			if time.Now().Before(ghostCooldownUntil) {
				// Still collect pre-roll so first words aren't lost when cooldown ends
				if len(preRoll) >= preRollSize {
					copy(preRoll, preRoll[1:])
					preRoll[len(preRoll)-1] = audioChunk
				} else {
					preRoll = append(preRoll, audioChunk)
				}
				continue
			}

			vadEvent := vad.Process(audioChunk)

			switch vadEvent {
			case audio.SpeechStarted:
				logger.Info("[VAD] Speech started")
				fmt.Println("[VAD] Speech started")

				// Duck Chrome/Edge volume
				if !meetingActive && cfg.Audio.DuckEnabled {
					if err := ducker.Duck(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to duck: %v", err)
					}
				}

				stateManager.Set(state.Speaking)

				// Reset ghost speech timer
				lastTextNano.Store(time.Now().UnixNano())

				// Choose STT engine
				var engine stt.Engine
				if cfg.Offline.Enabled {
					if voskEngine == nil {
						modelKey := vosk.ModelKeyForLanguage(cfg.Deepgram.Language, cfg.Offline.ModelSize == "large")
						if vosk.IsAvailable() && vosk.IsModelDownloaded(modelKey) {
							modelPath, _ := vosk.GetModelPath(modelKey)
							var err error
							voskEngine, err = vosk.NewEngine(modelPath)
							if err != nil {
								logger.Error("[Vosk] Failed to load: %v", err)
								fmt.Printf("[Vosk] Error: %v, falling back to Deepgram\n", err)
							}
						} else {
							logger.Info("[Vosk] Model not ready, falling back to Deepgram")
							fmt.Println("[Vosk] Model not ready, using Deepgram")
						}
					}
					if voskEngine != nil {
						engine = voskEngine
					}
				}
				if engine == nil {
					engine = deepgram.NewClient(
						cfg.Deepgram.APIKey,
						cfg.Deepgram.Language,
						cfg.Deepgram.Model,
						cfg.Deepgram.Punctuate,
						cfg.Deepgram.FillerWords,
						cfg.Deepgram.Keywords,
					)
				}

				sttAudioTx = make(chan []int16, 100)
				sttCtx, sttCancel = context.WithCancel(ctx)

				// Text bridge: forwards text to keyboard simulator while tracking
				// the last time real text was received (for ghost speech detection).
				// Uses sttCtx so it exits when the STT session ends. We never close
				// textBridge directly to avoid "send on closed channel" panics -
				// the channel is GC'd when both writer and reader goroutines exit.
				textBridge = make(chan string, 10)
				go func(bridge <-chan string, bridgeCtx context.Context) {
					for {
						select {
						case text, ok := <-bridge:
							if !ok {
								return
							}
							lastTextNano.Store(time.Now().UnixNano())
							select {
							case textTx <- text:
							case <-bridgeCtx.Done():
								return
							}
						case <-bridgeCtx.Done():
							return
						}
					}
				}(textBridge, sttCtx)

				go func(e stt.Engine, eCtx context.Context, eAudioRx <-chan []int16, eTx chan<- string) {
					defer func() {
						if r := recover(); r != nil {
							logger.Error("[STT] PANIC recovered: %v", r)
							fmt.Printf("[STT] PANIC recovered: %v\n", r)
						}
					}()
					if err := e.StartStreaming(eCtx, eAudioRx, eTx); err != nil {
						fmt.Printf("[STT] Error: %v\n", err)
					}
				}(engine, sttCtx, sttAudioTx, textBridge)

				// Flush pre-roll buffer (captures audio BEFORE VAD fired)
				if len(preRoll) > 0 {
					logger.Info("[VAD] Flushing %d pre-roll chunks (~%dms)", len(preRoll), len(preRoll)*20)
					for _, chunk := range preRoll {
						select {
						case sttAudioTx <- chunk:
						default:
						}
					}
					preRoll = preRoll[:0]
				}

				// Send the current chunk that triggered SpeechStarted
				select {
				case sttAudioTx <- audioChunk:
				default:
				}

			case audio.Speaking:
				if sttAudioTx != nil {
					// Ghost speech check: no real text for too long = background noise
					lastText := time.Unix(0, lastTextNano.Load())
					if time.Since(lastText) > noTextTimeout {
						ghostCooldownUntil = time.Now().Add(ghostCooldown)
						logger.Info("[VAD] Ghost speech detected - no text for %v, closing STT (cooldown %v)", noTextTimeout, ghostCooldown)
						fmt.Println("[VAD] Ghost speech detected - background noise, cooling down 30s")

						if cfg.Audio.DuckEnabled {
							if err := ducker.Restore(); err != nil {
								logger.Error("[AppVolumeDucker] Failed to restore: %v", err)
							}
						}
						if sttCancel != nil {
							sttCancel()
							sttCancel = nil
						}
						close(sttAudioTx)
						sttAudioTx = nil
						textBridge = nil // don't close - bridge goroutine exits via sttCtx
						vad.Reset()
						preRoll = preRoll[:0]
						stateManager.Set(state.AutoPaused)
						continue
					}

					select {
					case sttAudioTx <- audioChunk:
					default:
					}
				}

			case audio.SilenceDetected:
				logger.Info("[VAD] Silence detected - closing STT connection")
				fmt.Println("[VAD] Silence detected")

				// Restore Chrome/Edge volume
				if cfg.Audio.DuckEnabled {
					if err := ducker.Restore(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to restore: %v", err)
					}
				}

				// Close STT connection
				if sttCancel != nil {
					sttCancel()
					sttCancel = nil
				}
				if sttAudioTx != nil {
					close(sttAudioTx)
					sttAudioTx = nil
				}
				textBridge = nil // don't close - bridge goroutine exits via sttCtx

				vad.Reset()
				preRoll = preRoll[:0] // Clear pre-roll for next utterance

				if audio.IsMediaPlaying() {
					logger.Info("[VAD] Media still playing - returning to MediaPaused")
					stateManager.Set(state.MediaPaused)
				} else {
					stateManager.Set(state.AutoPaused)
				}

			case audio.Silence:
				// Collect into pre-roll ring buffer for next speech start
				if len(preRoll) >= preRollSize {
					// Shift left: drop oldest, append newest
					copy(preRoll, preRoll[1:])
					preRoll[len(preRoll)-1] = audioChunk
				} else {
					preRoll = append(preRoll, audioChunk)
				}
			}
		}
	}
}

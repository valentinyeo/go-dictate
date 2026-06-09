package main

import (
	"context"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"github.com/valen/go-dictate/internal/audio"
	"github.com/valen/go-dictate/internal/config"
	"github.com/valen/go-dictate/internal/deepgram"
	"github.com/valen/go-dictate/internal/diagnostics"
	hk "github.com/valen/go-dictate/internal/hotkey"
	"github.com/valen/go-dictate/internal/keyboard"
	"github.com/valen/go-dictate/internal/logger"
	"github.com/valen/go-dictate/internal/notify"
	"github.com/valen/go-dictate/internal/state"
	"github.com/valen/go-dictate/internal/stt"
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
	audioPaused       atomic.Bool // true when capture is stopped due to pause
	missedWake        atomic.Bool // true when wake happened while paused
	trayMgr           *tray.Manager

	lastSTTResetMu     sync.Mutex
	lastSTTResetAt     time.Time
	lastSTTResetReason string
)

func main() {
	run()
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

// enableEfficiencyMode used to opt this process into Windows EcoQoS. That saved
// a little power, but real-time microphone capture is reliability-critical; on
// some wireless mics it correlated with silent capture callbacks. Keep normal
// priority and explicitly clear EcoQoS instead.
func enableEfficiencyMode() {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")

	setClass := kernel32.NewProc("SetPriorityClass")
	getCurrentProcess := kernel32.NewProc("GetCurrentProcess")
	h, _, _ := getCurrentProcess.Call()
	const NORMAL_PRIORITY_CLASS = 0x00000020
	setClass.Call(h, NORMAL_PRIORITY_CLASS)

	setInfo := kernel32.NewProc("SetProcessInformation")
	type PROCESS_POWER_THROTTLING_STATE struct {
		Version     uint32
		ControlMask uint32
		StateMask   uint32
	}
	const ProcessPowerThrottling = 4
	const PROCESS_POWER_THROTTLING_EXECUTION_SPEED = 0x1
	state := PROCESS_POWER_THROTTLING_STATE{
		Version:     1,
		ControlMask: PROCESS_POWER_THROTTLING_EXECUTION_SPEED,
		StateMask:   0,
	}
	setInfo.Call(h, ProcessPowerThrottling, uintptr(unsafe.Pointer(&state)), unsafe.Sizeof(state))
	fmt.Println("[Main] Real-time reliability mode enabled (EcoQoS disabled)")
}

func requestSTTReset(reason string) {
	if settingsChangedCh == nil {
		return
	}
	noteSTTReset(reason)
	logger.Info("[Main] Requesting STT reset: %s", reason)
	select {
	case settingsChangedCh <- struct{}{}:
	default:
	}
}

func noteSTTReset(reason string) {
	lastSTTResetMu.Lock()
	lastSTTResetAt = time.Now()
	lastSTTResetReason = reason
	lastSTTResetMu.Unlock()
}

func recentSTTReset(window time.Duration) (bool, string, time.Duration) {
	lastSTTResetMu.Lock()
	defer lastSTTResetMu.Unlock()
	if lastSTTResetAt.IsZero() {
		return false, "", 0
	}
	age := time.Since(lastSTTResetAt)
	if age > window {
		return false, "", age
	}
	return true, lastSTTResetReason, age
}

func run() {
	// Enforce single instance
	mutexHandle, singleErr := enforceSingleInstance()
	if singleErr != nil {
		fmt.Println("[Main] Another instance is already running. Exiting.")
		os.Exit(0)
	}
	defer syscall.CloseHandle(syscall.Handle(mutexHandle))

	// Enable Windows Efficiency Mode before anything else
	enableEfficiencyMode()

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
	logger.Info("Smart format enabled: %v", cfg.Deepgram.SmartFormat)
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
	if cfg.Keyboard.Injection == "type" {
		sim.SetTypeMode(true)
	}
	logger.Info("[Keyboard] Injection method: %s", cfg.Keyboard.Injection)
	go sim.Run(ctx, transcriptText)

	// Start VAD processor and STT engine manager (settingsChangedCh wired after tray creation)
	settingsChangedCh = make(chan struct{}, 1)
	go vadAndSTTManager(ctx, audioChunks, transcriptText, stateManager, cfg, settingsChangedCh, sim)

	// Create and run system tray
	fmt.Println("[Main] Creating system tray...")
	trayMgr = tray.NewManager(stateManager, cfg, micNames, cfg.Audio.DeviceName, Version, VersionName, VersionSummary)

	// Forward settings changes from tray to VAD manager
	go func() {
		for range trayMgr.SettingsChangedCh {
			noteSTTReset("tray settings changed")
			select {
			case settingsChangedCh <- struct{}{}:
			default:
			}
		}
	}()

	// Handle microphone changes from tray menu
	go func() {
		for deviceName := range trayMgr.MicChangedCh {
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

			// Update selectedDevice so unpause uses the right mic
			selectedDevice = newDevice

			// If paused, just remember the choice - capture restarts on unpause
			if audioPaused.Load() {
				fmt.Printf("[Main] Microphone will switch to %s on resume\n", displayName)
				continue
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
			requestSTTReset("microphone switched")
			fmt.Printf("[Main] Microphone switched to: %s\n", displayName)
		}
	}()

	// Start Windows power event monitor for sleep/wake detection
	powerMon := audio.NewPowerMonitor()
	defer powerMon.Stop()

	// Audio watchdog: detect dead audio devices (e.g., after sleep/wake,
	// wireless mic dropout) and automatically restart capture. Also detects
	// "alive but dead" mics where the callback fires but audio is flat.
	//
	// Strategy: try up to 5 restarts quickly, then STOP and just monitor.
	// The existing device may recover on its own (wireless reconnect).
	// Constant init/deinit leaks resources and freezes the UI.
	go func() {
		const watchdogInterval = 5 * time.Second
		const watchdogTimeout = 10 * time.Second
		const flatLineRMSThreshold = 0.0005 // lowered from 0.005: Intel SST mics with HW noise cancellation read ~0.001 during silence
		const flatLineMax = 6               // 6 * 5s = 30s of flat audio before restart (was 4/20s)
		const maxRestartAttempts = 5        // stop restarting after this many failures
		const monitorOnlyLogInterval = 60   // log every 60 ticks (5 min) while monitoring
		const monitorRetryInterval = 12     // retry from monitor mode every 12 ticks (1 min)
		flatLineCount := 0
		restartAttempts := 0
		monitorOnly := false // true = gave up restarting, just watching RMS
		monitorTicks := 0

		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return

			case <-powerMon.Wake:
				// Windows power event: system resumed from sleep/hibernate.
				// Wait a few seconds for audio subsystem to settle, then
				// force a fresh capture restart.
				if audioPaused.Load() {
					logger.Info("[Watchdog] Wake event while paused - flagging for unpause restart")
					missedWake.Store(true)
					continue
				}
				logger.Info("[Watchdog] Wake event received - waiting 5s for audio subsystem to settle")
				time.Sleep(5 * time.Second)
				logger.Info("[Watchdog] Restarting audio capture after wake")
				audioMu.Lock()
				audioCapture.Stop()
				time.Sleep(500 * time.Millisecond)
				newCapture, err := audio.NewCapture(audioChunks, selectedDevice)
				if err != nil {
					logger.Error("[Watchdog] Post-wake restart failed: %v", err)
					newCapture, err = audio.NewCapture(audioChunks, nil)
				}
				if err == nil {
					audioCapture = newCapture
					logger.Info("[Watchdog] Audio capture restarted after wake")
					requestSTTReset("audio capture restarted after wake")
				}
				monitorOnly = false
				restartAttempts = 0
				flatLineCount = 0
				monitorTicks = 0
				audioMu.Unlock()
				continue

			case <-ticker.C:
				// Skip watchdog while capture is stopped (paused)
				if audioPaused.Load() {
					continue
				}
				audioMu.Lock()
				alive := audioCapture.IsAlive(watchdogTimeout)
				peakRMS := audioCapture.PeakRMS()

				// Check if audio recovered (works in both modes)
				if peakRMS >= flatLineRMSThreshold {
					if monitorOnly {
						logger.Info("[Watchdog] Audio recovered naturally after %d monitor ticks!", monitorTicks)
						monitorOnly = false
					} else if restartAttempts > 0 {
						logger.Info("[Watchdog] Audio recovered after %d restart attempts", restartAttempts)
					}
					flatLineCount = 0
					restartAttempts = 0
					monitorTicks = 0
					audioMu.Unlock()
					continue
				}

				// Monitor-only mode: log periodically, retry every 5 min
				if monitorOnly {
					monitorTicks++
					if monitorTicks%monitorOnlyLogInterval == 0 {
						logger.Debug("[Watchdog] Still flat (RMS %.6f) - monitoring, %d min elapsed", peakRMS, monitorTicks*5/60)
					}
					// Periodic retry: exit monitor mode every 5 min to try again
					if monitorTicks%monitorRetryInterval == 0 {
						logger.Info("[Watchdog] Periodic retry from monitor mode (%d min elapsed)", monitorTicks*5/60)
						monitorOnly = false
						restartAttempts = 0
						flatLineCount = 0
						// Force an immediate restart
						audioCapture.Stop()
						time.Sleep(500 * time.Millisecond)
						newCapture, err := audio.NewCapture(audioChunks, selectedDevice)
						if err != nil {
							logger.Error("[Watchdog] Periodic retry failed: %v", err)
							newCapture, err = audio.NewCapture(audioChunks, nil)
						}
						if err == nil {
							audioCapture = newCapture
							logger.Info("[Watchdog] Audio capture restarted (periodic retry)")
							requestSTTReset("audio capture periodic retry")
						} else {
							// Failed again, go back to monitor mode
							monitorOnly = true
						}
					}
					audioMu.Unlock()
					continue
				}

				// Active mode: detect flat audio and restart
				needsRestart := false
				reason := ""
				callbackSilent := false

				if !alive {
					needsRestart = true
					callbackSilent = true
					reason = fmt.Sprintf("callback silent for >%v", watchdogTimeout)
				} else if peakRMS < flatLineRMSThreshold {
					flatLineCount++
					if flatLineCount >= flatLineMax {
						needsRestart = true
						reason = fmt.Sprintf("flat audio (peak RMS %.6f) for %ds", peakRMS, flatLineCount*5)
					}
				}

				// Periodic RMS logging
				if flatLineCount%6 == 0 {
					logger.Debug("[Watchdog] Peak RMS: %.6f (flat: %d/%d, restarts: %d/%d)", peakRMS, flatLineCount, flatLineMax, restartAttempts, maxRestartAttempts)
				}

				if needsRestart {
					restartAttempts++

					if restartAttempts > maxRestartAttempts {
						logger.Info("[Watchdog] %d restart attempts failed - entering monitor mode (will retry in 1 min)", maxRestartAttempts)
						monitorOnly = true
						flatLineCount = 0
						audioMu.Unlock()
						continue
					}

					logger.Info("[Watchdog] Restarting capture: %s (attempt %d/%d)", reason, restartAttempts, maxRestartAttempts)

					// First 2 flat-audio attempts: try lightweight stop/start.
					// If the callback itself stopped, a full rebuild is more reliable
					// than Stop+Start and avoids a 30s dead period on some USB mics.
					if !callbackSilent && restartAttempts <= 2 {
						if err := audioCapture.QuickRestart(); err != nil {
							logger.Error("[Watchdog] Quick restart failed: %v", err)
						} else {
							logger.Info("[Watchdog] Quick restart succeeded")
							requestSTTReset("audio capture quick restart")
							flatLineCount = 0
							audioMu.Unlock()
							continue
						}
					}

					// Full teardown + rebuild
					audioCapture.Stop()
					time.Sleep(500 * time.Millisecond)

					// After 3 failures, try system default mic (only if user didn't explicitly select a device)
					useDevice := selectedDevice
					if restartAttempts >= 3 && selectedDevice != nil && cfg.Audio.DeviceName == "" {
						logger.Info("[Watchdog] Trying system default mic")
						useDevice = nil
					}

					newCapture, err := audio.NewCapture(audioChunks, useDevice)
					if err != nil {
						logger.Error("[Watchdog] Failed to restart: %v", err)
						if useDevice != nil {
							newCapture, err = audio.NewCapture(audioChunks, nil)
						}
					}
					if err == nil {
						audioCapture = newCapture
						logger.Info("[Watchdog] Audio capture restarted successfully")
						requestSTTReset("audio capture restarted")
					}
					flatLineCount = 0
				}
				audioMu.Unlock()
			}
		}
	}()

	// Pause/resume audio capture when the app enters/exits Paused state.
	// Stopping the WASAPI capture device lets audiodg.exe go idle (zero CPU).
	go func() {
		pauseChangeCh := stateManager.Changed()
		for {
			select {
			case <-ctx.Done():
				return
			case <-pauseChangeCh:
				s := stateManager.Get()
				isPaused := s == state.Paused || s == state.MicConflict
				wasPaused := audioPaused.Load()

				if isPaused && !wasPaused {
					// Entering pause - stop capture
					audioMu.Lock()
					audioCapture.Stop()
					audioPaused.Store(true)
					audioMu.Unlock()
					logger.Info("[Pause] Audio capture stopped (audiodg.exe can idle)")
					fmt.Println("[Pause] Audio capture stopped")
				} else if !isPaused && wasPaused {
					// Leaving pause - restart capture
					wakeDeferred := missedWake.Swap(false)
					if wakeDeferred {
						logger.Info("[Pause] Wake event was deferred - waiting 5s for audio subsystem to settle")
						time.Sleep(5 * time.Second)
					}
					audioMu.Lock()
					newCapture, err := audio.NewCapture(audioChunks, selectedDevice)
					if err != nil {
						logger.Error("[Pause] Failed to restart capture: %v", err)
						newCapture, err = audio.NewCapture(audioChunks, nil)
					}
					if err == nil {
						audioCapture = newCapture
						audioPaused.Store(false)
						if wakeDeferred {
							logger.Info("[Pause] Audio capture restarted (post-wake)")
							requestSTTReset("audio capture restarted after paused wake")
						} else {
							logger.Info("[Pause] Audio capture restarted")
							requestSTTReset("audio capture restarted after pause")
						}
						fmt.Println("[Pause] Audio capture restarted")
					} else {
						logger.Error("[Pause] CRITICAL: Cannot restart audio: %v", err)
					}
					audioMu.Unlock()
				}
			}
		}
	}()

	// Poll Windows default capture device every 5s.
	// When the app is on "System Default" mode (cfg.Audio.DeviceName == ""),
	// detect when the user changes the Windows default in Sound Settings and
	// restart capture so the app picks up the new device.
	go func() {
		<-trayMgr.ReadyCh()
		var lastDefault string
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if audioPaused.Load() {
					continue // don't poll devices while capture is stopped
				}
				if cfg.Audio.DeviceName != "" {
					lastDefault = "" // reset so we re-detect when switching back to System Default
					continue
				}

				currentDefault, err := audio.GetWindowsDefaultCaptureName()
				if err != nil {
					continue
				}

				if lastDefault == "" {
					lastDefault = currentDefault
					continue
				}

				if currentDefault != lastDefault {
					logger.Info("[MicSync] Windows default changed: %q -> %q, restarting capture", lastDefault, currentDefault)
					fmt.Printf("[MicSync] Windows default changed to: %s\n", currentDefault)
					lastDefault = currentDefault

					audioMu.Lock()
					audioCapture.Stop()
					time.Sleep(500 * time.Millisecond)
					newCapture, err := audio.NewCapture(audioChunks, nil)
					if err != nil {
						logger.Error("[MicSync] Failed to restart capture: %v", err)
					} else {
						audioCapture = newCapture
						logger.Info("[MicSync] Capture restarted on new default device")
						requestSTTReset("default microphone changed")
					}
					audioMu.Unlock()
				}
			}
		}
	}()

	// Register hotkeys after tray is created
	go registerHotkeys()

	fmt.Printf("[Main] Dictation app v%s ready!\n", Version)
	fmt.Println("[Main] - Ctrl+Alt+X: Pause/Resume")
	fmt.Println("[Main] - Ctrl+Alt+L: Toggle language (EN ↔ DE)")
	fmt.Println("[Main] - Ctrl+Alt+M: Cycle microphone")
	fmt.Println("[Main] - Ctrl+Alt+U: Toggle Ultra Fast mode")

	// Run tray (blocks until exit)
	trayMgr.Run()
}

func registerHotkeys() {
	// Give tray time to initialize
	time.Sleep(500 * time.Millisecond)

	hkMgr := hk.NewManager()

	// Pause/Resume: Ctrl+Alt+X
	logger.Info("[Hotkey] Registering Ctrl+Alt+X for pause/resume...")
	if err := hkMgr.Register(hk.MOD_CONTROL|hk.MOD_ALT, 0x58, "Ctrl+Alt+X (Pause/Resume)", func() {
		logger.Info("[Hotkey] *** CTRL+ALT+X PRESSED *** Toggling pause...")
		newState := stateManager.TogglePause()
		logger.Info("[Hotkey] Pause toggled to: %s", newState)
	}); err != nil {
		logger.Error("[Hotkey] FAILED to register Ctrl+Alt+X: %v - another app may have claimed this hotkey", err)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Alt+X")
	}

	// Language toggle: Ctrl+Alt+L
	logger.Info("[Hotkey] Registering Ctrl+Alt+L for language toggle...")
	if err := hkMgr.Register(hk.MOD_CONTROL|hk.MOD_ALT, 0x4C, "Ctrl+Alt+L (Language Toggle)", func() {
		logger.Info("[Hotkey] Ctrl+Alt+L pressed - toggling language")
		if cfg.Deepgram.Language == "en" {
			cfg.Deepgram.Language = "de"
		} else {
			cfg.Deepgram.Language = "en"
		}
		if err := cfg.Save(); err != nil {
			logger.Error("Failed to save language change: %v", err)
		}
		logger.Info("Language switched to: %s", cfg.Deepgram.Language)
		requestSTTReset("language hotkey")
	}); err != nil {
		logger.Error("[Hotkey] FAILED to register Ctrl+Alt+L: %v - another app may have claimed this hotkey", err)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Alt+L")
	}

	// Microphone cycle: Ctrl+Alt+M
	logger.Info("[Hotkey] Registering Ctrl+Alt+M for mic cycle...")
	if err := hkMgr.Register(hk.MOD_CONTROL|hk.MOD_ALT, 0x4D, "Ctrl+Alt+M (Cycle Mic)", func() {
		logger.Info("[Hotkey] Ctrl+Alt+M pressed - cycling microphone")
		micName := trayMgr.CycleMic()
		logger.Info("[Hotkey] Microphone switched to: %s", micName)
		notify.ShowBalloon("Microphone Switched", micName, 3*time.Second)
	}); err != nil {
		logger.Error("[Hotkey] FAILED to register Ctrl+Alt+M: %v - another app may have claimed this hotkey", err)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Alt+M")
	}

	// Ultra Fast toggle: Ctrl+Alt+U
	logger.Info("[Hotkey] Registering Ctrl+Alt+U for Ultra Fast toggle...")
	if err := hkMgr.Register(hk.MOD_CONTROL|hk.MOD_ALT, 0x55, "Ctrl+Alt+U (Ultra Fast)", func() {
		logger.Info("[Hotkey] Ctrl+Alt+U pressed - toggling Ultra Fast mode")
		enabled := trayMgr.ToggleZeroLatency()
		label := "OFF"
		if enabled {
			label = "ON"
		}
		logger.Info("[Hotkey] Ultra Fast mode: %s", label)
		notify.ShowBalloon("Ultra Fast Mode", label, 3*time.Second)
	}); err != nil {
		logger.Error("[Hotkey] FAILED to register Ctrl+Alt+U: %v - another app may have claimed this hotkey", err)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Alt+U")
	}
}

func vadAndSTTManager(
	ctx context.Context,
	audioRx <-chan []int16,
	textTx chan<- string,
	stateManager *state.Manager,
	cfg *config.Config,
	settingsChangedCh <-chan struct{},
	sim *keyboard.Simulator,
) {
	defer func() {
		if r := recover(); r != nil {
			logger.Error("[VAD] PANIC recovered: %v", r)
			fmt.Printf("[VAD] PANIC recovered: %v\n", r)
		}
	}()

	effectiveSilenceMs := func() uint64 {
		silenceMs := cfg.Audio.SilenceThresholdMs
		if silenceMs == 0 {
			silenceMs = 3000
		}
		if cfg.Audio.ZeroLatency && silenceMs < 2500 {
			// Ultra Fast should stream interim text quickly, not chop utterances after
			// tiny pauses. Short 700ms cutoffs caused missing sentence endings.
			silenceMs = 2500
		}
		return silenceMs
	}
	newConfiguredVAD := func(silenceMs uint64) *audio.VoiceActivityDetector {
		return audio.NewVADWithOptions(cfg.VAD.EnergyThreshold, silenceMs, audio.VADOptions{
			AdaptiveNoise:     cfg.VAD.AdaptiveNoise,
			NoiseMultiplier:   cfg.VAD.NoiseMultiplier,
			MinStartThreshold: cfg.VAD.MinStartThreshold,
			SpeechStartChunks: cfg.VAD.SpeechStartChunks,
		})
	}
	silenceMs := effectiveSilenceMs()
	if cfg.Audio.ZeroLatency {
		logger.Info("[VAD] Zero-latency mode: using reliable silence threshold %dms", silenceMs)
	}
	vad := newConfiguredVAD(silenceMs)

	// Pre-roll ring buffer: keeps last ~500ms of audio so first words aren't lost.
	// VAD needs audio to detect speech - by the time it fires SpeechStarted,
	// the beginning of the utterance has already passed. Pre-roll captures it.
	const audioChunkMs = 80 // capture.go uses 1280 frames at 16kHz
	const preRollSize = 6   // ~480ms at ~80ms/chunk
	preRoll := make([][]int16, 0, preRollSize)
	var silenceStartTime time.Time // when VAD last detected silence (for delayed context reset)

	// Per-app volume ducking for Chrome/Edge
	logger.Info("[AppVolumeDucker] Initializing with duck volume: %.0f%%", cfg.Audio.DuckVolume*100)
	ducker := audio.NewAppVolumeDucker(cfg.Audio.DuckVolume)

	// Ghost speech detection: if VAD says "speaking" but Deepgram returns no
	// actual text for this long, background noise is triggering the VAD.
	// Force-close the STT connection so the icon goes back to standby.
	// This is Deepgram-only: Vosk emits text only after silence/finalization,
	// so applying this to Vosk kills legitimate offline dictation.
	const noTextTimeout = 15 * time.Second
	var lastTextNano atomic.Int64 // tracks when real text was last received

	// After ghost speech is detected, suppress VAD for a cooldown period
	// to prevent immediate re-triggering from the same background noise.
	const ghostCooldown = 30 * time.Second
	var ghostCooldownUntil time.Time

	var sttCtx context.Context
	var sttCancel context.CancelFunc
	var sttAudioTx chan []int16
	var sttDoneCh chan struct{} // closed when STT goroutine exits (connection dead)
	var textBridge chan string  // intermediary channel to track text timing
	var voskEngine *vosk.Engine
	var voskEngineKey string
	var connectionStarted time.Time
	var currentUtteranceHasText atomic.Bool
	var emptyUtteranceCount atomic.Int64
	const maxPersistentConnectionAge = 90 * time.Second

	diagTracker := diagnostics.NewTracker()
	var activeUtterance *diagnostics.Utterance

	currentProviderModel := func() (string, string) {
		if cfg.Offline.Enabled {
			return "Vosk", vosk.ModelKeyForLanguage(cfg.Deepgram.Language, cfg.Offline.ModelSize == "large")
		}
		return "Deepgram", cfg.Deepgram.Model
	}

	currentMicName := func() string {
		if cfg.Audio.DeviceName != "" {
			return cfg.Audio.DeviceName
		}
		return "System Default"
	}

	beginUtterance := func(reuseConnection bool) *diagnostics.Utterance {
		provider, model := currentProviderModel()
		recentReset, resetReason, resetAge := recentSTTReset(30 * time.Second)
		connAge := time.Duration(0)
		if reuseConnection && !connectionStarted.IsZero() {
			connAge = time.Since(connectionStarted)
		}
		return diagTracker.Begin(diagnostics.UtteranceConfig{
			Provider:          provider,
			Model:             model,
			Language:          cfg.Deepgram.Language,
			MicName:           currentMicName(),
			ZeroLatency:       cfg.Audio.ZeroLatency,
			ReusedConnection:  reuseConnection,
			ConnectionAge:     connAge,
			VADThreshold:      float32(vad.LastThreshold()),
			StartRMS:          vad.LastRMS(),
			PreRollChunks:     len(preRoll),
			SaveFailedAudio:   cfg.Diagnostics.SaveFailedAudio,
			RecentReset:       recentReset,
			RecentResetReason: resetReason,
			RecentResetAge:    resetAge,
		})
	}

	sendSTTAudio := func(chunk []int16) {
		if sttAudioTx == nil {
			diagTracker.RecordDropped(activeUtterance)
			return
		}
		select {
		case sttAudioTx <- chunk:
			diagTracker.RecordAudio(activeUtterance, chunk)
		default:
			diagTracker.RecordDropped(activeUtterance)
		}
	}

	endCurrentUtterance := func(reason string) {
		if activeUtterance != nil {
			diagTracker.End(activeUtterance, reason)
			activeUtterance = nil
			return
		}
		diagTracker.EndCurrent(reason)
	}

	scheduleUtteranceEnd := func(u *diagnostics.Utterance, reason string, delay time.Duration) {
		if u == nil {
			return
		}
		diagTracker.EndAfter(u, reason, delay)
		if activeUtterance == u {
			activeUtterance = nil
		}
	}

	closeVoskEngine := func() {
		if voskEngine != nil {
			voskEngine.Close()
			voskEngine = nil
			voskEngineKey = ""
		}
	}

	preloadVoskEngine := func(reason string) {
		if !cfg.Offline.Enabled {
			closeVoskEngine()
			return
		}

		modelKey := vosk.ModelKeyForLanguage(cfg.Deepgram.Language, cfg.Offline.ModelSize == "large")
		if voskEngine != nil && voskEngineKey == modelKey {
			return
		}
		closeVoskEngine()

		if !vosk.IsAvailable() {
			logger.Info("[Vosk] Library not ready; cannot preload offline model %s", modelKey)
			return
		}
		if !vosk.IsModelDownloaded(modelKey) {
			logger.Info("[Vosk] Model %s not downloaded yet; cannot preload", modelKey)
			return
		}

		modelPath, err := vosk.GetModelPath(modelKey)
		if err != nil {
			logger.Error("[Vosk] Failed to resolve model %s: %v", modelKey, err)
			return
		}

		start := time.Now()
		logger.Info("[Vosk] Preloading offline model %s (%s)", modelKey, reason)
		fmt.Printf("[Vosk] Loading offline model %s...\n", modelKey)
		engine, err := vosk.NewEngine(modelPath)
		if err != nil {
			logger.Error("[Vosk] Failed to preload model %s: %v", modelKey, err)
			fmt.Printf("[Vosk] Failed to load offline model: %v\n", err)
			return
		}
		voskEngine = engine
		voskEngineKey = modelKey
		logger.Info("[Vosk] Offline model %s ready in %v", modelKey, time.Since(start).Round(time.Millisecond))
	}

	defer closeVoskEngine()

	// Load Vosk while idle, not after speech starts. Large Vosk models can take
	// many seconds to map into memory; doing that in SpeechStarted leaves the tray
	// stuck in speaking/green and loses the user's audio.
	preloadVoskEngine("startup")

	// Independent meeting/recording detection ticker.
	// Runs every 1s regardless of audio flow so auto-pause works even when
	// the mic is dead, in monitor mode, or the audio channel is nil.
	// The detectors themselves have internal caches (3s meeting, 2s recording)
	// so the actual syscalls are throttled despite the fast ticker.
	meetingTicker := time.NewTicker(1 * time.Second)
	defer meetingTicker.Stop()

	vadChangeCh := stateManager.Changed()

	for {
		// When paused, disable ALL processing (zero CPU)
		var activeAudioRx <-chan []int16
		var activeMeetingTick <-chan time.Time
		if s := stateManager.Get(); s != state.Paused && s != state.MicConflict {
			activeAudioRx = audioRx
			activeMeetingTick = meetingTicker.C
		}

		select {
		case <-ctx.Done():
			endCurrentUtterance("shutdown")
			if sttCancel != nil {
				sttCancel()
			}
			return

		case <-vadChangeCh:
			// Clear stale pre-roll on any state change (e.g., unpause after hours)
			preRoll = preRoll[:0]
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
			sttDoneCh = nil
			textBridge = nil // don't close - bridge goroutine exits via sttCtx
			connectionStarted = time.Time{}
			endCurrentUtterance("settings_changed")
			// Recreate VAD with potentially changed silence/noise settings.
			silenceMs = effectiveSilenceMs()
			if cfg.Audio.ZeroLatency {
				logger.Info("[VAD] Zero-latency mode: using reliable silence threshold %dms", silenceMs)
			}
			vad = newConfiguredVAD(silenceMs)
			stateManager.Set(state.AutoPaused)
			preloadVoskEngine("settings changed")

		case <-activeMeetingTick:
			// Independent meeting/recording check - fires even when mic is dead
			// or audio channel is nil. Disabled entirely when paused (channel is nil).
			currentState := stateManager.Get()
			meetingActive := cfg.Audio.AutoPauseOnMedia && audio.IsMeetingActive()
			recordingActive := cfg.Audio.AutoPauseOnMedia && audio.IsRecordingActive()
			// Only auto-pause on media if ducking is off. When ducking is enabled,
			// the user wants to keep dictating with lowered media volume.
			mediaPlaying := cfg.Audio.AutoPauseOnMedia && !cfg.Audio.DuckEnabled && audio.IsMediaPlaying()

			if meetingActive || recordingActive || mediaPlaying {
				if currentState != state.MediaPaused {
					if recordingActive {
						logger.Info("[Meeting] Screen recording detected via ticker - auto-pausing")
						fmt.Println("[Recording] Screen recording active - pausing dictation")
					} else if mediaPlaying {
						logger.Info("[Media] Audio playback detected via ticker - auto-pausing")
						fmt.Println("[Media] Audio playing - pausing dictation")
					} else {
						logger.Info("[Meeting] Active call detected via ticker - auto-pausing")
						fmt.Println("[Meeting] In a call - pausing dictation")
					}
					// Close any active STT connection
					if sttCancel != nil {
						sttCancel()
						sttCancel = nil
					}
					if sttAudioTx != nil {
						close(sttAudioTx)
						sttAudioTx = nil
					}
					sttDoneCh = nil
					textBridge = nil
					connectionStarted = time.Time{}
					endCurrentUtterance("media_auto_pause")
					vad.Reset()
					preRoll = preRoll[:0]
					stateManager.Set(state.MediaPaused)
				}
			} else if currentState == state.MediaPaused {
				logger.Info("[Media] Media/call/recording ended via ticker - resuming")
				fmt.Println("[Media] Media/call/recording ended - resuming dictation")
				stateManager.Set(state.Active)
			}

		case audioChunk, ok := <-activeAudioRx:
			if !ok {
				return
			}

			currentState := stateManager.Get()

			// Auto-pause during active meetings or screen recordings
			// Note: media playback detection is handled by the meetingTicker only,
			// not here on every audio chunk, because IsMediaPlaying() does heavy
			// COM calls that can hang and block this entire goroutine.
			meetingActive := cfg.Audio.AutoPauseOnMedia && audio.IsMeetingActive()
			recordingActive := cfg.Audio.AutoPauseOnMedia && audio.IsRecordingActive()

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
				logger.Info("[VAD] Speech started (RMS %.4f, threshold %.4f, noise %.4f)", vad.LastRMS(), vad.LastThreshold(), vad.NoiseFloor())
				fmt.Println("[VAD] Speech started")

				// In Ultra Fast mode, reset keyboard context if silence was long enough
				// that we've likely switched contexts (>3s). Short gaps (<3s) keep context
				// so late Deepgram results still get proper spacing.
				if cfg.Audio.ZeroLatency && !silenceStartTime.IsZero() && time.Since(silenceStartTime) > 3*time.Second {
					sim.ResetContext()
					logger.Debug("[VAD] Reset keyboard context after %.1fs silence", time.Since(silenceStartTime).Seconds())
				}

				// Duck Chrome/Edge volume
				if !meetingActive && cfg.Audio.DuckEnabled {
					if err := ducker.Duck(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to duck: %v", err)
					}
				}

				stateManager.Set(state.Speaking)

				// Reset ghost speech timer
				lastTextNano.Store(time.Now().UnixNano())
				currentUtteranceHasText.Store(false)

				// In zero-latency mode, try to reuse the existing persistent connection,
				// but recycle it periodically. Recent failures showed old Deepgram
				// sessions staying open while returning only empty transcripts.
				reuseConnection := false
				if cfg.Audio.ZeroLatency && sttAudioTx != nil && sttDoneCh != nil {
					if !connectionStarted.IsZero() && time.Since(connectionStarted) > maxPersistentConnectionAge {
						logger.Info("[VAD] Persistent connection age %s exceeded %s; reconnecting", time.Since(connectionStarted).Round(time.Millisecond), maxPersistentConnectionAge)
						if sttCancel != nil {
							sttCancel()
							sttCancel = nil
						}
						close(sttAudioTx)
						sttAudioTx = nil
						sttDoneCh = nil
						textBridge = nil
						connectionStarted = time.Time{}
					} else {
						select {
						case <-sttDoneCh:
							// Connection died, need a fresh one
							logger.Info("[VAD] Persistent connection dead, reconnecting")
							sttAudioTx = nil
							sttDoneCh = nil
							textBridge = nil
							connectionStarted = time.Time{}
							if sttCancel != nil {
								sttCancel()
								sttCancel = nil
							}
						default:
							// Connection still alive, reuse it
							reuseConnection = true
							logger.Info("[VAD] Reusing persistent Deepgram connection (zero-latency)")
							fmt.Println("[VAD] Reusing persistent connection")
						}
					}
				}

				activeUtterance = beginUtterance(reuseConnection)

				if !reuseConnection {
					// Choose STT engine
					var engine stt.Engine
					if cfg.Offline.Enabled {
						modelKey := vosk.ModelKeyForLanguage(cfg.Deepgram.Language, cfg.Offline.ModelSize == "large")
						if voskEngine == nil || voskEngineKey != modelKey {
							preloadVoskEngine("speech start")
						}
						if voskEngine != nil && voskEngineKey == modelKey {
							engine = voskEngine
						} else {
							logger.Info("[Vosk] Selected offline model is not ready; not falling back to Deepgram")
							fmt.Println("[Vosk] Offline model not ready; wait for it to load/download or switch to Deepgram")
							if cfg.Audio.DuckEnabled {
								if err := ducker.Restore(); err != nil {
									logger.Error("[AppVolumeDucker] Failed to restore: %v", err)
								}
							}
							endCurrentUtterance("offline_model_not_ready")
							vad.Reset()
							preRoll = preRoll[:0]
							stateManager.Set(state.AutoPaused)
							continue
						}
					}
					if engine == nil {
						engine = deepgram.NewClient(
							cfg.Deepgram.APIKey,
							cfg.Deepgram.Language,
							cfg.Deepgram.Model,
							cfg.Deepgram.Punctuate,
							cfg.Deepgram.SmartFormat,
							cfg.Deepgram.FillerWords,
							cfg.Deepgram.Holdback,
							cfg.Deepgram.Keywords,
							cfg.Deepgram.Keyterms,
							cfg.Audio.ZeroLatency,
						)
					}

					sttAudioTx = make(chan []int16, 100)
					sttCtx, sttCancel = context.WithCancel(ctx)
					sttDoneCh = make(chan struct{})
					connectionStarted = time.Now()

					// Text bridge: forwards text to keyboard simulator while tracking
					// the last time real text was received (for ghost speech detection).
					// It intentionally does NOT exit on sttCtx cancellation: offline Vosk
					// produces its final transcript while handling that cancellation. The
					// STT goroutine owns and closes textBridge after StartStreaming returns.
					bridgeUtterance := activeUtterance
					bridgeTracksCurrentUtterance := cfg.Audio.ZeroLatency && !cfg.Offline.Enabled
					textBridge = make(chan string, 10)
					go func(bridge <-chan string, utterance *diagnostics.Utterance, tracksCurrent bool) {
						for {
							select {
							case text, ok := <-bridge:
								if !ok {
									return
								}
								lastTextNano.Store(time.Now().UnixNano())
								currentUtteranceHasText.Store(true)
								emptyUtteranceCount.Store(0)
								if tracksCurrent {
									diagTracker.RecordText(text)
								} else {
									diagTracker.RecordTextFor(utterance, text)
								}
								select {
								case textTx <- text:
								case <-ctx.Done():
									return
								}
							case <-ctx.Done():
								return
							}
						}
					}(textBridge, bridgeUtterance, bridgeTracksCurrentUtterance)

					go func(e stt.Engine, eCtx context.Context, eAudioRx <-chan []int16, eTx chan string, doneCh chan struct{}, utterance *diagnostics.Utterance, tracksCurrent bool) {
						defer close(doneCh)
						defer close(eTx)
						defer func() {
							if r := recover(); r != nil {
								logger.Error("[STT] PANIC recovered: %v", r)
								fmt.Printf("[STT] PANIC recovered: %v\n", r)
							}
						}()
						if err := e.StartStreaming(eCtx, eAudioRx, eTx); err != nil {
							if tracksCurrent {
								diagTracker.RecordSTTError(err)
							} else {
								diagTracker.RecordSTTErrorFor(utterance, err)
							}
							logger.Error("[STT] Error: %v", err)
							fmt.Printf("[STT] Error: %v\n", err)
						}
					}(engine, sttCtx, sttAudioTx, textBridge, sttDoneCh, bridgeUtterance, bridgeTracksCurrentUtterance)
				}

				// Flush pre-roll buffer (captures audio BEFORE VAD fired)
				if len(preRoll) > 0 {
					logger.Info("[VAD] Flushing %d pre-roll chunks (~%dms)", len(preRoll), len(preRoll)*audioChunkMs)
					for _, chunk := range preRoll {
						sendSTTAudio(chunk)
					}
					preRoll = preRoll[:0]
				}

				// Send the current chunk that triggered SpeechStarted
				sendSTTAudio(audioChunk)

			case audio.Speaking:
				if sttAudioTx != nil {
					// Ghost speech check: no real text for too long = background noise.
					// Deepgram streams interim text while speaking; Vosk does not, so this
					// must stay disabled for offline mode.
					if !cfg.Offline.Enabled {
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
							sttDoneCh = nil
							textBridge = nil // don't close - bridge goroutine exits via sttCtx
							connectionStarted = time.Time{}
							endCurrentUtterance("ghost_speech_timeout")
							vad.Reset()
							preRoll = preRoll[:0]
							stateManager.Set(state.AutoPaused)
							continue
						}
					}

					sendSTTAudio(audioChunk)
				}

			case audio.SilenceDetected:
				fmt.Println("[VAD] Silence detected")

				// Restore Chrome/Edge volume
				if cfg.Audio.DuckEnabled {
					if err := ducker.Restore(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to restore: %v", err)
					}
				}

				if cfg.Audio.ZeroLatency && !cfg.Offline.Enabled {
					// Zero-latency mode: keep connection alive, let Deepgram's
					// endpointing handle finalization naturally. Don't send Finalize
					// as it cuts off words prematurely (e.g., "outstanding" → "out").
					logger.Info("[VAD] Silence detected (RMS %.4f) - keeping persistent connection (zero-latency)", vad.LastRMS())
					if !currentUtteranceHasText.Load() {
						emptyCount := emptyUtteranceCount.Add(1)
						if emptyCount >= 3 {
							logger.Info("[VAD] %d consecutive empty utterances - reconnecting Deepgram", emptyCount)
							if sttCancel != nil {
								sttCancel()
								sttCancel = nil
							}
							if sttAudioTx != nil {
								close(sttAudioTx)
								sttAudioTx = nil
							}
							sttDoneCh = nil
							textBridge = nil
							connectionStarted = time.Time{}
							emptyUtteranceCount.Store(0)
						}
					} else {
						emptyUtteranceCount.Store(0)
					}
					silenceStartTime = time.Now()
					scheduleUtteranceEnd(activeUtterance, "silence_detected", 5*time.Second)
				} else {
					// Normal mode: close STT connection. For Vosk, close the audio
					// channel first and let the recognizer drain buffered audio before
					// FinalResult(); canceling the context first can discard the utterance.
					logger.Info("[VAD] Silence detected - closing STT connection")
					if cfg.Offline.Enabled {
						if sttAudioTx != nil {
							close(sttAudioTx)
							sttAudioTx = nil
						}
						if sttCancel != nil && sttDoneCh != nil {
							cancelFn := sttCancel
							doneCh := sttDoneCh
							go func() {
								select {
								case <-doneCh:
								case <-ctx.Done():
								}
								cancelFn()
							}()
						}
						sttCancel = nil
					} else {
						if sttCancel != nil {
							sttCancel()
							sttCancel = nil
						}
						if sttAudioTx != nil {
							close(sttAudioTx)
							sttAudioTx = nil
						}
					}
					sttDoneCh = nil
					textBridge = nil // don't close - bridge goroutine exits via sttCtx/STT goroutine
					connectionStarted = time.Time{}
					scheduleUtteranceEnd(activeUtterance, "silence_detected", 5*time.Second)
				}

				vad.Reset()
				if !(cfg.Audio.ZeroLatency && !cfg.Offline.Enabled) {
					// Normal mode: reset keyboard context so next session starts fresh.
					// In Ultra Fast mode, Deepgram sends late results after VAD silence
					// that still need proper spacing — don't reset lastChar.
					sim.ResetContext()
				}
				preRoll = preRoll[:0] // Clear pre-roll for next utterance

				if mediaCache := audio.IsMediaPlayingCached(); mediaCache {
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

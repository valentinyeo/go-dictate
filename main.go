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
)

var (
	cfg               *config.Config
	stateManager      *state.Manager
	audioChunks       chan []int16
	transcriptText    chan string
	settingsChangedCh chan struct{}
	ctx               context.Context
	cancel            context.CancelFunc
	audioPaused       atomic.Bool
	missedWake        atomic.Bool
	trayMgr           *tray.Manager
	releaseCapture    func()

	lastSTTResetMu     sync.Mutex
	lastSTTResetAt     time.Time
	lastSTTResetReason string
)

func main() {
	run()
}

func enforceSingleInstance() (uintptr, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	createMutex := kernel32.NewProc("CreateMutexW")
	name, _ := syscall.UTF16PtrFromString("Global\\GoDictateAppMutex")
	handle, _, err := createMutex.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if handle == 0 {
		return 0, err
	}
	if errno, ok := err.(syscall.Errno); ok && errno == 183 {
		syscall.CloseHandle(syscall.Handle(handle))
		return 0, fmt.Errorf("another instance is already running")
	}
	return handle, nil
}

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
	mutexHandle, singleErr := enforceSingleInstance()
	if singleErr != nil {
		fmt.Println("[Main] Another instance is already running. Exiting.")
		os.Exit(0)
	}
	defer syscall.CloseHandle(syscall.Handle(mutexHandle))

	enableEfficiencyMode()

	fmt.Printf("=== Dictation App v%s (%s) ===\n", Version, VersionName)

	logger.RedirectStderr()

	if err := logger.Init(); err != nil {
		fmt.Printf("[Logger] Warning: Failed to initialize logger: %v\n", err)
	}
	defer logger.Close()

	logger.Info("=== Dictation App v%s (%s) Starting ===", Version, VersionName)

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

	stateManager = state.NewManager()
	stateManager.SetPushToTalk(cfg.Audio.PushToTalk)

	audioChunks = make(chan []int16, 100)
	transcriptText = make(chan string, 10)

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

	var audioMu sync.Mutex
	captureChanged := make(chan struct{}, 1)
	fmt.Println("[Main] Starting audio capture...")
	var audioCapture *audio.Capture
	audioPaused.Store(!stateManager.CaptureAllowed())
	streamLifetime := deepgram.NewStreamLifetime(!audioPaused.Load())
	if stateManager.CaptureAllowed() {
		audioCapture, err = audio.NewCapture(audioChunks, selectedDevice)
	}
	if audioCapture == nil && stateManager.CaptureAllowed() && err != nil {
		fmt.Printf("[Main] Failed to start audio capture: %v\n", err)
		time.Sleep(5 * time.Second)
		os.Exit(1)
	}

	defer func() { audioMu.Lock(); audioCapture.Stop(); audioMu.Unlock() }()
	releaseCapture = func() {
		streamLifetime.SetMicrophone(false)
		audioMu.Lock()
		defer audioMu.Unlock()
		audioCapture.Stop()
		audioPaused.Store(true)
		select {
		case captureChanged <- struct{}{}:
		default:
		}
	}

	fmt.Println("[Main] Starting keyboard simulator...")
	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()

	sim := keyboard.NewSimulator()
	if cfg.Keyboard.Injection == "type" {
		sim.SetTypeMode(true)
	}
	logger.Info("[Keyboard] Injection method: %s", cfg.Keyboard.Injection)
	go sim.Run(ctx, transcriptText)

	settingsChangedCh = make(chan struct{}, 1)
	go vadAndSTTManager(ctx, audioChunks, transcriptText, stateManager, cfg, settingsChangedCh, streamLifetime, sim)

	fmt.Println("[Main] Creating system tray...")
	trayMgr = tray.NewManager(stateManager, cfg, micNames, cfg.Audio.DeviceName, Version, VersionName, VersionSummary)

	go func() {
		for range trayMgr.SettingsChangedCh {
			noteSTTReset("tray settings changed")
			select {
			case settingsChangedCh <- struct{}{}:
			default:
			}
		}
	}()

	go func() {
		for deviceName := range trayMgr.MicChangedCh {
			displayName := deviceName
			if displayName == "" {
				displayName = "System Default"
			}
			logger.Info("[Main] Switching microphone to: %s", displayName)

			var newDevice *audio.CaptureDevice
			if deviceName != "" {
				for i, d := range micDevices {
					if d.Name == deviceName {
						newDevice = &micDevices[i]
						break
					}
				}
			}

			audioMu.Lock()
			selectedDevice = newDevice

			if audioPaused.Load() || !stateManager.CaptureAllowed() {
				audioMu.Unlock()
				fmt.Printf("[Main] Microphone will switch to %s on resume\n", displayName)
				continue
			}

			audioCapture.Stop()
			newCapture, err := audio.NewCapture(audioChunks, newDevice)
			if err != nil {
				logger.Error("[Main] Failed to switch microphone: %v", err)
				fmt.Printf("[Main] Failed to switch microphone: %v\n", err)
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

	powerMon := audio.NewPowerMonitor()
	defer powerMon.Stop()

	go func() {
		const watchdogInterval = 5 * time.Second
		const watchdogTimeout = 10 * time.Second
		const flatLineRMSThreshold = 0.0005
		const flatLineMax = 6
		const maxRestartAttempts = 5
		const monitorOnlyLogInterval = 60
		const monitorRetryInterval = 12
		flatLineCount := 0
		restartAttempts := 0
		monitorOnly := false
		monitorTicks := 0

		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return

			case <-powerMon.Wake:
				if audioPaused.Load() {
					logger.Info("[Watchdog] Wake event while paused - flagging for unpause restart")
					missedWake.Store(true)
					continue
				}
				logger.Info("[Watchdog] Wake event received - waiting 5s for audio subsystem to settle")
				time.Sleep(5 * time.Second)
				logger.Info("[Watchdog] Restarting audio capture after wake")
				audioMu.Lock()
				if !stateManager.CaptureAllowed() || stateManager.PushToTalkEnabled() {
					audioMu.Unlock()
					continue
				}
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
				if audioPaused.Load() {
					continue
				}
				audioMu.Lock()
				if audioPaused.Load() || !stateManager.CaptureAllowed() || stateManager.PushToTalkEnabled() {
					audioMu.Unlock()
					continue
				}
				alive := audioCapture.IsAlive(watchdogTimeout)
				peakRMS := audioCapture.PeakRMS()

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

				if monitorOnly {
					monitorTicks++
					if monitorTicks%monitorOnlyLogInterval == 0 {
						logger.Debug("[Watchdog] Still flat (RMS %.6f) - monitoring, %d min elapsed", peakRMS, monitorTicks*5/60)
					}
					if monitorTicks%monitorRetryInterval == 0 {
						logger.Info("[Watchdog] Periodic retry from monitor mode (%d min elapsed)", monitorTicks*5/60)
						monitorOnly = false
						restartAttempts = 0
						flatLineCount = 0
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
							monitorOnly = true
						}
					}
					audioMu.Unlock()
					continue
				}

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

					audioCapture.Stop()
					time.Sleep(500 * time.Millisecond)

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

	pauseChangeCh := stateManager.Changed()
	select {
	case captureChanged <- struct{}{}:
	default:
	}
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-pauseChangeCh:
			case <-captureChanged:
			}
			if missedWake.Swap(false) && !stateManager.PushToTalkEnabled() && stateManager.CaptureAllowed() {
				timer := time.NewTimer(5 * time.Second)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
			{
				audioMu.Lock()
				allowed := stateManager.CaptureAllowed()
				if !allowed && !audioPaused.Load() {
					streamLifetime.SetMicrophone(false)
					audioCapture.Stop()
					audioPaused.Store(true)
					logger.Info("[Capture] Microphone released")
				} else if allowed && audioPaused.Load() {
					drainAudio(audioChunks)
					fresh, err := audio.NewCapture(audioChunks, selectedDevice)
					if err != nil {
						fresh, err = audio.NewCapture(audioChunks, nil)
					}
					if err != nil {
						logger.Error("[Capture] Cannot open microphone: %v", err)
						stateManager.Set(state.Paused)
						notify.ShowBalloon("Microphone unavailable", "Check Sound Settings and resume to retry.", 5*time.Second)
					} else if !stateManager.CaptureAllowed() {
						fresh.Stop()
					} else {
						audioCapture = fresh
						audioPaused.Store(false)
						streamLifetime.SetMicrophone(true)
						logger.Info("[Capture] Microphone opened")
						if !stateManager.PushToTalkEnabled() {
							requestSTTReset("audio capture resumed")
						}
					}
				}
				audioMu.Unlock()
			}
		}
	}()

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
					continue
				}
				if cfg.Audio.DeviceName != "" {
					lastDefault = ""
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
					if !stateManager.CaptureAllowed() || stateManager.PushToTalkEnabled() {
						audioMu.Unlock()
						continue
					}
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

	go registerHotkeys()

	fmt.Printf("[Main] Dictation app v%s ready!\n", Version)
	fmt.Println("[Main] - Ctrl+Alt+X: Pause/Resume")
	fmt.Println("[Main] - Ctrl+Alt+L: Toggle language (EN ↔ DE)")
	fmt.Println("[Main] - Ctrl+Alt+M: Cycle microphone")
	fmt.Println("[Main] - Ctrl+Alt+U: Toggle Ultra Fast mode")

	trayMgr.Run()
}

func registerHotkeys() {
	time.Sleep(500 * time.Millisecond)

	hkMgr := hk.NewManager()
	if err := hkMgr.RegisterHold(ctx, hk.MOD_CONTROL, 0x20, "Ctrl+Space push-to-talk", stateManager.Hold); err != nil {
		logger.Error("[Hotkey] Push-to-talk registration failed: %v", err)
		notify.ShowBalloon("Push-to-talk shortcut unavailable", "Ctrl+Space is used by another app. Free the shortcut and restart Go Dictate.", 8*time.Second)
	} else {
		logger.Info("[Hotkey] Successfully registered Ctrl+Space for push-to-talk")
	}

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
	streamLifetime *deepgram.StreamLifetime,
	sim *keyboard.Simulator,
) {
	defer streamLifetime.Close("shutdown")
	defer func() {
		if r := recover(); r != nil {
			logger.Error("[VAD] PANIC recovered: %v", r)
			fmt.Printf("[VAD] PANIC recovered: %v\n", r)
		}
	}()

	effectiveZeroLatency := func() bool {
		return cfg.Audio.ZeroLatency && !cfg.Audio.BatteryBudgetSaver
	}
	effectiveKeyterms := func() []string {
		if cfg.Audio.BatteryBudgetSaver {
			return nil
		}
		return cfg.Deepgram.Keyterms
	}
	effectiveNoTextTimeout := func() time.Duration {
		if cfg.Audio.BatteryBudgetSaver {
			return 8 * time.Second
		}
		return 15 * time.Second
	}
	effectiveSilenceMs := func() uint64 {
		silenceMs := cfg.Audio.SilenceThresholdMs
		if silenceMs == 0 {
			silenceMs = 3000
		}
		if cfg.Audio.BatteryBudgetSaver && silenceMs > 1500 {
			silenceMs = 1500
		} else if effectiveZeroLatency() && silenceMs < 2500 {
			silenceMs = 2500
		}
		return silenceMs
	}
	newConfiguredVAD := func(silenceMs uint64) *audio.VoiceActivityDetector {
		settings := cfg.VAD.Effective()
		return audio.NewVADWithOptions(settings.EnergyThreshold, silenceMs, audio.VADOptions{
			AdaptiveNoise:     settings.AdaptiveNoise,
			NoiseMultiplier:   settings.NoiseMultiplier,
			MinStartThreshold: settings.MinStartThreshold,
			SpeechStartChunks: settings.SpeechStartChunks,
		})
	}
	newRumbleFilter := func() *audio.HighPassFilter {
		if config.NormalizeNoiseMode(cfg.VAD.NoiseMode) != config.NoiseModeTrain {
			return nil
		}
		return audio.NewHighPassFilter(16000, 120)
	}
	silenceMs := effectiveSilenceMs()
	if effectiveZeroLatency() {
		logger.Info("[VAD] Zero-latency mode: using reliable silence threshold %dms", silenceMs)
	} else if cfg.Audio.BatteryBudgetSaver {
		logger.Info("[Saver] Battery & Budget Saver enabled: silence=%dms, ghost_timeout=%s, keyterms=off, persistent_connection=off",
			silenceMs, effectiveNoTextTimeout())
	}
	vad := newConfiguredVAD(silenceMs)
	rumbleFilter := newRumbleFilter()
	if rumbleFilter != nil {
		settings := cfg.VAD.Effective()
		logger.Info("[VAD] Train mode enabled: 120Hz rumble filter, threshold=%.4f, noise_multiplier=%.1f, start_chunks=%d",
			settings.MinStartThreshold, settings.NoiseMultiplier, settings.SpeechStartChunks)
	}

	const audioChunkMs = 80
	const preRollSize = 6
	preRoll := make([][]int16, 0, preRollSize)
	streamGate := audio.NewStreamGate((1500+audioChunkMs-1)/audioChunkMs, 3)
	var dailyUsage audio.DailyUsage
	usageActive := false
	logUsage := func() {
		if !usageActive {
			return
		}
		usageActive = false
		streamed := time.Duration(streamGate.Sent()*audioChunkMs) * time.Millisecond
		voiced := time.Duration(streamGate.Voiced()*audioChunkMs) * time.Millisecond
		day, streamedToday, voicedToday := dailyUsage.Add(time.Now(), streamed, voiced)
		logger.Info("[Usage] day=%s utterance_streamed=%.1fs utterance_voiced=%.1fs streamed_today=%.1fs voiced_today=%.1fs",
			day, streamed.Seconds(), voiced.Seconds(), streamedToday.Seconds(), voicedToday.Seconds())
	}
	pushPreRoll := func(chunk []int16) {
		if len(preRoll) >= preRollSize {
			copy(preRoll, preRoll[1:])
			preRoll[len(preRoll)-1] = chunk
		} else {
			preRoll = append(preRoll, chunk)
		}
	}
	var silenceStartTime time.Time

	logger.Info("[AppVolumeDucker] Initializing with duck volume: %.0f%%", cfg.Audio.DuckVolume*100)
	ducker := audio.NewAppVolumeDucker(cfg.Audio.DuckVolume)

	const (
		duckHangover     = 700 * time.Millisecond
		duckVoiceFloor   = 0.018
		systemAudioFloor = 0.01
		echoGateRatio    = 0.12
		minEchoGate      = 0.02
	)
	var lastVoiceActivity time.Time

	noTextTimeout := effectiveNoTextTimeout()
	var lastTextNano atomic.Int64

	const ghostCooldown = 30 * time.Second
	var ghostCooldownUntil time.Time

	var sttCtx context.Context
	var sttCancel context.CancelFunc
	defer func() {
		if sttCancel != nil {
			sttCancel()
		}
	}()
	var sttAudioTx chan []int16
	var sttDoneCh chan struct{}
	var textBridge chan string
	var connectionStarted time.Time
	var currentUtteranceHasText atomic.Bool
	var emptyUtteranceCount atomic.Int64

	diagTracker := diagnostics.NewTracker()
	var activeUtterance *diagnostics.Utterance

	currentMicName := func() string {
		if cfg.Audio.DeviceName != "" {
			return cfg.Audio.DeviceName
		}
		return "System Default"
	}

	beginUtterance := func(reuseConnection bool) *diagnostics.Utterance {
		recentReset, resetReason, resetAge := recentSTTReset(30 * time.Second)
		connAge := time.Duration(0)
		if reuseConnection && !connectionStarted.IsZero() {
			connAge = time.Since(connectionStarted)
		}
		return diagTracker.Begin(diagnostics.UtteranceConfig{
			Provider:          "Deepgram",
			Model:             "nova-3",
			Language:          cfg.Deepgram.Language,
			MicName:           currentMicName(),
			ZeroLatency:       effectiveZeroLatency(),
			SaverMode:         cfg.Audio.BatteryBudgetSaver,
			NoiseMode:         config.NormalizeNoiseMode(cfg.VAD.NoiseMode),
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

	meetingTicker := time.NewTicker(1 * time.Second)
	defer meetingTicker.Stop()

	vadChangeCh := stateManager.Changed()

	for {
		if ctx.Err() != nil {
			return
		}
		if stateManager.PushToTalkEnabled() {
			streamLifetime.Close("push_to_talk")
			if sttCancel != nil {
				sttCancel()
				sttCancel = nil
			}
			if sttAudioTx != nil {
				close(sttAudioTx)
				sttAudioTx = nil
			}
			sttDoneCh = nil
			endCurrentUtterance("push_to_talk")
			_ = ducker.Restore()
			vad.Reset()
			preRoll = preRoll[:0]
			runPushToTalk(ctx, audioRx, textTx, stateManager, vadChangeCh, releaseCapture)
			continue
		}
		var activeAudioRx <-chan []int16
		var activeMeetingTick <-chan time.Time
		if s := stateManager.Get(); !stateManager.PushToTalkEnabled() && s != state.Paused && s != state.MicConflict {
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

		case <-sttDoneCh:
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

		case <-vadChangeCh:
			if !stateManager.CaptureAllowed() {
				streamLifetime.SetMicrophone(false)
				vad.Reset()
				logUsage()
				endCurrentUtterance("mic_released")
				_ = ducker.Restore()
			}
			preRoll = preRoll[:0]
			continue

		case <-settingsChangedCh:
			streamLifetime.Close("settings_changed")
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
			textBridge = nil
			connectionStarted = time.Time{}
			endCurrentUtterance("settings_changed")
			silenceMs = effectiveSilenceMs()
			noTextTimeout = effectiveNoTextTimeout()
			if effectiveZeroLatency() {
				logger.Info("[VAD] Zero-latency mode: using reliable silence threshold %dms", silenceMs)
			} else if cfg.Audio.BatteryBudgetSaver {
				logger.Info("[Saver] Battery & Budget Saver enabled: silence=%dms, ghost_timeout=%s, keyterms=off, persistent_connection=off",
					silenceMs, noTextTimeout)
			}
			vad = newConfiguredVAD(silenceMs)
			rumbleFilter = newRumbleFilter()
			if rumbleFilter != nil {
				settings := cfg.VAD.Effective()
				logger.Info("[VAD] Train mode enabled: 120Hz rumble filter, threshold=%.4f, noise_multiplier=%.1f, start_chunks=%d",
					settings.MinStartThreshold, settings.NoiseMultiplier, settings.SpeechStartChunks)
			}
			if stateManager.CaptureAllowed() {
				stateManager.Set(state.AutoPaused)
			}

		case <-activeMeetingTick:
			currentState := stateManager.Get()
			meetingActive := cfg.Audio.AutoPauseOnMedia && audio.IsMeetingActive()
			recordingActive := cfg.Audio.AutoPauseOnMedia && audio.IsRecordingActive()
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
					streamLifetime.Close("media_auto_pause")
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
			if audioPaused.Load() || !stateManager.CaptureAllowed() {
				continue
			}
			if rumbleFilter != nil {
				rumbleFilter.ProcessInPlace(audioChunk)
			}

			currentState := stateManager.Get()

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

			if time.Now().Before(ghostCooldownUntil) {
				pushPreRoll(audioChunk)
				continue
			}

			chunkRMS := audio.ChunkRMS(audioChunk)

			if cfg.Audio.IgnoreSystemAudio && currentState != state.Speaking && currentState != state.PushToTalkRecording {
				if peak := audio.SystemOutputPeak(); peak > systemAudioFloor {
					gate := float64(peak) * echoGateRatio
					if gate < minEchoGate {
						gate = minEchoGate
					}
					if chunkRMS < gate {
						pushPreRoll(audioChunk)
						continue
					}
				}
			}

			if cfg.Audio.DuckEnabled {
				voiceThreshold := vad.LastThreshold()
				if voiceThreshold < duckVoiceFloor {
					voiceThreshold = duckVoiceFloor
				}
				if chunkRMS > voiceThreshold {
					lastVoiceActivity = time.Now()
				}
				if !ducker.IsDucked() {
					if !lastVoiceActivity.IsZero() && time.Since(lastVoiceActivity) < duckHangover {
						if err := ducker.Duck(); err != nil {
							logger.Error("[AppVolumeDucker] Failed to duck: %v", err)
						}
					}
				} else if time.Since(lastVoiceActivity) > duckHangover {
					if err := ducker.Restore(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to restore (hangover): %v", err)
					}
				}
			}

			vadEvent := vad.Process(audioChunk)
			if vad.LastChunkVoiced() && (vadEvent == audio.SpeechStarted || vadEvent == audio.Speaking) {
				streamLifetime.Speech()
			}

			switch vadEvent {
			case audio.SpeechStarted:
				streamGate.Start(len(preRoll) + 1)
				usageActive = true
				logger.Info("[VAD] Speech started (RMS %.4f, threshold %.4f, noise %.4f)", vad.LastRMS(), vad.LastThreshold(), vad.NoiseFloor())
				fmt.Println("[VAD] Speech started")

				if effectiveZeroLatency() && !silenceStartTime.IsZero() && time.Since(silenceStartTime) > 3*time.Second {
					sim.ResetContext()
					logger.Debug("[VAD] Reset keyboard context after %.1fs silence", time.Since(silenceStartTime).Seconds())
				}

				if !meetingActive && cfg.Audio.DuckEnabled {
					if err := ducker.Duck(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to duck: %v", err)
					}
				}

				stateManager.Set(state.Speaking)

				lastTextNano.Store(time.Now().UnixNano())
				currentUtteranceHasText.Store(false)

				reuseConnection := false
				if effectiveZeroLatency() && sttAudioTx != nil && sttDoneCh != nil && streamLifetime.BeginUtterance() {
					select {
					case <-sttDoneCh:
					default:
						reuseConnection = true
						logger.Info("[VAD] Reusing persistent Deepgram connection (zero-latency)")
					}
				}

				activeUtterance = beginUtterance(reuseConnection)

				if !reuseConnection {
					if sttCancel != nil {
						sttCancel()
					}
					if sttAudioTx != nil {
						close(sttAudioTx)
					}
					engine := deepgram.NewClient(
						cfg.Deepgram.APIKey,
						cfg.Deepgram.Language,
						"nova-3",
						cfg.Deepgram.Punctuate,
						cfg.Deepgram.SmartFormat,
						cfg.Deepgram.FillerWords,
						cfg.Deepgram.Holdback,
						effectiveKeyterms(),
						effectiveZeroLatency(),
					)

					sttAudioTx = make(chan []int16, 100)
					streamCtx, streamCancel := context.WithCancel(ctx)
					sttCtx, sttCancel = streamCtx, streamCancel
					if effectiveZeroLatency() {
						engine.SetStreamLifetime(streamCtx, streamLifetime)
					}
					sttDoneCh = make(chan struct{})
					connectionStarted = time.Now()

					bridgeUtterance := activeUtterance
					bridgeTracksCurrentUtterance := effectiveZeroLatency()
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

					go func(e stt.Engine, eCtx context.Context, cancelFn context.CancelFunc, eAudioRx <-chan []int16, eTx chan string, doneCh chan struct{}, utterance *diagnostics.Utterance, tracksCurrent bool) {
						defer cancelFn()
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
					}(engine, sttCtx, streamCancel, sttAudioTx, textBridge, sttDoneCh, bridgeUtterance, bridgeTracksCurrentUtterance)
				}

				if len(preRoll) > 0 {
					logger.Info("[VAD] Flushing %d pre-roll chunks (~%dms)", len(preRoll), len(preRoll)*audioChunkMs)
					for _, chunk := range preRoll {
						sendSTTAudio(chunk)
					}
					preRoll = preRoll[:0]
				}

				sendSTTAudio(audioChunk)

			case audio.Speaking:
				if sttAudioTx != nil {
					lastText := time.Unix(0, lastTextNano.Load())
					if time.Since(lastText) > noTextTimeout {
						streamLifetime.Close("ghost_speech_timeout")
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
						textBridge = nil
						connectionStarted = time.Time{}
						logUsage()
						endCurrentUtterance("ghost_speech_timeout")
						vad.Reset()
						preRoll = preRoll[:0]
						stateManager.Set(state.AutoPaused)
						continue
					}

					for _, chunk := range streamGate.Next(audioChunk, vad.LastChunkVoiced()) {
						sendSTTAudio(chunk)
					}
				}

			case audio.SilenceDetected:
				logUsage()
				fmt.Println("[VAD] Silence detected")

				if cfg.Audio.DuckEnabled {
					if err := ducker.Restore(); err != nil {
						logger.Error("[AppVolumeDucker] Failed to restore: %v", err)
					}
				}

				if effectiveZeroLatency() {
					logger.Info("[VAD] Silence detected (RMS %.4f) - keeping persistent connection (zero-latency)", vad.LastRMS())
					if !currentUtteranceHasText.Load() {
						emptyCount := emptyUtteranceCount.Add(1)
						if emptyCount >= 3 {
							logger.Info("[VAD] %d consecutive empty utterances - reconnecting Deepgram", emptyCount)
							streamLifetime.Close("empty_utterances")
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
					logger.Info("[VAD] Silence detected - closing STT connection")
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
					scheduleUtteranceEnd(activeUtterance, "silence_detected", 5*time.Second)
				}

				vad.Reset()
				if !effectiveZeroLatency() {
					sim.ResetContext()
				}
				preRoll = preRoll[:0]

				if mediaCache := audio.IsMediaPlayingCached(); mediaCache {
					logger.Info("[VAD] Media still playing - returning to MediaPaused")
					stateManager.Set(state.MediaPaused)
				} else {
					stateManager.Set(state.AutoPaused)
				}

			case audio.Silence:
				if len(preRoll) >= preRollSize {
					copy(preRoll, preRoll[1:])
					preRoll[len(preRoll)-1] = audioChunk
				} else {
					preRoll = append(preRoll, audioChunk)
				}
			}
		}
	}
}

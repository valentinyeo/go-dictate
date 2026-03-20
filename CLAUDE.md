# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Critical Rules

- The executable MUST be named `go-dictate.exe`. Do NOT rename it. Start Menu shortcuts and user workflows depend on this name.
- Every build MUST increment the version in `version.go`. The user verifies they have the latest build by checking the version number in the tray menu.
- After making code changes, always build AND launch the app. Don't ask the user to do it manually.
- After every build: stop running instances, wait 3 seconds, start the new exe.
- Always commit and push to GitHub after changes.

## Build Commands

```bash
# Build (requires Go 1.21+, MinGW-w64 GCC, CGO_ENABLED=1)
go build -ldflags="-s -w -H windowsgui" -trimpath -o go-dictate.exe .

# Or use the batch script
build.bat

# Launch
Start-Process 'C:\Users\valen\Documents\GitHub\go-dictate\go-dictate.exe'

# Kill running instances
Stop-Process -Name 'go-dictate' -Force -ErrorAction SilentlyContinue
```

There are no tests in this project. `-H windowsgui` hides the console window; omit it for debug builds with console output.

## Version Tracking Workflow

Current version: **1.8.73 "Ultra Fast"**

1. Update `version.go` (Version, BuildDate, VersionName, VersionSummary)
2. Update `CHANGELOG.md` with new version header and changes
3. Update `internal/tray/manager.go` tooltip, version menu item, and summary line
4. Make code changes
5. Build
6. Commit with format: `vX.X.X: Brief description`
7. Push to GitHub

### Tray Menu Version Pattern

The top of the right-click tray menu MUST always have:
1. **Version item** (top-most): `"vX.X.X - Version Name"` - clickable, opens CHANGELOG.md in default editor
2. **Summary item** (below version): `"  One-line summary of what changed"` - disabled/greyed out, informational only
3. **Separator** - then the rest of the menu

The tooltip string `"Dictation App vX.X.X"` appears in 6 places in manager.go (onReady, handleModeClicks online/offline/error/default, prepareOffline). Update all of them - use `replace_all` when editing.

## Architecture

Windows system tray dictation app: captures microphone audio, detects speech via VAD, streams to either Deepgram (cloud) or Vosk (local) for speech-to-text, types transcribed text into the active window.

### Goroutine Structure

```
main thread (systray.Run - blocks)
  +-- Audio Capture (malgo/WASAPI) -> audioChunks chan []int16 (buf:100)
  +-- vadAndSTTManager goroutine
  |     +-- VAD processes each audio chunk (RMS energy)
  |     +-- SpeechStarted -> opens STT engine (Deepgram or Vosk)
  |     +-- SilenceDetected -> closes STT connection
  |     +-- Ghost speech detection (15s no-text timeout + 30s cooldown)
  |     +-- Media/meeting/recording detection -> auto-pause
  |     +-- Per-app volume ducking (Chrome/Edge/etc)
  +-- Text Bridge goroutine (STT -> keyboard, tracks last-text time for ghost detection)
  +-- Audio Watchdog (5s check, 10s timeout -> restart capture)
  +-- Keyboard Simulator <- transcriptText chan string (buf:10)
  +-- Hotkey listener (internal/hotkey, blocking GetMessageW, single thread)
  +-- Tray menu handlers (settings, mic switch, feature toggles, STT mode)
```

### Key Packages

- **internal/audio/**: WASAPI capture (`malgo`) with watchdog heartbeat, VAD (RMS energy), media detection (IAudioSessionManager2 COM), meeting detection, recording detection (process/window enumeration), volume ducking (IAudioEndpointVolume, ISimpleAudioVolume)
- **internal/stt/**: `Engine` interface - abstraction for speech-to-text backends
- **internal/deepgram/**: WebSocket streaming client with send/receive goroutines, JSON response types, KeepAlive auto-send, interim result diffing. `NewClient` takes `useInterim bool` as last arg. Implements `stt.Engine`.
- **internal/vosk/**: Offline speech-to-text via Vosk. DLL loaded at runtime via syscall (no CGO). Model download manager with progress reporting. Implements `stt.Engine`.
- **internal/hotkey/**: Global hotkey registration using blocking `GetMessageW` (zero CPU when idle). Single OS-thread-locked goroutine handles all hotkeys. Replaced `golang.design/x/hotkey` which polled PeekMessage at 10ms per hotkey.
- **internal/keyboard/**: Windows SendInput Unicode character injection
- **internal/state/**: Thread-safe state machine (Active/Paused/AutoPaused/MediaPaused/Speaking/MicConflict) with change notification channel
- **internal/tray/**: System tray UI, menu items, feature toggles, mic selector, STT mode toggle, autostart registry
  - `icons.go` - Loads 128x128 source PNGs from `icons/` folder, scales to multi-size ICO (16/20/24/32/48), tints by state, theme-aware standby color
  - `manager.go` - Menu construction, event handlers, online/offline toggle, model selection
- **internal/config/**: TOML config at `%APPDATA%\dictation\config.toml`
- **internal/logger/**: File logger at `%APPDATA%\dictation\debug.log`, crash log redirect (`crash.log`) for capturing panics in windowsgui builds

### Icons

Source PNGs in `icons/` folder (128x128, white on transparent, rendered from Fluent UI SVGs):

| File | Icon | Used For | Tint Color |
|------|------|----------|------------|
| `mic-on.png` | Filled chat bubble with sparkle stars | Speaking/active | Green `#10B981` |
| `mic-standby.png` | Outline chat bubble with sparkle stars | Standby/idle | Dark `#202020` (light taskbar) or Light `#D0D0D0` (dark taskbar) |
| `mic-off.png` | Slashed microphone | Paused/off | Red `#EF4444` |
| `mic-media.png` | Same as standby | Media detected | Amber `#F59E0B` |

Theme detection: reads `HKCU\Software\Microsoft\Windows\CurrentVersion\Themes\Personalize\SystemUsesLightTheme` from Windows registry.

### Windows COM Threading

All Windows COM API calls (media detection, meeting detection, ducking) MUST use `runtime.LockOSThread()` / `runtime.UnlockOSThread()`. Go goroutines migrate between OS threads; COM objects are thread-bound. Missing this causes random freezes. See `internal/audio/capture.go:ListCaptureDevices` for the correct pattern.

### State Flow

Active (grey) -> Speaking (green, STT engine open) -> AutoPaused (grey, 3s silence) -> Active (speech detected)

Side states: Paused (red, manual), MediaPaused (amber, meeting/recording detected), MicConflict (red)

Ghost speech: if Speaking for 15s with no text from STT, force-close connection and enter 30s cooldown (VAD suppressed). Prevents background noise (TV, podcasts) from keeping the app stuck in Speaking state.

### Connection Lifecycle

STT connections open only when speech starts and close after silence (normal mode). In Ultra Fast mode, connections persist via KeepAlive between utterances — `sttDoneCh` tracks connection liveness. Deepgram API cost: nova-3 at $0.36/hr (KeepAlive is free). The `vadAndSTTManager` goroutine owns the connection lifecycle.

### Pause Behavior (Zero CPU)

When paused (manual or MicConflict), the app achieves truly zero CPU:
1. **WASAPI capture stopped** - `audioCapture.Stop()` releases the device, `audiodg.exe` goes idle. Controlled by `audioPaused` atomic flag.
2. **VAD select channels nil'd** - Both `activeAudioRx` and `activeMeetingTick` are set to nil, so the select blocks only on `ctx.Done()`, `stateManager.Changed()`, and `settingsChangedCh`.
3. **Watchdog/mic-sync skip** - Check `audioPaused.Load()` and `continue` immediately.
4. **Hotkeys use blocking GetMessageW** - Thread sleeps in kernel, zero polling.

On unpause, capture is restarted with the current `selectedDevice`. Mic changes while paused update `selectedDevice` but defer the actual restart.

A `textBridge` channel sits between the STT engine and keyboard simulator. The bridge goroutine tracks the last-text timestamp for ghost speech detection. NEVER close `textBridge` directly (causes "send on closed channel" panics) - instead set it to nil and let the bridge goroutine exit via its context.

### Offline Mode (Vosk)

- Toggle Online (Deepgram) / Offline (Vosk) via tray menu radio buttons
- Vosk DLL (`libvosk.dll`) auto-downloads from GitHub releases on first use
- Models auto-download on first use: Small (~50MB) or Large (~1.8GB)
- Models stored at `%APPDATA%\dictation\models\`
- DLL loaded at runtime via syscall - no CGO dependency for Vosk
- App works without libvosk.dll (offline mode just won't be available)

### Audio Watchdog

The audio capture callback updates an atomic timestamp on every frame. A watchdog goroutine checks every 5s - if no audio for 10s (e.g. after sleep/wake, USB disconnect), it restarts the capture device. Falls back to system default mic if the selected device fails. Protected by `audioMu sync.Mutex`.

### Recording Detection

Auto-pauses dictation during screen recording. Uses Windows Toolhelp32 process snapshots and EnumWindows:
- **screencast2-go/OBS**: Detects `ffmpeg.exe` process (only spawned during active recording)
- **Loom**: Detects visible Loom windows (hides to tray when idle, shows recording UI when recording)
- Results cached for 2s to avoid expensive syscalls
- `syscall.NewCallback` created ONCE at init (Go has a global limit of ~2000) - never per-call

### Crash Logging

`logger.RedirectStderr()` redirects Windows stderr to `%APPDATA%\dictation\crash.log` via `SetStdHandle`. Since `-H windowsgui` has no console, panics would otherwise vanish silently.

### Ultra Fast Mode (Zero Latency)

Toggle via tray menu checkbox or Ctrl+Alt+U hotkey. Online (Deepgram) only.
- **Persistent connection**: WebSocket stays open between utterances via `{"type":"KeepAlive"}` JSON messages (free, no audio billed). `sttDoneCh` channel detects dead connections for reconnection.
- **Interim results**: `interim_results=true&endpointing=200` added to Deepgram URL. Text streams word-by-word.
- **700ms silence threshold** (vs 3000ms default) for snappy single-word commands.
- **Append-only diffing** in `client.go`: tracks `dgText` (Deepgram's version) and `typedBy` (bytes on screen) separately. NEVER backspace — corrections silently update `dgText` and emit new words beyond `typedBy` at word boundaries. Backspace approach was tried and causes visible gibberish.
- **Keyboard raw mode**: `\x01` prefix on text skips auto-spacing in simulator. Used for interim suffixes.
- Config: `[audio] zero_latency = true/false`

### Global Hotkeys

| Shortcut | Action |
|----------|--------|
| Ctrl+Alt+X | Pause/Resume dictation |
| Ctrl+Alt+L | Toggle language (EN ↔ DE) |
| Ctrl+Alt+M | Cycle microphone |
| Ctrl+Alt+U | Toggle Ultra Fast mode |

### Single Instance

Enforced via Windows named mutex (`Global\GoDictateAppMutex`). Second instances exit immediately. Restart uses `cmd.exe` with ping delay to release the mutex before the new instance starts.

## Config Structure

```toml
[deepgram]
api_key, language (en/de), model (nova-3), punctuate, filler_words, keywords

[audio]
silence_threshold_ms (3000), duck_volume (0.05), duck_enabled, auto_pause_on_media, device_name, zero_latency (false)

[vad]
energy_threshold (0.02)

[offline]
enabled (false), model_size (small/large)
```

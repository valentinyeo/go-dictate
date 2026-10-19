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
# Build (requires Go 1.21+, CGO_ENABLED=1). MinGW gcc is NOT installed -
# Zig (C:\Users\valen\zig) is the C toolchain: CC="zig cc", CXX="zig c++".
# CRITICAL: with CGO the external linker does the final link and -H windowsgui
# is silently lost (exe becomes a console app that opens a terminal window).
# Always also set CGO_LDFLAGS="-Wl,--subsystem,windows".
go build -ldflags="-s -w -H windowsgui" -trimpath -o go-dictate.exe .

# Or use the batch script (sets all of the above)
build.bat

# Launch
Start-Process 'C:\Users\valen\Documents\GitHub\go-dictate\go-dictate.exe'

# Kill running instances
Stop-Process -Name 'go-dictate' -Force -ErrorAction SilentlyContinue
```

On Windows, run `go test ./...` with the Go/CGO and C toolchain environment set by `build.bat`. CI runs `go vet -unsafeptr=false ./...` and `go test ./...` on `windows-latest`. `-H windowsgui` hides the console window; omit it for debug builds with console output.

## Version Tracking Workflow

`version.go` is the single source of truth (Version, BuildDate, VersionName, VersionSummary). Per release:

1. Update `version.go`
2. Add a `CHANGELOG.md` entry
3. Make code changes, build
4. Commit with format `vX.X.X: Brief description`, push

Version strings flow from `version.go` into the tray via `tray.NewManager(...)` and `tooltipText()` — do not hardcode version strings anywhere else. The top of the tray menu MUST keep: clickable version item (`"vX.X.X - Version Name"`, opens CHANGELOG.md), greyed-out one-line summary item, separator.

## Architecture

Windows system tray dictation app: captures microphone audio, detects speech via VAD, streams to either Deepgram (cloud) or Vosk (local) for speech-to-text, types transcribed text into the active window.

### Goroutine Structure

```
main thread (systray.Run - blocks)
  +-- Audio Capture (malgo/WASAPI) -> audioChunks chan []int16 (buf:100)
  +-- vadAndSTTManager goroutine - owns VAD, STT connection lifecycle,
  |       ghost-speech detection, media/meeting/recording auto-pause, ducking
  +-- Text Bridge goroutine (STT -> keyboard, tracks last-text time)
  +-- Audio Watchdog (5s check, 10s timeout -> restart capture)
  +-- Keyboard Simulator <- transcriptText chan string (buf:10)
  +-- Hotkey listener (blocking GetMessageW, single OS-locked thread)
  +-- Tray menu handlers
```

### Key Packages

- **internal/audio/**: WASAPI capture (`malgo`) with watchdog heartbeat, VAD (RMS energy, adaptive noise floor, speech-onset debounce), media/meeting detection (COM), recording detection, volume ducking
- **internal/stt/**: `Engine` interface - abstraction for speech-to-text backends
- **internal/deepgram/**: WebSocket streaming client (implements `stt.Engine`), KeepAlive auto-send, interim result diffing. `NewClient` takes `useInterim bool` as last arg.
- **internal/vosk/**: Offline STT (implements `stt.Engine`). DLL + models auto-download to `%APPDATA%\dictation\` on first use; DLL loaded at runtime via syscall (no CGO). App works without `libvosk.dll` — offline mode just won't be available.
- **internal/hotkey/**: Global hotkeys via blocking `GetMessageW` on one OS-locked thread (zero CPU idle)
- **internal/keyboard/**: Text injection (clipboard paste by default, SendInput fallback)
- **internal/state/**: Thread-safe state machine with change notification channel
- **internal/tray/**: Menu construction/handlers (`manager.go`), state-tinted multi-size icons from `icons/` PNGs with light/dark-theme detection (`icons.go`), autostart registry
- **internal/config/**: TOML config at `%APPDATA%\dictation\config.toml` — see `config.go` for the full structure and defaults
- **internal/logger/**: File logger at `%APPDATA%\dictation\debug.log`; `RedirectStderr()` sends panics to `crash.log` (vital — `-H windowsgui` has no console, panics would vanish silently)
- **internal/diagnostics/**: Per-utterance tracker — logs compact `[UTT n]` start/result lines, optionally saves failed utterances as WAVs under `%APPDATA%\dictation\failed-utterances`

### Windows COM Threading

All Windows COM API calls (media detection, meeting detection, ducking) MUST use `runtime.LockOSThread()` / `runtime.UnlockOSThread()`. Go goroutines migrate between OS threads; COM objects are thread-bound. Missing this causes random freezes. See `internal/audio/capture.go:ListCaptureDevices` for the correct pattern.

### State Flow

Active (grey) -> Speaking (green, STT engine open) -> AutoPaused (grey, 3s silence) -> Active (speech detected)

Side states: Paused (red, manual), MediaPaused (amber, meeting/recording detected), MicConflict (red)

Ghost speech: if Speaking for 15s with no text from STT, force-close connection and enter 30s cooldown (VAD suppressed). Prevents background noise (TV, podcasts) from keeping the app stuck in Speaking state.

### Pause Behavior (Zero CPU)

When paused (manual or MicConflict), the app achieves truly zero CPU:
1. **WASAPI capture stopped** - `audioCapture.Stop()` releases the device. Controlled by `audioPaused` atomic flag.
2. **VAD select channels nil'd** - `activeAudioRx` and `activeMeetingTick` set to nil; select blocks only on ctx/state/settings channels.
3. **Watchdog/mic-sync skip** - check `audioPaused.Load()` and `continue`.
4. **Hotkeys use blocking GetMessageW** - thread sleeps in kernel.

On unpause, capture restarts with the current `selectedDevice`. Mic changes while paused update `selectedDevice` but defer the restart.

A `textBridge` channel sits between the STT engine and keyboard simulator. NEVER close `textBridge` directly (causes "send on closed channel" panics) - set it to nil and let the bridge goroutine exit via its context.

### Audio Watchdog

The capture callback updates an atomic timestamp per frame. A watchdog checks every 5s - if no audio for 10s (sleep/wake, USB disconnect), it restarts the capture device, falling back to the system default mic. Protected by `audioMu sync.Mutex`.

### Recording Detection

Auto-pauses during screen recording via Toolhelp32 process snapshots and EnumWindows (`ffmpeg.exe` for OBS/screencast2-go, visible Loom windows). Results cached 2s. `syscall.NewCallback` created ONCE at init (Go has a global limit of ~2000) - never per-call.

### Ultra Fast Mode (Zero Latency)

Toggle via tray checkbox or Ctrl+Alt+U. Online (Deepgram) only. Config: `[audio] zero_latency`.
- **Persistent connection**: WebSocket stays open between utterances via `{"type":"KeepAlive"}` (free, no audio billed); `sttDoneCh` detects dead connections for reconnect. In normal mode, connections open on speech start and close after silence. `vadAndSTTManager` owns the lifecycle.
- **Interim results**: `interim_results=true&endpointing=200` on the Deepgram URL; text streams word-by-word.
- **700ms silence threshold** (vs 3000ms default).
- **Append-only diffing** in `client.go`: tracks `dgText` (Deepgram's version) and `typedBy` (bytes on screen). NEVER backspace - corrections silently update `dgText` and emit new words beyond `typedBy` at word boundaries. Backspace was tried and causes visible gibberish.
- **Keyboard raw mode**: `\x01` prefix on text skips auto-spacing in the simulator (used for interim suffixes).

### Global Hotkeys

| Shortcut | Action |
|----------|--------|
| Ctrl+Alt+X | Pause/Resume dictation |
| Ctrl+Alt+L | Toggle language (EN ↔ DE) |
| Ctrl+Alt+M | Cycle microphone |
| Ctrl+Alt+U | Toggle Ultra Fast mode |

### Single Instance

Enforced via Windows named mutex (`Global\GoDictateAppMutex`). Second instances exit immediately. Restart uses `cmd.exe` with ping delay to release the mutex before the new instance starts.

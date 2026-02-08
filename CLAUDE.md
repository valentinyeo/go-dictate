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

Example:
```
v1.8.0 - Offline Mode                                          ← clickable → opens CHANGELOG.md
  Added local speech-to-text with Vosk (no internet needed)     ← disabled, greyed out
───────────────────────────────────────────────────────────────
Online (Deepgram)
...
```

The summary should be a brief one-liner describing the main feature/fix of the release.

## Architecture

Windows system tray dictation app: captures microphone audio, detects speech via VAD, streams to Deepgram WebSocket API, types transcribed text into the active window.

### Goroutine Structure

```
main thread (systray.Run - blocks)
  ├── Audio Capture (malgo/WASAPI) → audioChunks chan []int16 (buf:100)
  ├── vadAndDeepgramManager goroutine
  │     ├── VAD processes each audio chunk (RMS energy)
  │     ├── SpeechStarted → opens Deepgram WebSocket, spawns send/receive goroutines
  │     ├── SilenceDetected → closes Deepgram connection
  │     ├── Media/meeting detection (Windows COM) → auto-pause
  │     └── Per-app volume ducking (Chrome/Edge/etc)
  ├── Keyboard Simulator ← transcriptText chan string (buf:10)
  ├── Hotkey listeners (Ctrl+Alt+X pause, Ctrl+Alt+L language)
  └── Tray menu handlers (settings, mic switch, feature toggles)
```

### Key Packages

- **internal/audio/**: WASAPI capture (`malgo`), VAD (RMS energy), media detection (IAudioSessionManager2 COM), meeting detection, volume ducking (IAudioEndpointVolume, ISimpleAudioVolume)
- **internal/deepgram/**: WebSocket streaming client with send/receive goroutines, JSON response types
- **internal/keyboard/**: Windows SendInput Unicode character injection
- **internal/state/**: Thread-safe state machine (Active/Paused/AutoPaused/MediaPaused/Speaking/MicConflict) with change notification channel
- **internal/tray/**: System tray UI, menu items, feature toggles, mic selector, autostart registry
- **internal/config/**: TOML config at `%APPDATA%\dictation\config.toml`
- **internal/logger/**: File logger at `%APPDATA%\dictation\debug.log`

### Windows COM Threading

All Windows COM API calls (media detection, meeting detection, ducking) MUST use `runtime.LockOSThread()` / `runtime.UnlockOSThread()`. Go goroutines migrate between OS threads; COM objects are thread-bound. Missing this causes random freezes. See `internal/audio/capture.go:ListCaptureDevices` for the correct pattern.

### State Flow

Active (grey) → Speaking (blue, Deepgram open) → AutoPaused (grey, 3s silence) → Active (speech detected)

Side states: Paused (red, manual), MediaPaused (amber, meeting detected), MicConflict (red)

### Connection Lifecycle

Deepgram connections open only when speech starts and close after silence. This minimizes API cost (nova-2 at $0.36/hr). The `vadAndDeepgramManager` goroutine owns the connection lifecycle. When paused, the audio channel is set to nil in the select to achieve zero CPU usage.

### Single Instance

Enforced via Windows named mutex (`Global\GoDictateAppMutex`). Second instances exit immediately.

## Config Structure

```toml
[deepgram]
api_key, language (en/de), model (nova-3), punctuate, filler_words, keywords

[audio]
silence_threshold_ms (3000), duck_volume (0.2), duck_enabled, auto_pause_on_media, device_name

[vad]
energy_threshold (0.02)
```

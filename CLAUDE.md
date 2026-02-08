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

Current version: **1.8.4 "Chat Sparkle Icons"**

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

The tooltip string `"Dictation App vX.X.X"` appears in 3 places in manager.go (onReady, handleModeClicks online, prepareOffline). Update all of them - use `replace_all` when editing.

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
  |     +-- Media/meeting detection (Windows COM) -> auto-pause
  |     +-- Per-app volume ducking (Chrome/Edge/etc)
  +-- Keyboard Simulator <- transcriptText chan string (buf:10)
  +-- Hotkey listeners (Ctrl+Alt+X pause, Ctrl+Alt+L language)
  +-- Tray menu handlers (settings, mic switch, feature toggles, STT mode)
```

### Key Packages

- **internal/audio/**: WASAPI capture (`malgo`), VAD (RMS energy), media detection (IAudioSessionManager2 COM), meeting detection, volume ducking (IAudioEndpointVolume, ISimpleAudioVolume)
- **internal/stt/**: `Engine` interface - abstraction for speech-to-text backends
- **internal/deepgram/**: WebSocket streaming client with send/receive goroutines, JSON response types. Implements `stt.Engine`.
- **internal/vosk/**: Offline speech-to-text via Vosk. DLL loaded at runtime via syscall (no CGO). Model download manager with progress reporting. Implements `stt.Engine`.
- **internal/keyboard/**: Windows SendInput Unicode character injection
- **internal/state/**: Thread-safe state machine (Active/Paused/AutoPaused/MediaPaused/Speaking/MicConflict) with change notification channel
- **internal/tray/**: System tray UI, menu items, feature toggles, mic selector, STT mode toggle, autostart registry
  - `icons.go` - Loads 128x128 source PNGs from `icons/` folder, scales to multi-size ICO (16/20/24/32/48), tints by state, theme-aware standby color
  - `manager.go` - Menu construction, event handlers, online/offline toggle, model selection
- **internal/config/**: TOML config at `%APPDATA%\dictation\config.toml`
- **internal/logger/**: File logger at `%APPDATA%\dictation\debug.log`

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

Side states: Paused (red, manual), MediaPaused (amber, meeting detected), MicConflict (red)

### Connection Lifecycle

STT connections open only when speech starts and close after silence. This minimizes Deepgram API cost (nova-3 at $0.36/hr). The `vadAndSTTManager` goroutine owns the connection lifecycle. When paused, the audio channel is set to nil in the select to achieve zero CPU usage.

### Offline Mode (Vosk)

- Toggle Online (Deepgram) / Offline (Vosk) via tray menu radio buttons
- Vosk DLL (`libvosk.dll`) auto-downloads from GitHub releases on first use
- Models auto-download on first use: Small (~50MB) or Large (~1.8GB)
- Models stored at `%APPDATA%\dictation\models\`
- DLL loaded at runtime via syscall - no CGO dependency for Vosk
- App works without libvosk.dll (offline mode just won't be available)

### Single Instance

Enforced via Windows named mutex (`Global\GoDictateAppMutex`). Second instances exit immediately. Restart uses `cmd.exe` with ping delay to release the mutex before the new instance starts.

## Config Structure

```toml
[deepgram]
api_key, language (en/de), model (nova-3), punctuate, filler_words, keywords

[audio]
silence_threshold_ms (3000), duck_volume (0.2), duck_enabled, auto_pause_on_media, device_name

[vad]
energy_threshold (0.02)

[offline]
enabled (false), model_size (small/large)
```

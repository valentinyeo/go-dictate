# Changelog

All notable changes to the Dictation App will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.8.9] - 2026-02-08 "Icon Polish"

### Fixed
- **Sparkle stars clipped on right edge** - SVG viewBox was exactly 32x32 but sparkle paths extended to ~31.8px. Added 1px padding on all sides (`viewBox="-1 -1 34 34"`) so anti-aliasing renders fully.

### Changed
- **Black app icon** - Start Menu / taskbar / exe icon is now black instead of green, matching standard Windows icon style.

---

## [1.8.8] - 2026-02-08 "App Icon"

### Changed
- **Chat sparkle app icon** - Updated the embedded exe icon and Start Menu shortcut to use the green chat-sparkle icon (multi-size: 16, 32, 48, 256px)
- Updated Windows resource version info to 1.8.8

---

## [1.8.7] - 2026-02-08 "Ducking Guard"

### Fixed
- **Volume ducking ignoring disabled config** - `Restore()` was called unconditionally on every silence event regardless of `duck_enabled` setting. Now both `Duck()` and `Restore()` are gated behind the config flag. When ducking is disabled, neither call runs.

---

## [1.8.6] - 2026-02-08 "Chat X Icon"

### Changed
- **Chat bubble with X for paused/off** - Replaced slashed mic with Fluent UI chat-dismiss icon (chat bubble with X). All tray icons now use the chat bubble theme.

---

## [1.8.5] - 2026-02-08 "Simplified Icons"

### Changed
- **Simplified icon usage** - All states now use the same filled chat-sparkle icon, just tinted differently
  - Standby: black/white (theme-aware)
  - Speaking: green
  - Paused: red slashed mic (unchanged)
  - Media: amber

---

## [1.8.4] - 2026-02-08 "Chat Sparkle Icons"

### Changed
- **New chat-with-sparkles icons** - Replaced microphone icons with Fluent UI chat-with-sparkles design
  - Filled chat bubble with sparkle stars for speaking/active state (green tint)
  - Outline chat bubble with sparkle stars for standby state (theme-aware grey)
  - Kept slashed mic icon for paused/off state (red tint)
  - Better visual distinction between "AI dictation active" vs "paused"

---

## [1.8.3] - 2026-02-08 "Fluent Icons"

### Changed
- **Microsoft Fluent UI microphone icons** - Replaced custom icons with official Fluent UI System Icons
  - Filled mic for speaking (green)
  - Outline mic for standby (theme-aware grey)
  - Slashed mic for paused (red)
  - 128x128 source PNGs rendered from SVGs for maximum detail
- **Multi-size ICO** (16, 20, 24, 32, 48) for crisp display at any DPI
- **Theme-aware standby icon** - Detects Windows taskbar theme via registry. Dark icon on light taskbar, light icon on dark taskbar

## [1.8.2] - 2026-02-08 "Crisp Icons" (reverted)

## [1.8.1] - 2026-02-08 "Offline Fix"

### Fixed
- **Vosk DLL download URL** - v0.3.50 release has no assets; switched to v0.3.45 which has the actual win64 zip
- **Restart broken by single-instance mutex** - Old process held the mutex while new process tried to start, causing it to exit immediately. Now uses cmd.exe with a delay so old process exits first, releasing the mutex before the new instance launches.

## [1.8.0] - 2026-02-08 "Offline Mode"

### Added
- **Offline dictation with Vosk** - Speech-to-text without internet using local models
  - Toggle between Online (Deepgram) and Offline (Vosk) via tray menu
  - Two model sizes: Small (~50 MB, fast) and Large (~1.8 GB, more accurate)
  - Models auto-download on first use with progress shown in tray menu
  - Vosk DLL auto-downloaded on first use (~10 MB)
  - Falls back to Deepgram if model not ready
  - Language switching works in offline mode (downloads new model if needed)
- **STT Engine interface** - Abstracted speech-to-text backend behind `stt.Engine` interface
  - Both Deepgram and Vosk implement the same interface
  - Easy to add more backends in the future
- **Vosk DLL loaded at runtime** - No CGO dependency, DLL loaded via syscall
  - App works without libvosk.dll (offline mode just won't be available)
  - DLL searched next to exe, then system PATH

### Changed
- Renamed `vadAndDeepgramManager` to `vadAndSTTManager` (handles both engines)
- Config validation skips API key check when offline mode is enabled
- New `[offline]` config section: `enabled` and `model_size`

### Technical Details
- New: `internal/stt/engine.go` - STT Engine interface
- New: `internal/vosk/dll.go` - Runtime DLL loading via syscall (no CGO)
- New: `internal/vosk/engine.go` - Vosk STT engine implementation
- New: `internal/vosk/model.go` - Model registry, download manager, DLL downloader
- New: `internal/vosk/types.go` - Vosk JSON result types
- Modified: `internal/config/config.go` - Added OfflineConfig struct
- Modified: `internal/tray/manager.go` - Online/Offline radio toggle, model size submenu
- Modified: `main.go` - STT engine abstraction, vosk integration

---

## [1.7.1] - 2026-02-06 "Stability Fix"

### Fixed
- **CRITICAL: COM threading violations causing random freezes** - All Windows COM calls now lock the goroutine to its OS thread with `runtime.LockOSThread()`. Previously, Go could migrate goroutines between threads mid-COM-call, corrupting COM state and causing hangs. Affected: media detection (every 5ms), meeting detection, volume ducking.
- **Deepgram WebSocket ReadMessage blocking forever** - When context is cancelled, the websocket connection is now explicitly closed, unblocking `ReadMessage()`. Previously, leaked goroutines accumulated after each speech/silence cycle with network issues.
- **Deepgram done channel double-close panic** - Used `sync.Once` to prevent `close(done)` from being called twice when context cancellation and read error happen simultaneously.
- **Deepgram receive loop break bug** - `break` on CloseMessage only exited the `if` block, not the `for` loop. Changed to `return nil` so the function exits properly.
- **Meeting cache data race** - Added `sync.Mutex` to `meetingCache` (was unprotected, unlike `mediaCache`).
- **Kernel32 DLL created on every call** - Cached `kernel32.dll` procs at package level instead of creating new `LazyDLL` objects on every `getProcessName()` call (called hundreds of times per second).

### Improved
- **5x faster keyboard typing** - Reduced per-character delay from 10ms to 2ms, preventing transcript backpressure from blocking Deepgram receive loop.

### Technical Details
- Modified: `internal/audio/media_detector.go` - Added `runtime.LockOSThread()` to `checkMediaPlayback()`
- Modified: `internal/audio/meeting_detector.go` - Added `runtime.LockOSThread()` to `checkActiveMeetingAudio()`, mutex to cache, cached kernel32 procs
- Modified: `internal/audio/ducking.go` - Added `runtime.LockOSThread()` to `Duck()` and `Restore()`
- Modified: `internal/audio/app_volume.go` - Added `runtime.LockOSThread()` to `Duck()` and `Restore()`
- Modified: `internal/deepgram/client.go` - sync.Once for done channel, conn.Close() on context cancel, fixed break→return
- Modified: `internal/keyboard/simulator.go` - Reduced typing delay 10ms→2ms

---

## [1.7.0] - 2026-02-06 "Feature Toggles"

### Added
- **Tray menu feature toggles** - Toggle settings without editing config.toml
  - Audio Ducking (off by default - disabled until fixed)
  - Auto-pause in Meetings
  - Punctuation
  - Remove Filler Words
- **Instant settings** - Language, punctuation, and filler word changes take effect immediately
  - No longer need to restart the app after changing settings
  - Active Deepgram connection is closed and VAD resets on settings change
- **Clickable version** - Click version number in tray menu to open CHANGELOG.md

### Fixed
- **Settings required app restart** - Language switch and other Deepgram settings now take effect on the next speech burst without restarting

### Technical Details
- Added: `SettingsChangedCh` channel in tray manager to signal VAD goroutine
- Modified: `vadAndDeepgramManager` - Listens for settings changes, closes active Deepgram connection and resets VAD
- Added: `DuckEnabled` bool in config to explicitly toggle ducking on/off
- Added: `handleFeatureToggles()` in tray manager for checkbox menu items
- Modified: Hotkey language toggle also signals settings changed

---

## [1.6.0] - 2026-02-06 "Microphone Selector"

### Added
- **Microphone selector** in tray right-click menu
  - Lists all available capture devices
  - "System Default" option plus all enumerated microphones
  - Hot-switching: changes mic without restarting the app
  - Selection persisted in config.toml (`device_name` field)
- **Embedded application icon** - exe now has a green microphone icon
  - Uses go-winres for Windows resource embedding
  - Includes version info and manifest in the executable
- **Start Menu entry** - "Go Dictate" shortcut in Start Menu
- **Fixed executable name** - `go-dictate.exe` no longer includes version number
  - Start Menu shortcuts survive rebuilds

### Fixed
- **Device enumeration crash** - WASAPI needs COM initialized on the calling thread
  - Added `runtime.LockOSThread()` and `CoInitializeEx` in `ListCaptureDevices()`
- **Capture Stop() crash** - Fixed `Free()` being called before `Uninit()`
- **CGo pointer violation** - Used `DeviceID.Pointer()` instead of `unsafe.Pointer(&id)`

### Technical Details
- Added: `ListCaptureDevices()` in audio/capture.go with COM initialization
- Modified: `NewCapture()` accepts optional `*CaptureDevice` for device selection
- Added: Microphone submenu with radio-button behavior in tray manager
- Added: `MicChangedCh` channel for signaling mic changes to main.go
- Added: `winres/` directory with icon and Windows resource metadata

---

## [1.5.0] - 2026-02-05 "Per-App Volume Ducking"

### Changed
- **Per-app volume ducking** - Ducks individual app volumes instead of master volume
  - Targets: Chrome, Edge, Firefox, Brave, Opera, VLC, Screenbox, Spotify
  - Uses Windows IAudioSessionManager2 COM interface
  - More precise control than master volume ducking
- **Meeting detection** - Auto-pauses during active calls (Teams, Zoom, Slack, Discord, Skype)
  - Only pauses during active audio sessions, not just when app is running

### Technical Details
- Added: `internal/audio/app_volume.go` - Per-app volume control via IAudioSessionManager2
- Added: `internal/audio/meeting_detector.go` - Active call detection via audio sessions
- Modified: `main.go` - Uses per-app ducking instead of master volume

---

## [1.3.3] - 2026-02-05 "Speech Overrides Media"

### Changed
- **Restored auto-pause + auto-ducking combination** - The way it was meant to work!
  - Media playing → Auto-pauses transcription (prevents transcribing video audio)
  - You start speaking → Overrides pause, ducks volume to 20%, starts transcribing YOUR speech
  - You stop speaking → Restores volume, resumes pause if media still playing
  - Renamed MediaDetected back to MediaPaused (it's actually pausing now)

### Fixed
- **CRITICAL: Couldn't dictate while media playing** - Fixed stale state variable
  - `currentState` was read at loop start but not updated after state changes
  - When transitioning MediaPaused → Speaking, audio wasn't forwarded to Deepgram
  - Now forwards audio based on Deepgram connection status, not stale state variable

### Technical Details
- Modified: `main.go` - Restored auto-pause behavior with VAD override
  - Media detection auto-pauses but VAD continues monitoring
  - SpeechStarted overrides MediaPaused, ducks volume, starts transcribing
  - SilenceDetected returns to MediaPaused if media still playing
- Fixed: `main.go` - Removed stale currentState check in Speaking handler (line 248-257)
- Modified: `internal/state/manager.go` - Renamed MediaDetected → MediaPaused

---

## [1.3.2] - 2026-02-05 "5ms Media Detection"

### Improved
- **Much faster media detection** - Reduced cache from 25ms to 5ms
  - Checks media status 200 times per second (was 40 times/sec)
  - Near-instant transition when video/audio stops playing
  - Faster return to Active state for quicker dictation readiness

### Technical Details
- Modified: `internal/audio/media_detector.go` - Reduced cacheDuration from 25ms to 5ms

---

## [1.3.1] - 2026-02-05 "Fixed Feedback Loop"

### Fixed
- **CRITICAL: Audio feedback loop** - Fixed ducking triggered by video audio through microphone
  - VAD was detecting video audio through mic as "speech", causing continuous ducking
  - Now only ducks volume when media is NOT playing (prevents feedback loop)
  - When media detected while speaking, immediately restores volume and closes Deepgram connection
  - Volume now properly restores when you stop speaking

### Technical Details
- Modified: `main.go` - Added conditional ducking logic (line 207-209)
  - Only calls `Duck()` when `currentState != state.MediaDetected`
- Modified: `main.go` - Added media detection while speaking handler (lines 191-208)
  - Restores volume immediately when media starts during speech
  - Closes Deepgram connection to prevent transcribing video audio
  - Transitions to MediaDetected state

---

## [1.3.0] - 2026-02-05 "Auto-Ducking"

### Added
- **Auto-ducking Windows volume** - Automatically reduces system volume to 20% when you speak
  - Allows dictation even while videos/music are playing
  - Restores volume when you stop talking
  - Configurable via `duck_volume` in config.toml (default: 0.2 = 20%)
  - Uses Windows IAudioEndpointVolume API for system master volume control

### Changed
- **Removed auto-pause on media** - Now uses auto-ducking instead of pausing
  - Old behavior: paused transcription when media detected
  - New behavior: ducks volume and continues transcribing
  - Simpler and more seamless experience
  - No more interruption when watching videos
- **Renamed MediaPaused → MediaDetected** - Media icon now just visual indicator
  - Amber icon shows when media is playing (visual feedback only)
  - Does not block transcription anymore

### Removed
- `auto_pause_on_media` config option - feature replaced by auto-ducking

### Technical Details
- Implemented: `internal/audio/ducking.go` - Windows IAudioEndpointVolume API integration
  - `Duck()` method reduces volume to configured level (saves original volume)
  - `Restore()` method returns volume to original level
  - Thread-safe with mutex protection
  - Proper COM initialization and resource cleanup
- Modified: `main.go` - Removed auto-pause logic (lines 191-220)
  - Simplified media detection to visual indicator only
  - Audio processing continues even when media is detected
- Modified: `internal/state/manager.go` - Renamed MediaPaused → MediaDetected
  - Updated state constants, String(), and IconColor() methods
- Updated: `version.go` - Bumped to v1.3.0
- Updated: `internal/tray/manager.go` - Updated tooltip and menu version
- Updated: `CHANGELOG.md` - Documented major feature change

---

## [1.2.10] - 2026-02-05 "25ms Media Detection"

### Improved
- **Even faster media resume** - Reduced cache to 25ms (was 100ms)
  - Checks media status up to 40 times per second
  - Near-instant resume when video stops playing

### Technical Details
- Modified: `internal/audio/media_detector.go` - Reduced cache duration from 100ms to 25ms

---

## [1.2.9] - 2026-02-05 "Snappy Media Resume"

### Improved
- **Much faster media resume** - App now resumes within ~100ms after media stops
  - Added 100ms cache to media detection (was checking on every audio chunk)
  - Drastically reduced CPU usage from repeated Windows COM API calls
  - Snappier transition from MediaPaused → Active state

### Technical Details
- Modified: `internal/audio/media_detector.go` - Added caching with 100ms TTL
- New: `checkMediaPlayback()` - Internal function for actual Windows API check
- Media status now cached and only refreshed every 100ms instead of every audio chunk

---

## [1.2.8] - 2026-02-05 "Media Icon + Fix"

### Added
- **New MediaPaused state** - Distinct state for when media/YouTube is playing
- **New media icon** (`mic-media.png`) - Amber/orange icon shows when paused due to media
  - Makes it immediately clear WHY the app is paused
  - Different from grey standby icon (AutoPaused after silence)

### Fixed
- **Media resume issue** - App now reliably comes out of media-paused state
  - Changed condition to check for MediaPaused state directly
  - More reliable than checking AutoPaused + config flag
  - App properly transitions: MediaPaused → Active when video stops

### Technical Details
- Added: `state.MediaPaused` - New app state for media playback detection
- Modified: `internal/state/manager.go` - Added MediaPaused state and icon mapping
- Modified: `internal/tray/icons.go` - Added "media" color case for mic-media.png
- Modified: `main.go` - Use MediaPaused instead of AutoPaused when media detected

---

## [1.2.7] - 2026-02-05 "Fixed Standby Bug"

### Fixed
- **Critical bug: App stuck in standby state** - Fixed issue where app wouldn't come out of AutoPaused state
  - When media playback was detected while Deepgram connection was open, the connection was never closed
  - This left the connection hanging and prevented new speech detection
  - Now properly closes Deepgram connection and restores audio ducking when media is detected
  - App now correctly transitions from AutoPaused → Active → Speaking when you start talking again

### Technical Details
- Modified: `main.go:191-206` - Added Deepgram connection cleanup when media playback detected
- Closes both the Deepgram context and audio channel
- Restores audio ducking to prevent audio conflicts

---

## [1.2.6] - 2026-02-05 "Restart Menu Option"

### Added
- **Restart menu option** in system tray
  - Right-click tray icon and select "🔄 Restart" to restart the application
  - Useful for quickly restarting if something goes wrong
  - Located between Pause/Resume and Exit menu items

### Technical Details
- Modified: `internal/tray/manager.go` - Added restart menu item and handler
- New function: `restartApplication()` - Handles graceful restart with 500ms delay

---

## [1.2.5] - 2026-02-05 "Ctrl+Alt+X Restored"

### Changed
- **Pause hotkey restored to Ctrl+Alt+X** (from Ctrl+Shift+P)
  - Simpler key combination without Shift
  - Works reliably with golang.design/x/hotkey implementation
  - Updated all UI elements and documentation to reflect the change

### Technical Details
- Modified: `main.go` - Changed pause hotkey from Ctrl+Shift+P to Ctrl+Alt+X
- Modified: `internal/tray/manager.go` - Updated tooltip and menu text
- Modified: `version.go` - Updated to v1.2.5

---

## [1.2.1] - 2026-02-05 "Auto-Pause & Hotkeys (Fixed)"

### Fixed
- **Windows Core Audio API implementation** - Fixed compilation errors in media detection
  - Changed from naive browser process detection to proper audio session monitoring
  - Uses IAudioSessionManager2 to enumerate actual audio sessions on speakers
  - Filters out own process ID to avoid false positives
  - Now correctly detects when YouTube, videos, or any media is playing
- Fixed syscall type issues that prevented v1.2.0 from compiling properly

### Technical Details
- Fixed: `internal/audio/media_detector.go` - Proper COM interface calls with correct argument types
- All syscall.Syscall6 calls now use proper uintptr type casting

---

## [1.2.0] - 2026-02-05 "Auto-Pause & Hotkeys"

### Added
- **Auto-pause on media playback** - Automatically pauses dictation when YouTube or other media is playing
  - Prevents transcribing video/audio content
  - Configurable via `auto_pause_on_media` in config.toml
  - Detects Chrome, Edge, Firefox browser processes as media indicators
- **Pause/Resume hotkey** - Press **Ctrl+Alt+Shift+P** to quickly pause/resume dictation
  - No need to click tray icon
  - Works system-wide from any app
- **Multi-hotkey support** - Hotkey manager now supports multiple global hotkeys
  - Ctrl+Alt+Shift+L: Language toggle (English/German)
  - Ctrl+Alt+Shift+P: Pause/Resume toggle

### Changed
- Refactored hotkey manager to support multiple hotkeys simultaneously
- Improved VAD loop to check for media playback before processing audio

### Technical Details
- Added: `internal/audio/media_detector.go` - Windows API media detection
- Modified: `internal/hotkey/hotkey.go` - Multi-hotkey support with sync.Mutex
- Modified: `internal/config/config.go` - Added AutoPauseOnMedia config option
- Modified: `main.go` - Integrated media detection and pause hotkey

---

## [1.1.0] - 2026-02-05 "Dashboard & PNG Icons"

### Added
- **Open Deepgram Dashboard** menu item in system tray
  - Opens https://console.deepgram.com/ for easy access to usage/billing
  - Located between "Open Debug Log" and separator before "Exit"
- **PNG Icon Support** - Icons now load from `icons/` folder at runtime
  - `icons/mic-on.png` - Speaking state (blue/green)
  - `icons/mic-off.png` - Paused state (red)
  - `icons/mic-standby.png` - Standby state (grey)
  - Runtime loading supports multiple paths (IDE and built exe)
- **Version Tracking** - Added `version.go` with version constants
- **This Changelog** - Now tracking all changes between versions

### Changed
- Icon system refactored from programmatic generation to runtime PNG loading
- Icons now use Fluent UI design language
- Simplified icon loading logic with fallback path support

### Technical Details
- Modified: `internal/tray/manager.go` - Added dashboard menu item handler
- Modified: `internal/tray/icons.go` - Switched from ICO generation to PNG loading
- Added: `version.go` - Version tracking
- Added: `CHANGELOG.md` - This file

### Cost Optimization Notes
- Current model: nova-2 ($0.36/hr - 20% cheaper than nova-3)
- Silence threshold: 3000ms (optimized from 5000ms)
- Connection lifecycle properly managed with context cancellation
- Connections open only when speaking, close after silence

---

## [1.0.0] - Previous

### Initial Features
- Real-time speech-to-text using Deepgram API
- System tray icon with state indicators
- Voice Activity Detection (VAD)
- Auto-pause after silence detection
- Audio ducking during dictation
- Keyboard simulation for text insertion
- Language toggle (English/German)
- Auto-start on boot option
- Configuration file management
- Debug logging

### Supported States
- **Active** - Waiting for speech (grey icon)
- **Speaking** - Actively transcribing (blue/green icon)
- **Paused** - User manually paused (red icon)
- **AutoPaused** - Paused after silence (grey icon)
- **MicConflict** - Microphone in use by another app (red icon)

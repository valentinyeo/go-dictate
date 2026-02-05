# Changelog

All notable changes to the Dictation App will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

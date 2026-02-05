# Changelog

All notable changes to the Dictation App will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

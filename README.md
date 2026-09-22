# Go Dictation App

A Windows system tray dictation application that uses Deepgram's speech-to-text API to transcribe your voice and type it into any active window.

## Push to talk

Right-click the tray icon and choose **Push to talk** or **Always on** near the top of the menu. The current mode is checked and your choice is saved. Hold **Ctrl+Space**, allow a brief moment for the microphone to open, and speak. Release the shortcut to stop capture and insert the final transcript. Release Ctrl too so it cannot affect pasting. Wait for the tray tooltip to say "Push-to-talk ready" before starting another recording.

The microphone stays closed between recordings, including after restarting the app. Speech profiles preserve this choice. To configure it directly, set `push_to_talk = true` under `[audio]` in `config.toml` and restart.

Push-to-talk overrides Ultra Fast streaming and automatic meeting/VAD gating for each deliberate hold. Text appears after release; microphone startup and network finalization add some latency. Ctrl+Alt+X pauses the app and cancels a recording. Resuming returns to push-to-talk standby. Disabling Push to Talk restores continuous listening. A hold ends automatically after five minutes.

Battery & Budget Saver alone still captures audio continuously to detect speech. Use Push to Talk to release the microphone between recordings. The actual battery saving depends on the microphone and Windows audio driver.

## Features

- **Real-time transcription**: Streams audio to Deepgram API for fast, accurate transcription
- **Voice Activity Detection**: Automatically detects speech and silence
- **Auto-pause**: Pauses after 3 seconds of silence to save API credits
- **System tray integration**: Simple UI with pause/resume, language toggle, and settings
- **Low resource usage**: <50 MB RAM, <5% CPU during transcription
- **Autostart support**: Option to start on Windows boot

## Requirements

- Windows 10/11
- Microphone
- Deepgram API key (get one free at https://console.deepgram.com/)

## Installation

1. **Download** or build the application:
   ```bash
   .\build.bat
   ```

2. **Run** the application for the first time:
   ```bash
   go-dictate.exe
   ```
   This will create a default config file at `%APPDATA%\dictation\config.toml`

3. **Configure** your API key:
   - Right-click the tray icon and select "Settings"
   - Replace `YOUR_API_KEY_HERE` with your actual Deepgram API key
   - Save and close the file
   - Restart the application

## Usage

### Basic Operation

1. **Start dictating**: The app automatically starts in listening mode (grey icon)
2. **Speak**: When you speak, the icon turns blue and transcription begins
3. **Pause**: After 3 seconds of silence, it auto-pauses (grey icon)
4. **Manual pause**: Click the tray icon or right-click → Pause

### Tray Menu

- **Pause/Resume**: Toggle dictation on/off
- **Language**: Switch between English and German
- **Start on Boot**: Enable/disable autostart
- **Settings**: Open configuration file
- **Exit**: Close the application

### Icon Colors

- **Grey**: Listening/idle
- **Blue**: Actively transcribing
- **Red**: Paused or microphone conflict

## Configuration

Edit `%APPDATA%\dictation\config.toml`:

```toml
[deepgram]
api_key = "your_api_key"
language = "en"  # en or de
model = "nova-2"

[audio]
silence_threshold_ms = 3000  # Auto-pause delay
duck_volume = 0.2           # Not implemented yet

[vad]
energy_threshold = 0.02  # Speech detection sensitivity
```

## Building from Source

### Prerequisites

- Go 1.21 or later
- GCC (for CGO, required by malgo)
  - Install MinGW-w64 or TDM-GCC

### Build Steps

```bash
# Install dependencies
go mod download

# Build release version
go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .

# Or use the build script
build.bat
```

### Build Flags Explained

- `-s -w`: Strip debug symbols (smaller binary)
- `-H windowsgui`: No console window on launch
- `-trimpath`: Remove file paths from binary

## Architecture

```
main.go                     # Entry point, goroutine orchestration
├── audio/
│   ├── capture.go         # Malgo-based microphone capture
│   ├── vad.go            # Voice activity detection
│   └── ducking.go        # Audio ducking (stub)
├── deepgram/
│   ├── client.go         # WebSocket streaming client
│   └── types.go          # API response types
├── keyboard/
│   └── simulator.go      # Win32 SendInput wrapper
├── state/
│   └── manager.go        # Thread-safe state machine
├── tray/
│   └── manager.go        # System tray UI
└── config/
    └── config.go         # TOML configuration
```

## Troubleshooting

### "No microphone found"
- Check that your microphone is connected and enabled
- Verify it's set as the default recording device in Windows

### "API key not set"
- Open Settings from the tray menu
- Add your Deepgram API key to the config file
- Restart the application

### No text appears when speaking
- Verify your microphone is working (test in Windows Sound settings)
- Check that the icon turns blue when you speak
- Ensure your API key is valid and has credits

### Build errors with CGO
- Install MinGW-w64 or TDM-GCC
- Ensure `gcc` is in your PATH
- Set `CGO_ENABLED=1` environment variable

## Performance

- **Binary size**: ~4 MB (release build)
- **RAM usage**: <50 MB idle, ~50 MB during transcription
- **CPU usage**: <5% during active transcription
- **Network**: ~16 kbps upload while speaking (16kHz mono audio)

## Credits

This is a Go port of the original [dictation-rs](https://github.com/valen/dictation-rs) Rust application, maintaining identical functionality with idiomatic Go patterns.

## License

MIT License - See LICENSE file for details

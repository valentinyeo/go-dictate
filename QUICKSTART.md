# Quick Start Guide

## Prerequisites

### 1. Install Go

Download and install Go 1.21 or later from https://go.dev/dl/

Verify installation:
```bash
go version
```

### 2. Install MinGW-w64 (for CGO)

The `malgo` audio library requires CGO, which needs a C compiler.

**Option A: Chocolatey (recommended)**
```bash
choco install mingw
```

**Option B: Manual Install**
1. Download from https://www.mingw-w64.org/downloads/
2. Install to `C:\mingw64`
3. Add `C:\mingw64\bin` to your PATH

Verify installation:
```bash
gcc --version
```

### 3. Get a Deepgram API Key

1. Sign up at https://console.deepgram.com/
2. Create a new API key
3. Copy the key (you'll need it later)

## Building the Application

```bash
# Navigate to the project directory
cd C:\Users\valen\Documents\GitHub\go-dictate

# Download dependencies
go mod download

# Build the application
go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .

# Or use the build script
build.bat
```

## First Run

1. **Start the application**:
   ```bash
   dictation.exe
   ```

2. **Configure your API key**:
   - The app will create a config file at `%APPDATA%\dictation\config.toml`
   - Right-click the tray icon → Settings
   - Replace `YOUR_API_KEY_HERE` with your actual Deepgram API key
   - Save and close

3. **Restart the application**:
   ```bash
   dictation.exe
   ```

## Verification

1. The grey microphone icon should appear in your system tray
2. Open Notepad or any text editor
3. Click in the text area
4. Speak into your microphone
5. The icon should turn blue while you're speaking
6. After 3 seconds of silence, your speech should appear as text

## Troubleshooting Build Issues

### "gcc: command not found"
- Install MinGW-w64 (see Prerequisites)
- Add MinGW to your PATH
- Restart your terminal

### "package github.com/gen2brain/malgo: C source files not allowed"
- Set environment variable: `set CGO_ENABLED=1`
- Ensure gcc is installed and in PATH

### "go: command not found"
- Install Go (see Prerequisites)
- Restart your terminal to refresh PATH

### Module download issues
```bash
# Clear module cache and retry
go clean -modcache
go mod download
```

## Usage Tips

- **Pause dictation**: Click the tray icon or press the menu → Pause
- **Change language**: Right-click tray → Language
- **Adjust sensitivity**: Edit `%APPDATA%\dictation\config.toml` and change `energy_threshold`
- **Change auto-pause delay**: Edit `silence_threshold_ms` in config

## Next Steps

- See [README.md](README.md) for full documentation
- Check [config.sample.toml](config.sample.toml) for all configuration options
- Report issues at the GitHub repository

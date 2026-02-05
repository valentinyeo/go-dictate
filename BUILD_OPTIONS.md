# Build Options Guide

This document explains all the ways you can build and distribute the Go Dictation App.

## Overview

You have **3 build options**:

1. **Local Development Build** - Fast iteration during development
2. **GitHub Actions (CI/CD)** - Automated builds and releases
3. **NSIS Installer** - Professional Windows installer

---

## Option 1: Local Development Build ⚡

**Best for:** Daily development, testing, quick iterations

### Prerequisites
- Go 1.21+
- MinGW-w64 (for CGO)

### Quick Build
```bash
cd C:\Users\valen\Documents\GitHub\go-dictate

# One-command setup and build
setup-and-build.bat

# Or manual build
go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .
```

### Output
- `dictation.exe` (~4 MB)
- Ready to run immediately

### Pros
- ✅ Instant feedback
- ✅ Full debugging capability
- ✅ No external dependencies

### Cons
- ❌ Requires local setup
- ❌ Manual distribution

---

## Option 2: GitHub Actions (Automated) 🤖

**Best for:** Continuous integration, automated releases, team collaboration

### Setup

Just push to GitHub - workflows are already configured!

### Workflows Included

#### A. Build Workflow (`.github/workflows/build.yml`)
- **Triggers:** Push to main/develop, Pull Requests
- **Runs:** On every commit
- **Output:** Build artifact (dictation.exe)
- **Purpose:** Verify builds work

#### B. Release Workflow (`.github/workflows/release.yml`)
- **Triggers:** Git tags (v1.0.0), Manual
- **Runs:** When you create a release
- **Output:** ZIP with exe + docs
- **Purpose:** Create GitHub releases

#### C. Release with Installer (`.github/workflows/release-with-installer.yml`)
- **Triggers:** Git tags (v1.0.0), Manual
- **Runs:** When you create a release
- **Output:**
  - NSIS installer (.exe)
  - Portable ZIP
  - Checksums
- **Purpose:** Professional releases

### How to Create a Release

**Method 1: Git Tag**
```bash
git tag v1.0.0
git push origin v1.0.0
```

**Method 2: GitHub UI**
1. Go to Actions → "Release with Installer"
2. Click "Run workflow"
3. Enter version (e.g., v1.0.0)
4. Click "Run workflow"

### What You Get

After workflow completes:
- ✅ `dictation-v1.0.0-windows-installer.exe` (NSIS installer)
- ✅ `dictation-v1.0.0-windows-portable.zip` (portable version)
- ✅ `checksums.txt` (SHA256 hashes)
- ✅ Automatic GitHub Release created
- ✅ Release notes generated

### Pros
- ✅ Fully automated
- ✅ Reproducible builds
- ✅ Professional releases
- ✅ No local setup needed for releases

### Cons
- ❌ Slower than local builds
- ❌ Requires GitHub repository

---

## Option 3: NSIS Installer 📦

**Best for:** Professional distribution, end-user installation

### Prerequisites
- Built `dictation.exe`
- NSIS installed

### Install NSIS
```bash
# Chocolatey
choco install nsis

# Winget
winget install NSIS.NSIS

# Or download from https://nsis.sourceforge.io/
```

### Build Installer Locally

```bash
# 1. Build the app first
build.bat

# 2. Build the installer
build-installer.bat
```

### Output
- `DictationSetup-1.0.0.exe` (~5 MB)

### What the Installer Does

**Installation:**
- ✅ Copies files to `C:\Program Files\Dictation App`
- ✅ Creates Start Menu shortcuts
- ✅ Creates desktop shortcut (optional)
- ✅ Adds to Add/Remove Programs
- ✅ Creates config in `%APPDATA%\dictation`
- ✅ Adds uninstaller

**Uninstallation:**
- ✅ Removes all files
- ✅ Removes shortcuts
- ✅ Asks about keeping config
- ✅ Cleans registry entries
- ✅ Removes autostart entry

### Installer Features

- One-click installation
- Professional wizard UI
- Start Menu integration
- Desktop shortcut option
- Clean uninstallation
- Config preservation option
- Add/Remove Programs entry

### Customization

Edit `installer.nsi` to change:
- `APP_NAME` - Application name
- `APP_VERSION` - Version number
- `APP_PUBLISHER` - Your name/company
- Installation directory
- Shortcuts created
- Files included

### Pros
- ✅ Professional user experience
- ✅ Easy for end-users
- ✅ Start Menu integration
- ✅ Clean uninstallation

### Cons
- ❌ Requires NSIS
- ❌ Extra build step
- ❌ Larger file size

---

## Recommended Build Strategy 🎯

### For Development
```bash
# Use local builds for speed
setup-and-build.bat
```

### For Testing
```bash
# Build locally, test manually
build.bat
dictation.exe
```

### For Releases
```bash
# Use GitHub Actions for automation
git tag v1.0.0
git push origin v1.0.0

# GitHub Actions will automatically:
# 1. Build the app
# 2. Create NSIS installer
# 3. Create portable ZIP
# 4. Generate checksums
# 5. Create GitHub Release
```

### For Manual Distribution
```bash
# Build everything locally
build.bat
build-installer.bat

# Distribute:
# - DictationSetup-1.0.0.exe (for installation)
# - dictation.exe (for portable use)
```

---

## Build Comparison

| Feature              | Local Build | GitHub Actions | NSIS Installer |
|----------------------|-------------|----------------|----------------|
| Speed                | ⚡ Fast     | 🐌 Slower      | 🐌 Slower      |
| Automation           | ❌ Manual   | ✅ Automatic   | ❌ Manual      |
| User Experience      | ⭐⭐        | ⭐⭐⭐         | ⭐⭐⭐⭐⭐      |
| Professional Look    | ⭐⭐        | ⭐⭐⭐⭐       | ⭐⭐⭐⭐⭐      |
| Distribution Ready   | ⭐⭐        | ⭐⭐⭐⭐⭐     | ⭐⭐⭐⭐⭐      |
| Setup Required       | Go + MinGW  | None           | NSIS           |

---

## File Outputs Summary

### Local Build
```
dictation.exe                           # Main executable
```

### GitHub Actions
```
dictation-v1.0.0-windows-installer.exe  # NSIS installer
dictation-v1.0.0-windows-portable.zip   # Portable version
checksums.txt                           # SHA256 hashes
```

### NSIS Installer Contents
```
DictationSetup-1.0.0.exe               # Installer

When installed:
C:\Program Files\Dictation App\
├── dictation.exe
├── config.sample.toml
├── README.md
├── QUICKSTART.md
└── Uninstall.exe

%APPDATA%\dictation\
└── config.toml

Start Menu:
├── Dictation App.lnk
├── README.lnk
├── Configuration.lnk
└── Uninstall.lnk
```

---

## Quick Commands Reference

```bash
# Local development build
setup-and-build.bat

# Manual build
go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .

# Build installer
build-installer.bat

# Create release (triggers GitHub Actions)
git tag v1.0.0
git push origin v1.0.0

# Manual workflow trigger
# Go to GitHub → Actions → "Release with Installer" → Run workflow
```

---

## Troubleshooting

### Build fails
- Ensure Go 1.21+ installed
- Ensure MinGW-w64 installed
- Run `go mod download`
- Check `CGO_ENABLED=1`

### Installer build fails
- Install NSIS
- Ensure `dictation.exe` exists
- Check `installer.nsi` syntax

### GitHub Actions fails
- Check workflow logs
- Verify `go.mod` is committed
- Ensure all source files are pushed

---

## Next Steps

1. **Start with local builds** during development
2. **Test the NSIS installer** locally
3. **Set up GitHub Actions** for automated releases
4. **Create your first release** when ready

Choose the option that best fits your workflow! 🚀

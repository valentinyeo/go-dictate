@echo off
echo ================================================
echo  Go Dictation App - Setup and Build
echo ================================================
echo.

where go >nul 2>nul
if %errorlevel% neq 0 (
    echo [ERROR] Go is not installed!
    echo Please install Go from: https://go.dev/dl/
    echo Or run: winget install GoLang.Go
    pause
    exit /b 1
)

echo [OK] Go found:
go version
echo.

where gcc >nul 2>nul
if %errorlevel% neq 0 (
    echo [ERROR] GCC (MinGW) is not installed!
    echo Please install MinGW-w64:
    echo   - Chocolatey: choco install mingw
    echo   - Winget: winget install mingw-w64
    pause
    exit /b 1
)

echo [OK] GCC found:
gcc --version | findstr "gcc"
echo.

echo [SETUP] Enabling CGO...
set CGO_ENABLED=1
set GOOS=windows
set GOARCH=amd64
echo.

echo [SETUP] Downloading dependencies...
go mod download
if %errorlevel% neq 0 (
    echo [ERROR] Failed to download dependencies
    pause
    exit /b 1
)
echo.

echo [SETUP] Organizing dependencies...
go mod tidy
echo.

echo [BUILD] Building release version...
echo Command: go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .
go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .

if %errorlevel% neq 0 (
    echo.
    echo [ERROR] Build failed!
    echo.
    echo Troubleshooting:
    echo 1. Ensure MinGW-w64 is properly installed
    echo 2. Restart your terminal
    echo 3. Try: set CGO_ENABLED=1
    echo 4. Check go.mod exists
    pause
    exit /b 1
)

echo.
echo ================================================
echo  Build Successful!
echo ================================================
echo.
echo Output: dictation.exe
dir dictation.exe | findstr "dictation.exe"
echo.
echo Next steps:
echo 1. Run: dictation.exe
echo 2. Right-click tray icon ^> Settings
echo 3. Add your Deepgram API key
echo 4. Restart the app
echo.
pause

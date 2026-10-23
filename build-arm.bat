@echo off
echo Building go-dictate for Windows ARM64...

where zig >nul 2>nul || set PATH=C:\Users\valen\zig;%PATH%

set GOOS=windows
set GOARCH=arm64
set CGO_ENABLED=1
set CC=zig cc -target aarch64-windows-gnu
set CXX=zig c++ -target aarch64-windows-gnu

set CGO_LDFLAGS=-Wl,--subsystem,windows

go build -ldflags="-s -w -H windowsgui" -trimpath -o go-dictate.exe .
if errorlevel 1 goto failed

echo.
echo Build successful! ARM64
echo Output: go-dictate.exe
for /f "tokens=*" %%a in ('powershell -NoProfile -Command "[regex]::Match((Get-Content '%~dp0version.go' -Raw), 'Version\s*=\s*.([0-9.]+)').Groups[1].Value"') do set VERSION=%%a
if not defined VERSION goto done
copy /Y go-dictate.exe "dictation-v%VERSION%-arm64.exe" >nul
echo Versioned copy: dictation-v%VERSION%-arm64.exe
:done
echo.
echo To run: go-dictate.exe
exit /b 0

:failed
echo.
echo Build failed! Ensure zig is on PATH: winget install zig.zig
exit /b 1

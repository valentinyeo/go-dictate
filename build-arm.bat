@echo off
echo Building go-dictate for Windows ARM64...

REM Add LLVM MinGW ARM64 to PATH if present (winget: MartinStorsjo.LLVM-MinGW.UCRT)
set "MINGW_ARM64=%LOCALAPPDATA%\Microsoft\WinGet\Packages\MartinStorsjo.LLVM-MinGW.UCRT_Microsoft.Winget.Source_8wekyb3d8bbwe\llvm-mingw-20251216-ucrt-aarch64\bin"
if exist "%MINGW_ARM64%\gcc.exe" set "PATH=%MINGW_ARM64%;%PATH%"

set GOOS=windows
set GOARCH=arm64
set CGO_ENABLED=1

REM Requires ARM64 C compiler on PATH (LLVM MinGW above, or MSYS2 ARM64)
go build -ldflags="-s -w -H windowsgui" -trimpath -o go-dictate.exe .

if %errorlevel% == 0 (
    echo.
    echo Build successful! (ARM64)
    echo Output: go-dictate.exe
    echo.
    for /f "tokens=*" %%a in ('powershell -NoProfile -Command "(Get-Content version.go) -match 'Version\s*=\s*\"([^\"]+)\"'; $Matches[1]"') do set VERSION=%%a
    if defined VERSION (
        copy /Y go-dictate.exe "dictation-v%VERSION%-arm64.exe" >nul
        echo Versioned copy: dictation-v%VERSION%-arm64.exe
    )
    echo.
    echo To run: go-dictate.exe
) else (
    echo.
    echo Build failed! Ensure ARM64 GCC is on PATH (e.g. MSYS2 UCRT aarch64).
    exit /b 1
)

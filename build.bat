@echo off
echo Building go-dictate...

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=1

REM Build with optimization flags and version info
go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .

if %errorlevel% == 0 (
    echo.
    echo Build successful!
    echo Output: dictation.exe
    echo.
    REM Copy to versioned backup
    for /f "tokens=*" %%a in ('powershell -Command "Get-Content version.go | Select-String 'Version.*=.*\"(.*)\"' | ForEach-Object { $_.Matches.Groups[1].Value }"') do set VERSION=%%a
    if defined VERSION (
        copy /Y dictation.exe "dictation-v%VERSION%.exe" >nul
        echo Versioned copy: dictation-v%VERSION%.exe
    )
    echo.
    echo To run: dictation.exe
) else (
    echo.
    echo Build failed!
    exit /b 1
)

@echo off
echo Building go-dictate...

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=1

REM Build with optimization flags
go build -ldflags="-s -w -H windowsgui" -trimpath -o dictation.exe .

if %errorlevel% == 0 (
    echo.
    echo Build successful!
    echo Output: dictation.exe
    echo.
    echo To run: dictation.exe
) else (
    echo.
    echo Build failed!
    exit /b 1
)

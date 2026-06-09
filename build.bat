@echo off
echo Building go-dictate...

set GOOS=windows
set GOARCH=amd64
set CGO_ENABLED=1

REM MinGW gcc is not installed; Zig provides the C toolchain for CGO (malgo).
set PATH=C:\Users\valen\zig;%PATH%
set CC=zig cc
set CXX=zig c++

REM With CGO the external linker produces the final exe and -H windowsgui is
REM silently lost (binary becomes a console app). Force the GUI subsystem.
set CGO_LDFLAGS=-Wl,--subsystem,windows

REM Build with optimization flags and version info
go build -ldflags="-s -w -H windowsgui" -trimpath -o go-dictate.exe .

if %errorlevel% == 0 (
    echo.
    echo Build successful!
    echo Output: go-dictate.exe
    echo.
    REM Copy to versioned backup
    for /f "tokens=*" %%a in ('powershell -Command "Get-Content version.go | Select-String 'Version.*=.*\"(.*)\"' | ForEach-Object { $_.Matches.Groups[1].Value }"') do set VERSION=%%a
    if defined VERSION (
        copy /Y go-dictate.exe "dictation-v%VERSION%.exe" >nul
        echo Versioned copy: dictation-v%VERSION%.exe
    )
    echo.
    echo To run: go-dictate.exe
) else (
    echo.
    echo Build failed!
    exit /b 1
)

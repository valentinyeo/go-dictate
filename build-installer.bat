@echo off
echo ================================================
echo  Building NSIS Installer
echo ================================================
echo.

where makensis >nul 2>nul
if %errorlevel% neq 0 (
    echo [ERROR] NSIS not found!
    echo.
    echo Please install NSIS:
    echo   Download: https://nsis.sourceforge.io/Download
    echo   Or use Chocolatey: choco install nsis
    echo   Or use winget: winget install NSIS.NSIS
    echo.
    pause
    exit /b 1
)

echo [OK] NSIS found:
makensis /VERSION
echo.

if not exist "dictation.exe" (
    echo [ERROR] dictation.exe not found!
    echo Please build the application first:
    echo   build.bat
    echo.
    pause
    exit /b 1
)

echo [OK] dictation.exe found
echo.

if not exist "LICENSE" (
    echo Creating placeholder LICENSE file...
    echo MIT License > LICENSE
    echo. >> LICENSE
    echo Copyright ^(c^) 2025 >> LICENSE
    echo. >> LICENSE
    echo Permission is hereby granted... >> LICENSE
    echo (Add full license text as needed^) >> LICENSE
    echo.
)

echo [BUILD] Creating installer...
makensis installer.nsi

if %errorlevel% neq 0 (
    echo.
    echo [ERROR] Installer build failed!
    pause
    exit /b 1
)

echo.
echo ================================================
echo  Installer built successfully!
echo ================================================
echo.

for %%F in (DictationSetup-*.exe) do (
    echo Installer: %%F
    echo Size: %%~zF bytes
    echo.
)

echo Ready to distribute!
echo.
pause

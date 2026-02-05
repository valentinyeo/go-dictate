; Go Dictation App - NSIS Installer Script
; Requires NSIS 3.0+ (https://nsis.sourceforge.io/)

!define APP_NAME "Dictation App"
!define APP_VERSION "1.0.0"
!define APP_PUBLISHER "Your Name"
!define APP_EXE "dictation.exe"
!define APP_REGKEY "Software\${APP_NAME}"

; Include Modern UI
!include "MUI2.nsh"

; General Settings
Name "${APP_NAME}"
OutFile "DictationSetup-${APP_VERSION}.exe"
InstallDir "$PROGRAMFILES64\${APP_NAME}"
InstallDirRegKey HKLM "${APP_REGKEY}" "InstallPath"
RequestExecutionLevel admin
ShowInstDetails show
ShowUnInstDetails show

; Version Info
VIProductVersion "${APP_VERSION}.0"
VIAddVersionKey "ProductName" "${APP_NAME}"
VIAddVersionKey "CompanyName" "${APP_PUBLISHER}"
VIAddVersionKey "FileDescription" "${APP_NAME} Installer"
VIAddVersionKey "FileVersion" "${APP_VERSION}"
VIAddVersionKey "ProductVersion" "${APP_VERSION}"
VIAddVersionKey "LegalCopyright" "© 2025 ${APP_PUBLISHER}"

; MUI Settings
!define MUI_ABORTWARNING
!define MUI_ICON "${NSISDIR}\Contrib\Graphics\Icons\modern-install.ico"
!define MUI_UNICON "${NSISDIR}\Contrib\Graphics\Icons\modern-uninstall.ico"
!define MUI_WELCOMEFINISHPAGE_BITMAP "${NSISDIR}\Contrib\Graphics\Wizard\nsis3-metro.bmp"
!define MUI_UNWELCOMEFINISHPAGE_BITMAP "${NSISDIR}\Contrib\Graphics\Wizard\nsis3-metro.bmp"

; Installer Pages
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES

; Finish Page with options
!define MUI_FINISHPAGE_RUN "$INSTDIR\${APP_EXE}"
!define MUI_FINISHPAGE_RUN_TEXT "Launch ${APP_NAME}"
!define MUI_FINISHPAGE_SHOWREADME "$INSTDIR\README.md"
!define MUI_FINISHPAGE_SHOWREADME_TEXT "View README"
!insertmacro MUI_PAGE_FINISH

; Uninstaller Pages
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_UNPAGE_FINISH

; Languages
!insertmacro MUI_LANGUAGE "English"

; Installer Sections
Section "Main Application" SecMain
    SectionIn RO ; Read-only, always installed

    SetOutPath "$INSTDIR"

    ; Copy files
    File "dictation.exe"
    File "config.sample.toml"
    File "README.md"
    File "QUICKSTART.md"
    File /nonfatal "LICENSE"

    ; Create application data directory
    SetShellVarContext current
    CreateDirectory "$APPDATA\dictation"

    ; Copy sample config if no config exists
    IfFileExists "$APPDATA\dictation\config.toml" +2 0
    CopyFiles /SILENT "$INSTDIR\config.sample.toml" "$APPDATA\dictation\config.toml"

    ; Create Start Menu shortcuts
    SetShellVarContext all
    CreateDirectory "$SMPROGRAMS\${APP_NAME}"
    CreateShortcut "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk" "$INSTDIR\${APP_EXE}"
    CreateShortcut "$SMPROGRAMS\${APP_NAME}\README.lnk" "$INSTDIR\README.md"
    CreateShortcut "$SMPROGRAMS\${APP_NAME}\Configuration.lnk" "$APPDATA\dictation\config.toml"
    CreateShortcut "$SMPROGRAMS\${APP_NAME}\Uninstall.lnk" "$INSTDIR\Uninstall.exe"

    ; Desktop shortcut (optional, ask user)
    MessageBox MB_YESNO "Create desktop shortcut?" IDNO +2
    CreateShortcut "$DESKTOP\${APP_NAME}.lnk" "$INSTDIR\${APP_EXE}"

    ; Write registry keys
    WriteRegStr HKLM "${APP_REGKEY}" "InstallPath" "$INSTDIR"
    WriteRegStr HKLM "${APP_REGKEY}" "Version" "${APP_VERSION}"

    ; Write uninstaller
    WriteUninstaller "$INSTDIR\Uninstall.exe"

    ; Add to Add/Remove Programs
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "DisplayName" "${APP_NAME}"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "UninstallString" "$INSTDIR\Uninstall.exe"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "InstallLocation" "$INSTDIR"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "DisplayIcon" "$INSTDIR\${APP_EXE}"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "Publisher" "${APP_PUBLISHER}"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "DisplayVersion" "${APP_VERSION}"
    WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "NoModify" 1
    WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "NoRepair" 1

    ; Estimate size
    ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
    IntFmt $0 "0x%08X" $0
    WriteRegDWORD HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}" "EstimatedSize" "$0"

SectionEnd

; Uninstaller Section
Section "Uninstall"

    ; Stop the application if running
    nsExec::Exec 'taskkill /F /IM ${APP_EXE}'

    ; Remove files
    Delete "$INSTDIR\${APP_EXE}"
    Delete "$INSTDIR\config.sample.toml"
    Delete "$INSTDIR\README.md"
    Delete "$INSTDIR\QUICKSTART.md"
    Delete "$INSTDIR\LICENSE"
    Delete "$INSTDIR\Uninstall.exe"

    ; Remove shortcuts
    SetShellVarContext all
    Delete "$SMPROGRAMS\${APP_NAME}\${APP_NAME}.lnk"
    Delete "$SMPROGRAMS\${APP_NAME}\README.lnk"
    Delete "$SMPROGRAMS\${APP_NAME}\Configuration.lnk"
    Delete "$SMPROGRAMS\${APP_NAME}\Uninstall.lnk"
    RMDir "$SMPROGRAMS\${APP_NAME}"
    Delete "$DESKTOP\${APP_NAME}.lnk"

    ; Remove install directory
    RMDir "$INSTDIR"

    ; Ask about config
    MessageBox MB_YESNO "Remove configuration file? (This will delete your API key)" IDNO +3
    SetShellVarContext current
    Delete "$APPDATA\dictation\config.toml"
    RMDir "$APPDATA\dictation"

    ; Remove registry keys
    DeleteRegKey HKLM "${APP_REGKEY}"
    DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APP_NAME}"

    ; Remove autostart entry if exists
    DeleteRegValue HKCU "Software\Microsoft\Windows\CurrentVersion\Run" "DictationApp"

SectionEnd

; Helper function to get directory size
!include "FileFunc.nsh"
!insertmacro GetSize

; NSIS installer for the lanweave Windows client.
;
; Build (on Windows, with the GUI client + wintun.dll placed next to this script):
;   1) build the client:  go build -tags gui -o lanweave-client.exe ./cmd/lanweave-client
;   2) copy wintun.dll (amd64) next to lanweave-client.exe
;   3) makensis lanweave-client.nsi   ->  lanweave-client-setup.exe
;
; The installer requests administrator rights only to install the WinTun driver and write to
; Program Files. The installed app obtains its own administrator rights at runtime: it
; self-elevates (UAC) on launch to create the network adapter (see internal/client/winelevate),
; so no manifest is embedded here. It installs the app + driver to C:\Program Files\lanweave\,
; creates Start-menu and desktop shortcuts, and registers an uninstaller. Uninstall removes
; the program files but LEAVES the user's stored secrets and local state (the device key in
; the Windows Credential Manager and %LOCALAPPDATA%\lanweave\), so the device identity is not
; destroyed by accident -- see docs/GUIDE.en.md for the manual purge.

!define APPNAME "lanweave"
!define EXE "lanweave-client.exe"
!define PUBLISHER "lanweave"

; VERSION is passed by the release pipeline (makensis /DVERSION=X.Y.Z); guard it so a bare
; `makensis lanweave-client.nsi` still compiles for local testing.
!ifndef VERSION
    !define VERSION "0.0.0-dev"
!endif

Name "${APPNAME}"
OutFile "lanweave-client-setup.exe"
InstallDir "$PROGRAMFILES64\lanweave"
RequestExecutionLevel admin          ; elevation required (driver + Program Files)
Unicode true
SetCompressor /SOLID lzma

; Installer and uninstaller executable icons (icon.ico is copied next to this script at build).
Icon "icon.ico"
UninstallIcon "icon.ico"

Page directory
Page instfiles
UninstPage uninstConfirm
UninstPage instfiles

Section "Install"
    ; If elevation was denied, $PROGRAMFILES64 is not writable and the section fails cleanly.
    SetOutPath "$INSTDIR"
    File "lanweave-client.exe"
    File "wintun.dll"

    CreateShortcut "$SMPROGRAMS\${APPNAME}.lnk" "$INSTDIR\${EXE}"
    CreateShortcut "$DESKTOP\${APPNAME}.lnk" "$INSTDIR\${EXE}"

    WriteUninstaller "$INSTDIR\uninstall.exe"

    ; Add/Remove Programs entry. DisplayIcon points at the installed EXE, which carries the
    ; embedded icon resource (built via windres), so the programs list shows the brand mark.
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayName" "${APPNAME}"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "UninstallString" "$INSTDIR\uninstall.exe"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "InstallLocation" "$INSTDIR"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayIcon" "$INSTDIR\${EXE}"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "DisplayVersion" "${VERSION}"
    WriteRegStr HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}" "Publisher" "${PUBLISHER}"
SectionEnd

Section "Uninstall"
    ; Remove the program files and shortcuts. Deliberately keep the user's secrets/state
    ; (Credential Manager entry + %LOCALAPPDATA%\lanweave\) -- see docs/GUIDE.en.md for purge steps.
    Delete "$INSTDIR\${EXE}"
    Delete "$INSTDIR\wintun.dll"
    Delete "$INSTDIR\uninstall.exe"
    RMDir "$INSTDIR"
    Delete "$SMPROGRAMS\${APPNAME}.lnk"
    Delete "$DESKTOP\${APPNAME}.lnk"
    DeleteRegKey HKLM "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPNAME}"
SectionEnd

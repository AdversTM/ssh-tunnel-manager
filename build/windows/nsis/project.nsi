Unicode true

## Per-user installer for SSH Tunnel Manager (no admin rights needed).
## Build: wails3 task windows:package VERSION=1.2.3
## or:    makensis -DARG_WAILS_AMD64_BINARY=..\..\..\bin\ssh-tunnel-manager.exe -DINFO_PRODUCTVERSION=1.2.3 project.nsi

!define INFO_PROJECTNAME "ssh-tunnel-manager"
!define INFO_COMPANYNAME "SSH Tunnel Manager"
!define INFO_PRODUCTNAME "SSH Tunnel Manager"
!define INFO_COPYRIGHT   "(c) 2026 SSH Tunnel Manager contributors"
!define UNINST_KEY_NAME  "SshTunnelManager"
!ifndef WAILS_INSTALL_SCOPE
    !define WAILS_INSTALL_SCOPE "user"
!endif
!ifndef REQUEST_EXECUTION_LEVEL
    !define REQUEST_EXECUTION_LEVEL "user"
!endif
!define RUN_KEY "Software\Microsoft\Windows\CurrentVersion\Run"
!define RUN_VALUE "SshTunnelManager"

!include "wails_tools.nsh"

VIProductVersion "${INFO_PRODUCTVERSION}.0"
VIFileVersion    "${INFO_PRODUCTVERSION}.0"
VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"
!define MUI_FINISHPAGE_RUN_PARAMETERS "-show"
!define MUI_FINISHPAGE_RUN_TEXT "Start ${INFO_PRODUCTNAME}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "..\..\..\LICENSE"
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\..\bin\${INFO_PROJECTNAME}-${INFO_PRODUCTVERSION}-setup.exe"
!if "${WAILS_INSTALL_SCOPE}" == "user"
    InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
!else
    InstallDir "$PROGRAMFILES64\${INFO_PRODUCTNAME}"
!endif
InstallDirRegKey HKCU "Software\${INFO_PRODUCTNAME}" "InstallDir"
ShowInstDetails show

; A running exe cannot be replaced or deleted; ask the user to exit it instead of killing other copies.
!macro ensureNotRunning ID
    retry_${ID}:
    ClearErrors
    ${If} ${FileExists} "$INSTDIR\${PRODUCT_EXECUTABLE}"
        Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
        ${If} ${Errors}
            MessageBox MB_RETRYCANCEL|MB_ICONEXCLAMATION "${INFO_PRODUCTNAME} is running.$\r$\nExit it from the tray icon menu (Exit), then click Retry." /SD IDCANCEL IDRETRY retry_${ID}
            Abort
        ${EndIf}
    ${EndIf}
!macroend

Function .onInit
    !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext
    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR
    !insertmacro ensureNotRunning install
    !insertmacro wails.files

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}" "-show"
    WriteRegStr HKCU "Software\${INFO_PRODUCTNAME}" "InstallDir" "$INSTDIR"

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext
    !insertmacro ensureNotRunning uninstall

    ; Remove autostart only if it points at this installation (a portable copy may own it).
    ReadRegStr $0 HKCU "${RUN_KEY}" "${RUN_VALUE}"
    ${If} $0 == '"$INSTDIR\${PRODUCT_EXECUTABLE}"'
        DeleteRegValue HKCU "${RUN_KEY}" "${RUN_VALUE}"
    ${EndIf}

    RMDir /r $INSTDIR
    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    DeleteRegKey HKCU "Software\${INFO_PRODUCTNAME}"
    DetailPrint "Tunnel configuration and logs are kept in $APPDATA\SshTunnelManager"

    !insertmacro wails.deleteUninstaller
SectionEnd

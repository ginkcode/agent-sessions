Unicode true

####
## NSIS script for the Windows installer, adapted from the Wails v2 template.
## `make package-windows` copies it to build/windows/installer/ and runs
## `wails build -nsis`, which writes wails_tools.nsh and the WebView2
## bootstrapper next to it and calls makensis with the binary path. The
## Makefile also writes version.nsh with VI_VERSION, the numeric part of the
## version, because VIProductVersion rejects a suffix such as -beta.1.
##
## Changes from the template:
## - Per-user install in %LOCALAPPDATA%\Programs: no UAC prompt, and the app
##   only reads the current user's sessions anyway.
## - The gzipped headless servers go to $INSTDIR\remote, where the app looks
##   for them next to its executable.
## - The uninstaller deletes only the files it installed, never the whole
##   install folder.
####

!define WAILS_INSTALL_SCOPE "user"
!define REQUEST_EXECUTION_LEVEL "user"

!include "wails_tools.nsh"
!include "version.nsh"

# The version information for these two must consist of 4 parts
VIProductVersion "${VI_VERSION}"
VIFileVersion    "${VI_VERSION}"

VIAddVersionKey "CompanyName"     "${INFO_COMPANYNAME}"
VIAddVersionKey "FileDescription" "${INFO_PRODUCTNAME} Installer"
VIAddVersionKey "ProductVersion"  "${INFO_PRODUCTVERSION}"
VIAddVersionKey "FileVersion"     "${INFO_PRODUCTVERSION}"
VIAddVersionKey "LegalCopyright"  "${INFO_COPYRIGHT}"
VIAddVersionKey "ProductName"     "${INFO_PRODUCTNAME}"

# Enable HiDPI support. https://nsis.sourceforge.io/Reference/ManifestDPIAware
ManifestDPIAware true

!include "MUI.nsh"

!define MUI_ICON "..\icon.ico"
!define MUI_UNICON "..\icon.ico"
!define MUI_FINISHPAGE_NOAUTOCLOSE
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\${PRODUCT_EXECUTABLE}"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH

!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "English"

Name "${INFO_PRODUCTNAME}"
OutFile "..\..\bin\${INFO_PROJECTNAME}-${ARCH}-installer.exe"
InstallDir "$LOCALAPPDATA\Programs\${INFO_PRODUCTNAME}"
ShowInstDetails show

Function .onInit
    !insertmacro wails.checkArchitecture
FunctionEnd

Section
    !insertmacro wails.setShellContext

    !insertmacro wails.webview2runtime

    SetOutPath $INSTDIR

    !insertmacro wails.files

    # Drop servers left by an older version before adding this version's.
    Delete "$INSTDIR\remote\*.gz"
    SetOutPath "$INSTDIR\remote"
    File "..\..\remote\*.gz"
    # Shortcuts take the current output path as their working directory.
    SetOutPath $INSTDIR

    CreateShortcut "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"
    CreateShortCut "$DESKTOP\${INFO_PRODUCTNAME}.lnk" "$INSTDIR\${PRODUCT_EXECUTABLE}"

    !insertmacro wails.associateFiles
    !insertmacro wails.associateCustomProtocols

    !insertmacro wails.writeUninstaller
SectionEnd

Section "uninstall"
    !insertmacro wails.setShellContext

    RMDir /r "$AppData\${PRODUCT_EXECUTABLE}" # Remove the WebView2 DataPath

    # Remove only what the installer wrote: the user may have picked a folder
    # that holds other files. Settings, cache and handoff files in %APPDATA%
    # and %LOCALAPPDATA%\agent-sessions stay.
    Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"
    RMDir /r "$INSTDIR\remote"

    Delete "$SMPROGRAMS\${INFO_PRODUCTNAME}.lnk"
    Delete "$DESKTOP\${INFO_PRODUCTNAME}.lnk"

    !insertmacro wails.unassociateFiles
    !insertmacro wails.unassociateCustomProtocols

    !insertmacro wails.deleteUninstaller

    RMDir $INSTDIR
SectionEnd

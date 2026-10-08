package desktop

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/template"
)

func writeAtomic(name string, write func(io.Writer) error) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), ".part-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if err := write(tmp); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), name)
}

// buildPortable zips the app with this server's server.json.
func (s *Service) buildPortable(base string, sc ServerConfig) error {
	cfg, _ := json.MarshalIndent(sc, "", "  ")
	return writeAtomic(s.output(base, sc, "zip"), func(out io.Writer) error {
		zw := zip.NewWriter(out)
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, err := filepath.Rel(base, p)
			if err != nil {
				return err
			}
			rel = filepath.ToSlash(rel)
			if rel == "resources/server.json" {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			hdr, err := zip.FileInfoHeader(info)
			if err != nil {
				return err
			}
			hdr.Name, hdr.Method = rel, zip.Deflate
			dst, err := zw.CreateHeader(hdr)
			if err != nil {
				return err
			}
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.Copy(dst, f)
			f.Close()
			return err
		})
		if err != nil {
			return err
		}
		dst, err := zw.Create("resources/server.json")
		if err != nil {
			return err
		}
		if _, err := dst.Write(cfg); err != nil {
			return err
		}
		return zw.Close()
	})
}

// buildInstaller compiles a per-user installer with NSIS: no administrator
// rights, Start menu and desktop shortcuts, an uninstaller, and a silent
// mode the app uses to update itself.
func (s *Service) buildInstaller(base string, sc ServerConfig) error {
	work, err := os.MkdirTemp(s.dir(), ".nsis-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	cfg, _ := json.MarshalIndent(sc, "", "  ")
	if err := os.WriteFile(filepath.Join(work, "server.json"), cfg, 0o644); err != nil {
		return err
	}
	out := s.output(base, sc, "exe")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		return err
	}
	tmpOut := filepath.Join(work, "setup.exe")
	v := Version()
	var script bytes.Buffer
	if err := installerScript.Execute(&script, map[string]string{
		"Base": base, "Config": filepath.Join(work, "server.json"), "Out": tmpOut,
		"Version": v, "Version4": version4(v), "Server": nsisText(sc.ServerURL),
	}); err != nil {
		return err
	}
	nsi := filepath.Join(work, "installer.nsi")
	if err := os.WriteFile(nsi, script.Bytes(), 0o644); err != nil {
		return err
	}
	cmd := exec.Command(s.MakeNSIS, "-V2", "-INPUTCHARSET", "UTF8", nsi)
	var logs bytes.Buffer
	cmd.Stdout, cmd.Stderr = &logs, &logs
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(logs.String())
		if len(msg) > 500 {
			msg = msg[len(msg)-500:]
		}
		return fmt.Errorf("makensis: %v: %s", err, msg)
	}
	return os.Rename(tmpOut, out)
}

// nsisText makes a value safe inside an NSIS string, where $ starts a
// variable and quotes end the string.
func nsisText(v string) string {
	v = strings.ReplaceAll(v, "$", "$$")
	return strings.ReplaceAll(v, `"`, `$\"`)
}

func version4(v string) string {
	parts := strings.Split(v, ".")
	for len(parts) < 4 {
		parts = append(parts, "0")
	}
	return strings.Join(parts[:4], ".")
}

var installerScript = template.Must(template.New("nsi").Parse(`Unicode true
!include "MUI2.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"

Name "ViewDock"
OutFile "{{.Out}}"
InstallDir "$LOCALAPPDATA\Programs\ViewDock"
InstallDirRegKey HKCU "Software\ViewDock" "InstallDir"
RequestExecutionLevel user
SetCompressor /SOLID lzma
SetCompressorDictSize 32
BrandingText "ViewDock {{.Version}}"

VIProductVersion "{{.Version4}}"
VIAddVersionKey "ProductName" "ViewDock"
VIAddVersionKey "FileDescription" "ViewDock setup"
VIAddVersionKey "CompanyName" "ViewDock"
VIAddVersionKey "LegalCopyright" "ViewDock contributors"
VIAddVersionKey "FileVersion" "{{.Version}}"
VIAddVersionKey "ProductVersion" "{{.Version}}"

!define MUI_ICON "{{.Base}}/resources/icon.ico"
!define MUI_UNICON "{{.Base}}/resources/icon.ico"
!define MUI_ABORTWARNING
!define MUI_WELCOMEPAGE_TITLE "ViewDock for Windows"
!define MUI_WELCOMEPAGE_TEXT "This installs ViewDock {{.Version}} for your Windows account, connected to {{.Server}}.$\r$\n$\r$\nViewDock closes if it is running."
!define MUI_FINISHPAGE_RUN "$INSTDIR\ViewDock.exe"
!define MUI_FINISHPAGE_RUN_TEXT "Open ViewDock"

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"

Function CloseViewDock
  nsExec::Exec '"$SYSDIR\taskkill.exe" /F /IM ViewDock.exe'
  Pop $0
  Sleep 800
FunctionEnd

Function un.CloseViewDock
  nsExec::Exec '"$SYSDIR\taskkill.exe" /F /IM ViewDock.exe'
  Pop $0
  Sleep 800
FunctionEnd

Section "ViewDock"
  Call CloseViewDock
  SetOutPath "$INSTDIR"
  RMDir /r "$INSTDIR\resources\app"
  RMDir /r "$INSTDIR\locales"
  File /r "{{.Base}}/*"
  SetOutPath "$INSTDIR\resources"
  File "{{.Config}}"
  SetOutPath "$INSTDIR"
  WriteRegStr HKCU "Software\ViewDock" "InstallDir" "$INSTDIR"
  WriteUninstaller "$INSTDIR\Uninstall ViewDock.exe"
  CreateShortCut "$SMPROGRAMS\ViewDock.lnk" "$INSTDIR\ViewDock.exe" "" "$INSTDIR\ViewDock.exe" 0
  CreateShortCut "$DESKTOP\ViewDock.lnk" "$INSTDIR\ViewDock.exe" "" "$INSTDIR\ViewDock.exe" 0
  WriteRegStr HKCU "Software\Classes\viewdock" "" "URL:ViewDock"
  WriteRegStr HKCU "Software\Classes\viewdock" "URL Protocol" ""
  WriteRegStr HKCU "Software\Classes\viewdock\DefaultIcon" "" "$INSTDIR\ViewDock.exe,0"
  WriteRegStr HKCU "Software\Classes\viewdock\shell\open\command" "" '"$INSTDIR\ViewDock.exe" "%1"'
  !define UNINST "Software\Microsoft\Windows\CurrentVersion\Uninstall\ViewDock"
  WriteRegStr HKCU "${UNINST}" "DisplayName" "ViewDock"
  WriteRegStr HKCU "${UNINST}" "DisplayVersion" "{{.Version}}"
  WriteRegStr HKCU "${UNINST}" "Publisher" "ViewDock"
  WriteRegStr HKCU "${UNINST}" "DisplayIcon" "$INSTDIR\ViewDock.exe,0"
  WriteRegStr HKCU "${UNINST}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINST}" "UninstallString" '"$INSTDIR\Uninstall ViewDock.exe"'
  WriteRegStr HKCU "${UNINST}" "QuietUninstallString" '"$INSTDIR\Uninstall ViewDock.exe" /S'
  WriteRegDWORD HKCU "${UNINST}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINST}" "NoRepair" 1
  ; The app updates itself with /S /RELAUNCH when it starts, and opens again.
  ${GetParameters} $R0
  ClearErrors
  ${GetOptions} $R0 "/RELAUNCH" $R1
  ${IfNot} ${Errors}
    Exec '"$INSTDIR\ViewDock.exe"'
  ${EndIf}
SectionEnd

Section "Uninstall"
  Call un.CloseViewDock
  Delete "$SMPROGRAMS\ViewDock.lnk"
  Delete "$DESKTOP\ViewDock.lnk"
  RMDir /r "$INSTDIR"
  DeleteRegKey HKCU "Software\Classes\viewdock"
  DeleteRegKey HKCU "Software\Microsoft\Windows\CurrentVersion\Uninstall\ViewDock"
  DeleteRegKey HKCU "Software\ViewDock"
SectionEnd
`))

package desktop

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	app "github.com/viewdock/viewdock/desktop"
)

// electronMaxBytes bounds the Electron download.
const electronMaxBytes = 600 << 20

// DefaultElectronMirror is where Electron releases are downloaded from;
// VD_DESKTOP_ELECTRON_MIRROR replaces it, with the same layout.
const DefaultElectronMirror = "https://github.com/electron/electron/releases/download/"

type electronPin struct {
	Version string            `json:"version"`
	SHA256  map[string]string `json:"sha256"`
}

func (s *Service) pinned() (electronPin, error) {
	if s.pin != nil {
		return *s.pin, nil
	}
	var p electronPin
	raw, err := app.Files.ReadFile("electron.json")
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, err
	}
	if p.Version == "" || len(p.SHA256["win32-x64"]) != 64 {
		return p, errors.New("desktop/electron.json does not pin a Windows download")
	}
	return p, nil
}

// baseName names the built app by everything it is made of, so a change
// to the app or to Electron builds it again.
func (s *Service) baseName() (string, error) {
	p, err := s.pinned()
	if err != nil {
		return "", err
	}
	h := sha256.New()
	fmt.Fprintf(h, "%s|%s|", Version(), p.SHA256["win32-x64"])
	err = fs.WalkDir(app.Files, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := app.Files.ReadFile(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s|%d|", name, len(raw))
		h.Write(raw)
		return nil
	})
	if err != nil {
		return "", err
	}
	return "app-" + Version() + "-" + hex.EncodeToString(h.Sum(nil)[:6]), nil
}

// prepared returns the built app's folder once it exists, the error of the
// last attempt to build it, and whether an attempt is running.
func (s *Service) prepared() (string, error, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	busy := s.prep != nil && !closed(s.prep.done)
	if s.base != "" {
		if _, err := os.Stat(filepath.Join(s.base, "ViewDock.exe")); err == nil {
			return s.base, nil, busy
		}
		s.base = ""
	}
	if name, err := s.baseName(); err == nil {
		dir := filepath.Join(s.dir(), name)
		if _, err := os.Stat(filepath.Join(dir, "ViewDock.exe")); err == nil {
			s.base = dir
			return dir, nil, busy
		}
	}
	var err error
	if s.prep != nil && !busy {
		err = s.prep.err
	}
	return "", err, busy
}

// Prepare downloads Electron and builds the app, once at a time.
func (s *Service) Prepare() *job {
	s.mu.Lock()
	if s.prep != nil && !closed(s.prep.done) {
		j := s.prep
		s.mu.Unlock()
		return j
	}
	j := &job{done: make(chan struct{})}
	s.prep = j
	s.mu.Unlock()
	go func() {
		defer close(j.done)
		started := time.Now()
		j.err = s.prepare()
		if s.Log == nil {
			return
		}
		if j.err != nil {
			s.Log.Warn("desktop app build failed", "category", "app", "err", j.err.Error())
		} else {
			s.Log.Info("desktop app ready", "category", "app", "version", Version(), "seconds", int(time.Since(started).Seconds()))
		}
	}()
	return j
}

func (s *Service) prepare() error {
	p, err := s.pinned()
	if err != nil {
		return err
	}
	name, err := s.baseName()
	if err != nil {
		return err
	}
	dir := filepath.Join(s.dir(), name)
	if _, err := os.Stat(filepath.Join(dir, "ViewDock.exe")); err == nil {
		return nil
	}
	if err := os.MkdirAll(s.dir(), 0o755); err != nil {
		return err
	}
	electron := filepath.Join(s.dir(), "electron-v"+p.Version+"-win32-x64.zip")
	if err := s.fetchElectron(p, electron); err != nil {
		return err
	}
	tmp, err := os.MkdirTemp(s.dir(), ".app-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	if err := extractElectron(electron, tmp); err != nil {
		return fmt.Errorf("unpacking Electron: %w", err)
	}
	icon, err := app.Files.ReadFile("icon.ico")
	if err != nil {
		return err
	}
	if err := brand(filepath.Join(tmp, "ViewDock.exe"), icon, Version()); err != nil {
		return fmt.Errorf("branding the program: %w", err)
	}
	if err := writeApp(filepath.Join(tmp, "resources", "app")); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "resources", "icon.ico"), icon, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(tmp, "version"), []byte(Version()+"\n"), 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dir); err != nil {
		return err
	}
	s.mu.Lock()
	s.base = dir
	s.mu.Unlock()
	s.prune()
	return nil
}

// fetchElectron downloads the pinned Electron for Windows unless a copy
// that matches the pinned SHA-256 is already there.
func (s *Service) fetchElectron(p electronPin, dest string) error {
	want := p.SHA256["win32-x64"]
	if sum, err := fileSHA256(dest); err == nil && sum == want {
		return nil
	}
	mirror := os.Getenv("VD_DESKTOP_ELECTRON_MIRROR")
	if mirror == "" {
		mirror = DefaultElectronMirror
	}
	u := strings.TrimRight(mirror, "/") + "/v" + p.Version + "/electron-v" + p.Version + "-win32-x64.zip"
	resp, err := s.HTTP.Get(u)
	if err != nil {
		return fmt.Errorf("downloading Electron: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading Electron: %s answered %d", u, resp.StatusCode)
	}
	part := dest + ".part"
	out, err := os.Create(part)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, electronMaxBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > electronMaxBytes {
		err = errors.New("the Electron download is larger than expected")
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = errors.New("the Electron download does not match its pinned SHA-256")
	}
	if err != nil {
		os.Remove(part)
		return fmt.Errorf("downloading Electron: %w", err)
	}
	return os.Rename(part, dest)
}

func fileSHA256(name string) (string, error) {
	f, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractElectron unpacks Electron for Windows as ViewDock.exe, without
// Electron's sample app and with English only: the other locales are
// Chromium's own dialogs, about 40 MB.
func extractElectron(src, dest string) error {
	zr, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer zr.Close()
	found := false
	for _, f := range zr.File {
		raw := strings.ReplaceAll(f.Name, "\\", "/")
		if strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "../") || strings.Contains(raw, "/../") || f.FileInfo().IsDir() {
			continue
		}
		name := path.Clean("/" + raw)[1:]
		if name == "" {
			continue
		}
		if name == "resources/default_app.asar" || (strings.HasPrefix(name, "locales/") && name != "locales/en-US.pak") {
			continue
		}
		if name == "electron.exe" {
			name, found = "ViewDock.exe", true
		}
		target := filepath.Join(dest, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := copyZipFile(f, target); err != nil {
			return err
		}
	}
	if !found {
		return errors.New("the download has no electron.exe")
	}
	return nil
}

func copyZipFile(f *zip.File, target string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, rc); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// brand gives the program ViewDock's icon and name, which Windows shows in
// Explorer, the taskbar, Task Manager and the file's properties.
func brand(exe string, icon []byte, v string) error {
	in, err := os.Open(exe)
	if err != nil {
		return err
	}
	rs, err := winres.LoadFromEXE(in)
	if err != nil {
		in.Close()
		return err
	}
	ico, err := winres.LoadICO(bytes.NewReader(icon))
	if err != nil {
		in.Close()
		return err
	}
	var group winres.Identifier = winres.ID(1)
	lang := uint16(0x0409)
	rs.WalkType(winres.RT_GROUP_ICON, func(id winres.Identifier, l uint16, _ []byte) bool {
		group, lang = id, l
		return false
	})
	if err := rs.SetIconTranslation(group, lang, ico); err != nil {
		in.Close()
		return err
	}
	var vi version.Info
	vi.SetFileVersion(v)
	vi.SetProductVersion(v)
	for key, value := range map[string]string{
		version.ProductName:      "ViewDock",
		version.FileDescription:  "ViewDock",
		version.CompanyName:      "ViewDock",
		version.InternalName:     "ViewDock",
		version.OriginalFilename: "ViewDock.exe",
		version.LegalCopyright:   "ViewDock contributors",
		version.FileVersion:      v,
		version.ProductVersion:   v,
	} {
		if err := vi.Set(0x0409, key, value); err != nil {
			in.Close()
			return err
		}
	}
	rs.SetVersionInfo(vi)
	tmp := exe + ".branded"
	out, err := os.Create(tmp)
	if err != nil {
		in.Close()
		return err
	}
	err = rs.WriteToEXE(out, in, winres.WithAuthenticode(winres.RemoveSignature))
	in.Close()
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, exe)
}

// writeApp copies the embedded app, with this ViewDock's app version.
func writeApp(dest string) error {
	return fs.WalkDir(app.Files, "app", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(strings.TrimPrefix(name, "app"), "/")
		target := filepath.Join(dest, filepath.FromSlash(rel))
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := app.Files.ReadFile(name)
		if err != nil {
			return err
		}
		if rel == "package.json" {
			var pkg map[string]any
			if err := json.Unmarshal(raw, &pkg); err != nil {
				return err
			}
			pkg["version"] = Version()
			if raw, err = json.MarshalIndent(pkg, "", "  "); err != nil {
				return err
			}
		}
		return os.WriteFile(target, raw, 0o644)
	})
}

// prune keeps the newest packages, the current app and its Electron.
func (s *Service) prune() {
	entries, err := os.ReadDir(s.dir())
	if err != nil {
		return
	}
	current, _ := s.baseName()
	p, _ := s.pinned()
	type item struct {
		name string
		mod  time.Time
	}
	var builds []item
	for _, e := range entries {
		name := e.Name()
		switch {
		case e.IsDir() && strings.HasPrefix(name, "build-"):
			if fi, err := e.Info(); err == nil {
				builds = append(builds, item{name, fi.ModTime()})
			}
		case e.IsDir() && strings.HasPrefix(name, "app-") && current != "" && name != current:
			os.RemoveAll(filepath.Join(s.dir(), name))
		case !e.IsDir() && strings.HasPrefix(name, "electron-v") && p.Version != "" && name != "electron-v"+p.Version+"-win32-x64.zip":
			os.Remove(filepath.Join(s.dir(), name))
		}
	}
	sort.Slice(builds, func(i, j int) bool { return builds[i].mod.After(builds[j].mod) })
	for i, b := range builds {
		if i >= keepBuilds {
			os.RemoveAll(filepath.Join(s.dir(), b.name))
		}
	}
}

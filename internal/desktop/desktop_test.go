package desktop

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
)

// windowsExe builds a small Windows program to stand in for electron.exe.
func windowsExe(t *testing.T) []byte {
	t.Helper()
	dir := t.TempDir()
	src := filepath.Join(dir, "main.go")
	if err := os.WriteFile(src, []byte("package main\n\nfunc main() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Like electron.exe, it carries an icon and version details.
	var rs winres.ResourceSet
	ico, err := winres.LoadICO(bytes.NewReader(mustRead(t, filepath.Join("..", "..", "desktop", "icon.ico"))))
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.SetIcon(winres.ID(1), ico); err != nil {
		t.Fatal(err)
	}
	var vi version.Info
	vi.SetFileVersion("9.9.9")
	_ = vi.Set(0x0409, version.ProductName, "Electron")
	rs.SetVersionInfo(vi)
	syso, err := os.Create(filepath.Join(dir, "rsrc_windows_amd64.syso"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rs.WriteObject(syso, winres.ArchAMD64); err != nil {
		t.Fatal(err)
	}
	syso.Close()
	out := filepath.Join(dir, "electron.exe")
	cmd := exec.Command("go", "build", "-o", out, ".")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOOS=windows", "GOARCH=amd64", "CGO_ENABLED=0", "GO111MODULE=off")
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building a Windows program: %v %s", err, msg)
	}
	raw, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// fakeElectron serves an Electron-like zip and returns the pin for it.
func fakeElectron(t *testing.T) (*electronPin, *int32) {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, body := range map[string][]byte{
		"electron.exe":                windowsExe(t),
		"resources/default_app.asar":  []byte("sample app"),
		"locales/en-US.pak":           []byte("en"),
		"locales/fr.pak":              []byte("fr"),
		"LICENSES.chromium.html":      []byte("licenses"),
		"ffmpeg.dll":                  []byte("dll"),
		"../escape.txt":               []byte("outside"),
		"resources/./keep.txt":        []byte("kept"),
		"resources/app-placeholder.x": []byte("x"),
	} {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write(body)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	data := buf.Bytes()
	sum := sha256.Sum256(data)
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v9.9.9/electron-v9.9.9-win32-x64.zip" {
			http.NotFound(w, r)
			return
		}
		atomic.AddInt32(&hits, 1)
		_, _ = w.Write(data)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("VD_DESKTOP_ELECTRON_MIRROR", srv.URL)
	return &electronPin{Version: "9.9.9", SHA256: map[string]string{"win32-x64": hex.EncodeToString(sum[:])}}, &hits
}

func mustRead(t *testing.T, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newService(t *testing.T, on *atomic.Bool) *Service {
	t.Helper()
	s := New(config.Config{}, nil, t.TempDir(), nil)
	s.MakeNSIS = ""
	s.Enabled = on.Load
	return s
}

func router(s *Service) http.Handler {
	r := chi.NewRouter()
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Test-User") != "" {
				r = r.WithContext(auth.WithPrincipal(r.Context(), &auth.Principal{Kind: auth.KindUser, UserID: "u1"}))
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Route("/api/v1", s.Routes)
	return r
}

func get(h http.Handler, path string, user bool) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://viewdock.example.com"+path, nil)
	if user {
		req.Header.Set("X-Test-User", "1")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func readInfo(t *testing.T, h http.Handler, user bool) info {
	t.Helper()
	var in info
	rec := get(h, "/api/v1/desktop", user)
	if err := json.Unmarshal(rec.Body.Bytes(), &in); err != nil {
		t.Fatalf("info: %s", rec.Body.String())
	}
	return in
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestOffUntilAnAdministratorTurnsItOn(t *testing.T) {
	var on atomic.Bool
	s := newService(t, &on)
	h := router(s)
	if rec := get(h, "/api/v1/desktop", true); strings.TrimSpace(rec.Body.String()) != `{"available":false}` {
		t.Fatalf("info while off: %s", rec.Body.String())
	}
	if rec := get(h, "/api/v1/desktop/windows/ViewDock-"+Version()+"-portable.zip", true); rec.Code != http.StatusNotFound {
		t.Fatalf("download while off: %d", rec.Code)
	}
	if _, err := os.Stat(s.dir()); !os.IsNotExist(err) {
		t.Fatal("files were downloaded while the app is off")
	}
}

func TestServerBuildsTheAppFromElectronForItself(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	pin, hits := fakeElectron(t)
	s := newService(t, &on)
	s.pin = pin
	h := router(s)

	// An anonymous visitor learns about the app but starts nothing.
	if in := readInfo(t, h, false); !in.Available || in.Version != Version() || in.Preparing {
		t.Fatalf("anonymous info: %+v", in)
	}
	if atomic.LoadInt32(hits) != 0 {
		t.Fatal("an anonymous visitor started the Electron download")
	}
	if rec := get(h, "/api/v1/desktop/windows/ViewDock-"+Version()+"-portable.zip", false); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous download: %d", rec.Code)
	}

	readInfo(t, h, true)
	waitFor(t, "the portable app", func() bool { return readInfo(t, h, true).Windows.PortableReady })
	in := readInfo(t, h, true)
	if in.ServerURL != "http://viewdock.example.com" || in.Windows.URL != in.Windows.Portable {
		t.Fatalf("info: %+v %+v", in, in.Windows)
	}
	if n := atomic.LoadInt32(hits); n != 1 {
		t.Fatalf("Electron was downloaded %d times", n)
	}

	rec := get(h, in.Windows.Portable, true)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Header().Get("Content-Disposition"), "ViewDock-"+Version()+"-portable.zip") {
		t.Fatalf("download: %d %q", rec.Code, rec.Header().Get("Content-Disposition"))
	}
	body := rec.Body.Bytes()
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		files[f.Name] = b
	}
	for _, gone := range []string{"electron.exe", "resources/default_app.asar", "locales/fr.pak", "escape.txt", "../escape.txt"} {
		if _, ok := files[gone]; ok {
			t.Errorf("%s should not be in the app", gone)
		}
	}
	for _, kept := range []string{"locales/en-US.pak", "LICENSES.chromium.html", "resources/icon.ico", "resources/app/main.js", "resources/app/preload.js", "resources/keep.txt"} {
		if _, ok := files[kept]; !ok {
			t.Errorf("%s is missing from the app", kept)
		}
	}
	var pkg struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(files["resources/app/package.json"], &pkg); err != nil || pkg.Version != Version() {
		t.Fatalf("app package.json: %s", files["resources/app/package.json"])
	}
	var sc ServerConfig
	if err := json.Unmarshal(files["resources/server.json"], &sc); err != nil || sc.ServerURL != "http://viewdock.example.com" {
		t.Fatalf("server.json: %s", files["resources/server.json"])
	}

	// The program carries ViewDock's name, version and icon.
	rs, err := winres.LoadFromEXE(bytes.NewReader(files["ViewDock.exe"]))
	if err != nil {
		t.Fatalf("branded program: %v", err)
	}
	vi, err := version.FromBytes(rs.Get(winres.RT_VERSION, winres.ID(1), 0x0409))
	if err != nil {
		t.Fatalf("version info: %v", err)
	}
	st := vi.Table().GetMainTranslation()
	if st[version.ProductName] != "ViewDock" || st[version.ProductVersion] != Version() {
		t.Fatalf("version strings: %v", st)
	}
	icons := 0
	rs.WalkType(winres.RT_GROUP_ICON, func(winres.Identifier, uint16, []byte) bool {
		icons++
		return true
	})
	if icons == 0 {
		t.Fatal("the program has no icon")
	}

	// Turning the app off deletes what was downloaded and built.
	on.Store(false)
	s.Apply(t.Context())
	if _, err := os.Stat(s.dir()); !os.IsNotExist(err) {
		t.Fatal("the app files were kept after turning it off")
	}
}

func TestElectronMustMatchItsPinnedChecksum(t *testing.T) {
	var on atomic.Bool
	on.Store(true)
	pin, _ := fakeElectron(t)
	pin.SHA256["win32-x64"] = strings.Repeat("0", 64)
	s := newService(t, &on)
	s.pin = pin
	h := router(s)
	readInfo(t, h, true)
	waitFor(t, "the failed build", func() bool {
		_, err, busy := s.prepared()
		return err != nil && !busy
	})
	in := readInfo(t, h, false)
	if !strings.Contains(in.Error, "SHA-256") {
		t.Fatalf("error: %q", in.Error)
	}
	entries, _ := os.ReadDir(s.dir())
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "electron-") || strings.HasPrefix(e.Name(), "app-") {
			t.Fatalf("an unverified download was kept: %s", e.Name())
		}
	}
	if rec := get(h, "/api/v1/desktop/windows/ViewDock-"+Version()+"-portable.zip", true); rec.Code != http.StatusInternalServerError {
		t.Fatalf("download after a failed build: %d %s", rec.Code, rec.Body.String())
	}
}

func TestInstallerBuildsWithNSIS(t *testing.T) {
	nsis, err := exec.LookPath("makensis")
	if err != nil {
		t.Skip("makensis is not installed")
	}
	var on atomic.Bool
	on.Store(true)
	pin, _ := fakeElectron(t)
	s := newService(t, &on)
	s.pin, s.MakeNSIS = pin, nsis
	if err := s.prepare(); err != nil {
		t.Fatal(err)
	}
	base, _, _ := s.prepared()
	sc := ServerConfig{ServerURL: "https://viewdock.example.com", ServerName: "ViewDock"}
	if err := s.buildInstaller(base, sc); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(s.output(base, sc, "exe"))
	if err != nil || len(raw) < 2 || string(raw[:2]) != "MZ" {
		t.Fatalf("installer is not a Windows program (%d bytes, %v)", len(raw), err)
	}
}

func TestNSISText(t *testing.T) {
	if got := nsisText(`https://a.example/$x"y`); got != `https://a.example/$$x$\"y` {
		t.Fatalf("got %q", got)
	}
}

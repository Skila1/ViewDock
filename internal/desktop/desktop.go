// Package desktop builds ViewDock for Windows on this server. The app's own
// files ship inside ViewDock (the desktop package at the repository root);
// Electron for Windows does not. When an administrator turns the app on,
// the server downloads the pinned Electron release into its cache folder,
// checks it against the pinned SHA-256, builds the branded app from it, and
// packages it with its own server.json, so the installer its viewers
// download opens this server and nobody else's. The file holds the
// server's public address and name only, never a secret.
package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	app "github.com/viewdock/viewdock/desktop"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// keepBuilds is how many packaged builds stay in the cache.
const keepBuilds = 4

type Service struct {
	Cfg config.Config
	KV  httpapi.SettingsLookup
	// CacheDir holds Electron, the app built from it and the packages.
	CacheDir string
	Log      *slog.Logger
	// Enabled is the administrator's switch (Admin, Settings, Desktop app).
	Enabled func() bool
	// MakeNSIS is the NSIS compiler; the installer needs it, the portable
	// zip does not.
	MakeNSIS string
	// HTTP downloads Electron.
	HTTP *http.Client

	mu     sync.Mutex
	builds map[string]*job
	prep   *job
	base   string
	// pin replaces desktop/electron.json in tests.
	pin *electronPin
}

type job struct {
	done chan struct{}
	err  error
}

// ServerConfig is the server.json each package carries.
type ServerConfig struct {
	ServerURL  string `json:"server_url"`
	ServerName string `json:"server_name"`
}

func New(cfg config.Config, kv httpapi.SettingsLookup, cacheDir string, log *slog.Logger) *Service {
	nsis, _ := exec.LookPath("makensis")
	return &Service{
		Cfg: cfg, KV: kv, CacheDir: cacheDir, Log: log, MakeNSIS: nsis,
		HTTP:   &http.Client{Timeout: 30 * time.Minute},
		builds: map[string]*job{},
	}
}

func (s *Service) Routes(r chi.Router) {
	r.Get("/desktop", s.handleInfo)
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireUser)
		r.Get("/desktop/windows/{file}", s.handleDownload)
	})
}

func (s *Service) enabled() bool {
	return s.Enabled != nil && s.Enabled() && s.CacheDir != ""
}

func (s *Service) dir() string {
	return filepath.Join(s.CacheDir, "desktop")
}

// Version is the app version this ViewDock builds.
func Version() string {
	return app.Version()
}

// serverConfig is what this request's server writes into the package.
func (s *Service) serverConfig(r *http.Request) ServerConfig {
	return ServerConfig{ServerURL: strings.TrimRight(httpapi.PublicBase(r, s.Cfg, s.KV), "/"), ServerName: "ViewDock"}
}

type info struct {
	Available bool   `json:"available"`
	Version   string `json:"version,omitempty"`
	ServerURL string `json:"server_url,omitempty"`
	// Preparing is true while the server downloads Electron and builds the
	// app; Error is why the last attempt failed.
	Preparing bool         `json:"preparing,omitempty"`
	Error     string       `json:"error,omitempty"`
	Windows   *windowsInfo `json:"windows,omitempty"`
}

type windowsInfo struct {
	URL      string `json:"url"`
	Portable string `json:"portable"`
	// Ready and PortableReady are true when the package for this server
	// is built and downloads at once.
	Ready         bool `json:"ready"`
	PortableReady bool `json:"portable_ready"`
}

// handleInfo tells the web app and the desktop app what is on offer. A
// signed-in caller also starts building this server's packages, so the
// download is ready by the time it is clicked.
func (s *Service) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if !s.enabled() {
		httpapi.WriteJSON(w, http.StatusOK, info{})
		return
	}
	v := Version()
	sc := s.serverConfig(r)
	out := info{Available: true, Version: v, ServerURL: sc.ServerURL, Windows: &windowsInfo{
		Portable: "/api/v1/desktop/windows/ViewDock-" + v + "-portable.zip",
		URL:      "/api/v1/desktop/windows/ViewDock-Setup-" + v + ".exe",
	}}
	if s.MakeNSIS == "" {
		out.Windows.URL = out.Windows.Portable
	}
	signedIn := auth.FromRequest(r) != nil
	base, perr, busy := s.prepared()
	switch {
	case base == "":
		out.Preparing = busy
		if perr != nil {
			out.Error = perr.Error()
		}
		if signedIn && !busy {
			s.Prepare()
			out.Preparing = true
		}
	default:
		out.Windows.PortableReady = s.ready(base, sc, "zip")
		out.Windows.Ready = out.Windows.PortableReady
		if s.MakeNSIS != "" {
			out.Windows.Ready = s.ready(base, sc, "exe")
		}
		if signedIn {
			if !out.Windows.PortableReady {
				s.start(base, sc, "zip")
			}
			if s.MakeNSIS != "" && !out.Windows.Ready {
				s.start(base, sc, "exe")
			}
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

// handleDownload sends this server's installer or portable zip. A package
// that is still being built answers 202, and the caller asks again.
func (s *Service) handleDownload(w http.ResponseWriter, r *http.Request) {
	if !s.enabled() {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "The Windows app is turned off on this server.")
		return
	}
	v := Version()
	file := chi.URLParam(r, "file")
	kind := ""
	switch file {
	case "ViewDock-Setup-" + v + ".exe":
		kind = "exe"
	case "ViewDock-" + v + "-portable.zip":
		kind = "zip"
	default:
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "There is no such download. Reload the page for the current version.")
		return
	}
	if kind == "exe" && s.MakeNSIS == "" {
		httpapi.WriteErr(w, http.StatusNotFound, "no_installer", "This server cannot build installers; download the portable app instead.")
		return
	}
	building := func() {
		w.Header().Set("Retry-After", "10")
		httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"building": true})
	}
	base, perr, busy := s.prepared()
	if base == "" {
		if !busy {
			if perr != nil {
				httpapi.WriteErr(w, http.StatusInternalServerError, "build_failed", "The Windows app could not be built: "+perr.Error())
				return
			}
			s.Prepare()
		}
		building()
		return
	}
	sc := s.serverConfig(r)
	if !s.ready(base, sc, kind) {
		bd := s.start(base, sc, kind)
		select {
		case <-bd.done:
		case <-time.After(20 * time.Second):
		}
		if !s.ready(base, sc, kind) {
			if bd.err != nil {
				s.forget(base, sc, kind)
				httpapi.WriteErr(w, http.StatusInternalServerError, "build_failed", "The Windows app could not be built: "+bd.err.Error())
				return
			}
			building()
			return
		}
	}
	f, err := os.Open(s.output(base, sc, kind))
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "build_missing", "The Windows app could not be read.")
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		httpapi.WriteErr(w, http.StatusInternalServerError, "build_missing", "The Windows app could not be read.")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`"`)
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, "", st.ModTime(), f)
}

// Apply follows the administrator's switch: turning the app on prepares it
// and the package for the configured public address; turning it off
// deletes everything it downloaded and built.
func (s *Service) Apply(ctx context.Context) {
	if s.CacheDir == "" {
		return
	}
	if !s.enabled() {
		s.mu.Lock()
		busy := s.prep != nil && !closed(s.prep.done)
		s.base = ""
		s.mu.Unlock()
		if !busy {
			if err := os.RemoveAll(s.dir()); err == nil && s.Log != nil {
				s.Log.Info("desktop app files removed", "category", "app")
			}
		}
		return
	}
	pj := s.Prepare()
	go func() {
		<-pj.done
		if pj.err != nil {
			return
		}
		u := httpapi.ResolvePublicURL(ctx, s.Cfg, s.KV)
		if u == "" || s.MakeNSIS == "" {
			return
		}
		base, _, _ := s.prepared()
		if base == "" {
			return
		}
		sc := ServerConfig{ServerURL: strings.TrimRight(u, "/"), ServerName: "ViewDock"}
		if !s.ready(base, sc, "exe") {
			s.start(base, sc, "exe")
		}
	}()
}

func closed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

func buildKey(base string, sc ServerConfig) string {
	raw, _ := json.Marshal(sc)
	sum := sha256.Sum256(append([]byte(filepath.Base(base)+"|"), raw...))
	return hex.EncodeToString(sum[:10])
}

func (s *Service) buildDir(base string, sc ServerConfig) string {
	return filepath.Join(s.dir(), "build-"+buildKey(base, sc))
}

func (s *Service) output(base string, sc ServerConfig, kind string) string {
	v := Version()
	if kind == "exe" {
		return filepath.Join(s.buildDir(base, sc), "ViewDock-Setup-"+v+".exe")
	}
	return filepath.Join(s.buildDir(base, sc), "ViewDock-"+v+"-portable.zip")
}

func (s *Service) ready(base string, sc ServerConfig, kind string) bool {
	fi, err := os.Stat(s.output(base, sc, kind))
	return err == nil && fi.Size() > 0
}

func (s *Service) forget(base string, sc ServerConfig, kind string) {
	s.mu.Lock()
	delete(s.builds, buildKey(base, sc)+kind)
	s.mu.Unlock()
}

// start builds a package once however many ask for it.
func (s *Service) start(base string, sc ServerConfig, kind string) *job {
	key := buildKey(base, sc) + kind
	s.mu.Lock()
	defer s.mu.Unlock()
	if bd := s.builds[key]; bd != nil {
		return bd
	}
	bd := &job{done: make(chan struct{})}
	s.builds[key] = bd
	go func() {
		defer close(bd.done)
		started := time.Now()
		if kind == "exe" {
			bd.err = s.buildInstaller(base, sc)
		} else {
			bd.err = s.buildPortable(base, sc)
		}
		if s.Log != nil {
			if bd.err != nil {
				s.Log.Warn("desktop app package failed", "category", "app", "kind", kind, "err", bd.err.Error())
			} else {
				s.Log.Info("desktop app packaged", "category", "app", "kind", kind, "version", Version(), "server", sc.ServerURL, "seconds", int(time.Since(started).Seconds()))
			}
		}
		s.prune()
	}()
	return bd
}

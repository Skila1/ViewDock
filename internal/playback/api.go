package playback

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/bandwidth"
	"github.com/viewdock/viewdock/internal/cache"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/hwaccel"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/progress"
	"github.com/viewdock/viewdock/internal/share"
	"github.com/viewdock/viewdock/internal/subtitle"
	"github.com/viewdock/viewdock/internal/watchtogether"
)

const (
	defaultLease        = 45 * time.Second
	defaultPlaylistWait = 45 * time.Second
	stokenTTL           = 20 * time.Minute
	hlsCap              = cache.DefaultHLSCap
)

// Deps is how cmd wires the playback engine. Locator and Gate are the
// producer-owned types — do not invent a second MediaLocator or Gate.
type Deps struct {
	Cfg      config.Config
	DB       *sql.DB
	Log      *slog.Logger
	Locator  library.MediaLocator
	Grants   library.LibraryGrants
	Gate     share.Gate
	Prober   ffmpeg.Prober
	FF       *ffmpeg.Tool
	Progress progress.Store
	Catalog  library.MediaCatalog
	CacheDir string
	Slots    int
}

// API serves /playback, /admin/streams, /admin/stats, /watch-together.
//
//	api := playback.New(playback.Deps{Cfg, DB, Locator, Grants, Gate, Prober, FF, Progress, CacheDir})
//	srv.APIMounts = append(srv.APIMounts, api.Routes) // /api/v1/playback, /admin, /watch-together
//	root := chi.NewRouter()
//	root.Mount("/hls", api.HLSHandler()) // HLSRoutes: /{sessionId}/index.m3u8 + segments
//	root.Mount("/", srv.Handler())
//
// WT is registered inside Routes. HLS is not under /api/v1 (SPA NotFound).
type API struct {
	Cfg      config.Config
	DB       *sql.DB
	Log      *slog.Logger
	Locator  library.MediaLocator
	Grants   library.LibraryGrants
	Gate     share.Gate
	Prober   ffmpeg.Prober
	FF       *ffmpeg.Tool
	Progress progress.Store
	Catalog  library.MediaCatalog
	Lim      *bandwidth.Limiter
	HW       hwaccel.Info
	HLS      *cache.HLS
	Art      *cache.Artwork
	Subs     *subtitle.Extractor
	WT       *watchtogether.Hub
	Reg          *Registry
	Lease        time.Duration
	PlaylistWait time.Duration

	stop   chan struct{}
	stopOnce sync.Once
}

func New(d Deps) *API {
	if d.Gate == nil {
		d.Gate = share.NoopGate()
	}
	if d.FF == nil {
		d.FF = ffmpeg.New()
	}
	if d.Prober == nil {
		d.Prober = d.FF
	}
	cacheDir := d.CacheDir
	if cacheDir == "" {
		cacheDir = d.Cfg.CacheDir
	}
	if cacheDir == "" {
		cacheDir = "./cache"
	}
	a := &API{
		Cfg: d.Cfg, DB: d.DB, Log: d.Log,
		Locator: d.Locator, Grants: d.Grants, Gate: d.Gate,
		Prober: d.Prober, FF: d.FF, Progress: d.Progress, Catalog: d.Catalog,
		Lim:   bandwidth.New(d.Slots),
		HW:    hwaccel.Apply(hwaccel.FromDetect(d.FF.Detect()), d.FF.FFmpeg),
		HLS:   cache.NewHLS(filepath.Join(cacheDir, "hls"), hlsCap),
		Art:   cache.NewArtwork(filepath.Join(cacheDir, "artwork"), 0),
		Subs:  subtitle.New(d.FF),
		WT:    watchtogether.New(d.Locator, d.Grants, d.Gate),
		Reg:          NewRegistry(),
		Lease:        defaultLease,
		PlaylistWait: defaultPlaylistWait,
		stop:         make(chan struct{}),
	}
	if d.Log != nil {
		d.Log.Info("hwaccel", "category", "playback", "vaapi", a.HW.VAAPI, "nvenc", a.HW.NVENC, "available", a.HW.Available)
	}
	go a.sweep()
	return a
}

func (a *API) Routes(r chi.Router) {
	r.Route("/playback", func(r chi.Router) {
		r.With(auth.RequireUser).Get("/continue", a.handleContinue)
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireUserOrGuest)
			r.Post("/sessions", a.handleCreate)
			r.Get("/sessions/{id}/file", a.handleFile)
			r.Put("/sessions/{id}/progress", a.handleProgress)
			r.Delete("/sessions/{id}", a.handleDelete)
			r.Get("/sessions/{id}/subtitles", a.handleSubtitles)
			r.Get("/sessions/{id}/download", a.handleDownload)
		})
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePerm(auth.PermStreamsInspect))
		r.Get("/admin/streams", a.handleAdminList)
		r.Get("/admin/streams/{id}", a.handleAdminOne)
		r.Get("/admin/stats", a.handleAdminStats)
	})
	if a.WT != nil {
		a.WT.Routes(r)
	}
}

// HLSRoutes registers playlist + segment GETs on a router that cmd mounts at /hls.
func (s *API) HLSRoutes(r chi.Router) {
	r.Get("/{sessionId}/index.m3u8", s.handlePlaylist)
	r.Get("/{sessionId}/{file}", s.handleSegment)
}

func (s *API) HLSHandler() http.Handler {
	r := chi.NewRouter()
	s.HLSRoutes(r)
	return r
}

func (a *API) sweep() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.Reg.Expire(a.Lease, a.kill)
			a.HLS.Evict(a.Reg.IDs())
		}
	}
}

func (a *API) Close() {
	a.stopOnce.Do(func() { close(a.stop) })
	for _, s := range a.Reg.List() {
		a.Reg.Delete(s.ID)
		a.kill(s)
	}
}

func (a *API) kill(s *Session) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.killed = true
	if s.cancel != nil {
		s.cancel()
	}
	if s.cmd != nil {
		ffmpeg.KillGroup(s.cmd)
	}
	s.mu.Unlock()
	if s.SlotHeld {
		a.Lim.Release()
		s.SlotHeld = false
	}
	if s.GuestSessionID != "" {
		a.Gate.Release(context.Background(), s.GuestSessionID)
	}
	if s.Dir != "" {
		a.HLS.Remove(s.ID)
	}
}

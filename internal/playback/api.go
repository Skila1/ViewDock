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
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/httpapi"
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
// producer-owned types: do not invent a second MediaLocator or Gate.
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
	Settings httpapi.SettingsLookup
	Flight   *diagnostics.Recorder
	CacheDir string
	Slots    int
	// NoParties skips the in-process watch party hub: on media workers,
	// and on control planes that use a separate coordinator.
	NoParties bool
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
	// Parties and PartyRelay replace WT when watch parties run on a
	// separate coordinator: Parties answers party access checks and
	// PartyRelay forwards the public watch party routes.
	Parties      watchtogether.Coordinator
	PartyRelay   http.Handler
	Flight       *diagnostics.Recorder
	Reg          *Registry
	Lease        time.Duration
	PlaylistWait time.Duration
	KF           Keyframer
	// DownloadsEnabled gates file downloads and Offline Vault saves; nil means enabled.
	DownloadsEnabled func() bool
	// Remote, when active, places new sessions on media workers.
	Remote Remote
	// Sources, when set, streams items from external media sources.
	Sources Sources
	// RemoteDiag, when set, adds worker-recorded events to session timelines.
	RemoteDiag  RemoteTimelines
	testInstall func(*Session) error

	progressStats progressCounters
	previews      *previews

	slotMu   sync.Mutex
	stop     chan struct{}
	stopOnce sync.Once
}

// Keyframer is ffprobe keyframe scan. Tests inject a stub.
type Keyframer interface {
	Keyframes(ctx context.Context, path string) ([]int64, error)
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
		Lim:          bandwidth.New(d.Slots),
		HW:           hwaccel.Apply(hwaccel.FromDetect(d.FF.Detect()), d.FF.FFmpeg),
		HLS:          cache.NewHLS(filepath.Join(cacheDir, "hls"), hlsCap),
		Art:          cache.NewArtwork(filepath.Join(cacheDir, "artwork"), 0),
		previews:     newPreviews(filepath.Join(cacheDir, "previews")),
		Subs:         subtitle.New(d.FF),
		Flight:       d.Flight,
		Reg:          NewRegistry(),
		Lease:        defaultLease,
		PlaylistWait: defaultPlaylistWait,
		stop:         make(chan struct{}),
	}
	if !d.NoParties {
		a.WT = watchtogether.NewWithOriginCheckerAndDB(d.Locator, d.Grants, d.Gate, httpapi.PublicOriginChecker(d.Cfg, d.Settings), d.DB, d.Cfg.DatabaseDriver == string(db.DialectPostgres))
	}
	if a.Flight == nil {
		a.Flight = diagnostics.New(1000, 256)
	}
	if d.Log != nil {
		d.Log.Info(hwaccel.StartupMessage(a.HW), "category", "playback", "vaapi", a.HW.VAAPI, "nvenc", a.HW.NVENC, "available", a.HW.Available, "reason", a.HW.DetectionReason)
	}
	go a.sweep()
	return a
}

func (a *API) Routes(r chi.Router) {
	r.With(auth.RequireUser).Put("/progress/{kind}/{id}", a.handleItemProgress)
	r.Route("/playback", func(r chi.Router) {
		r.With(auth.RequireUser).Get("/continue", a.handleContinue)
		r.With(auth.RequireUserOrGuest).Post("/sessions", a.handleCreate)
		a.sessionRoutes(r)
	})
	r.Group(func(r chi.Router) {
		r.Use(auth.RequirePerm(auth.PermStreamsInspect))
		r.Get("/admin/streams", a.handleAdminList)
		r.Get("/admin/streams/{id}", a.handleAdminOne)
		r.Get("/admin/stats", a.handleAdminStats)
		r.Get("/admin/diagnostics/flight-recorder/{id}", a.handleFlightRecorder)
		r.Get("/admin/diagnostics/sources", a.handleSourceReliability)
	})
	if a.PartyRelay != nil {
		r.Handle("/watch-together/*", a.PartyRelay)
	} else if a.WT != nil {
		a.WT.Routes(r)
	}
}

// partyAccess reports whether a party-only account may stream the item
// because it is playing or queued in a watch party the account joined.
func (a *API) partyAccess(principalID, itemKind, itemID string) bool {
	if a.Parties != nil {
		return a.Parties.PartyAccess(principalID, itemKind, itemID)
	}
	return a.WT != nil && a.WT.PartyAccess(principalID, itemKind, itemID)
}

func (a *API) sessionRoutes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(requireSessionCaller)
		r.Get("/sessions/{id}/file", a.handleFile)
		r.Put("/sessions/{id}/progress", a.handleProgress)
		r.Post("/sessions/{id}/telemetry", a.handleTelemetry)
		r.Post("/sessions/{id}/keepalive", a.handleKeepAlive)
		r.Delete("/sessions/{id}", a.handleDelete)
		r.Get("/sessions/{id}/subtitles", a.handleSubtitles)
		r.Get("/sessions/{id}/preview", a.handlePreview)
		r.Get("/sessions/{id}/download", a.handleDownload)
	})
}

// SessionRoutes mounts the per-session routes under /playback for a media
// worker, where callers authenticate with the session stream token.
func (a *API) SessionRoutes(r chi.Router) {
	r.Route("/playback", a.sessionRoutes)
}

// requireSessionCaller admits a signed-in caller or a request carrying a
// stream token; the handler then checks the token against the session.
func requireSessionCaller(next http.Handler) http.Handler {
	guarded := auth.RequireUserOrGuest(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("stoken") != "" {
			next.ServeHTTP(w, r)
			return
		}
		guarded.ServeHTTP(w, r)
	})
}

// HLSRoutes registers playlist + segment GETs on a router that cmd mounts at /hls.
func (s *API) HLSRoutes(r chi.Router) {
	r.Get("/{sessionId}/index.m3u8", s.handlePlaylist)
	r.Get("/{sessionId}/{file}", s.handleSegment)
}

func (s *API) HLSHandler() http.Handler {
	r := chi.NewRouter()
	if s.Log != nil {
		r.Use(func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				ww := &hlsStatus{ResponseWriter: w, code: 200}
				next.ServeHTTP(ww, req)
				s.Log.Info("http", "method", req.Method, "path", req.URL.Path,
					"status", ww.code, "category", "hls")
			})
		})
	}
	s.HLSRoutes(r)
	return r
}

type hlsStatus struct {
	http.ResponseWriter
	code int
}

func (w *hlsStatus) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (a *API) sweep() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()
	a.previews.sweep()
	for {
		select {
		case <-a.stop:
			return
		case <-t.C:
			a.Reg.Expire(a.Lease, a.kill)
			a.HLS.Evict(a.Reg.IDs())
		case <-daily.C:
			a.previews.sweep()
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
	a.flushProgress(context.Background(), s)
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
	if s.remoteStop != nil {
		stop := s.remoteStop
		s.remoteStop = nil
		go stop()
	}
}

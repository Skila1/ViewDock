package main

import (
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/artwork"
	"github.com/viewdock/viewdock/internal/audit"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/backup"
	"github.com/viewdock/viewdock/internal/collections"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/discordbot/interactions"
	"github.com/viewdock/viewdock/internal/download"
	"github.com/viewdock/viewdock/internal/ffmpeg"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/labs"
	"github.com/viewdock/viewdock/internal/library"
	"github.com/viewdock/viewdock/internal/mesh"
	"github.com/viewdock/viewdock/internal/metadata"
	"github.com/viewdock/viewdock/internal/nodeauth"
	"github.com/viewdock/viewdock/internal/oplog"
	"github.com/viewdock/viewdock/internal/playback"
	"github.com/viewdock/viewdock/internal/progress"
	"github.com/viewdock/viewdock/internal/reliability"
	"github.com/viewdock/viewdock/internal/resilience"
	"github.com/viewdock/viewdock/internal/runtimecfg"
	"github.com/viewdock/viewdock/internal/scan"
	"github.com/viewdock/viewdock/internal/search"
	"github.com/viewdock/viewdock/internal/settings"
	"github.com/viewdock/viewdock/internal/setup"
	"github.com/viewdock/viewdock/internal/share"
	"github.com/viewdock/viewdock/internal/update"
	"github.com/viewdock/viewdock/internal/upload"
	"github.com/viewdock/viewdock/internal/users"
	"github.com/viewdock/viewdock/internal/watchtogether"
)

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

type app struct {
	Auth     *auth.Service
	Backend  *backend.Monitor
	Playback *playback.API
	Uploads  *upload.Service
	Meta     *metadata.Service
	Logs     *oplog.Store
	Users    *users.API
	// Mesh is set on control-capable roles, Worker on VD_ROLE=worker.
	Mesh   *mesh.Dispatcher
	Worker *mesh.Worker
	// Interactions and Labs are nil on VD_ROLE=worker.
	Interactions *interactions.Service
	Labs         *labs.Broadcaster
}

func wire(srv *httpapi.Server, sqlDB *sql.DB, cfg config.Config, logger *slog.Logger, kv *settings.Store) *app {
	aud := audit.New(sqlDB)
	authSvc := auth.New(sqlDB, cfg, kv, aud)
	ff := ffmpeg.New()
	libs := library.NewService(sqlDB, authSvc.Grants, ff, ff, cfg.CacheDir)
	libs.Audit, libs.Cfg = aud, cfg
	sc := scan.New(sqlDB, libs, ff)
	libs.SetScan(sc)
	logs := oplog.New(sqlDB)
	art := artwork.New(sqlDB, cfg.CacheDir, ff, metadata.NewClient(kv))
	meta := metadata.New(sqlDB, kv, art)
	worker := cfg.Role == config.RoleWorker
	// controlPlane is every role that serves the application API: not media
	// workers and not a separate party coordinator.
	controlPlane := !worker && cfg.Role != config.RoleCoordinator
	remoteParties := controlPlane && cfg.CoordinatorURL != ""
	sc.OnIdle = func() { go func() { _ = meta.RunOnce(context.Background()) }() }
	if controlPlane {
		meta.Start(context.Background())
	}
	up := upload.New(sqlDB, libs, sc, ff, filepath.Join(cfg.ConfigDir, "uploads"))
	srch := search.New(sqlDB)
	cols := collections.New(sqlDB, libs)
	shareSvc := share.New(sqlDB, libs)
	shareAPI := share.NewAPI(shareSvc, authSvc)
	backendStore := backend.NewWithDialect(sqlDB, db.Dialect(cfg.DatabaseDriver))
	backendStore.Cipher = kv.Cipher()
	backendAPI := backend.NewAPI(backendStore)
	backendAPI.Audit, backendAPI.Cfg = aud, cfg
	var backendMonitor *backend.Monitor
	if controlPlane {
		backendMonitor = backend.NewMonitor(backendAPI.Router, 5*time.Second)
		backendMonitor.Start()
	}
	usersAPI := users.NewWithDialect(sqlDB, authSvc, db.Dialect(cfg.DatabaseDriver))
	setupAPI := setup.New(authSvc, kv, libs, sc, ff)
	kickMeta := func() { meta.NotifyKey(context.Background()) }
	authSvc.OnTMDBKey = kickMeta
	setupAPI.OnTMDBKey = kickMeta
	prog := progress.New(sqlDB)
	flight := diagnostics.New(1000, 256)
	tracker := reliability.New(reliability.Config{HalfLife: time.Hour})
	flight.AddObserver(reliability.FlightObserver{Recorder: tracker})
	overrides := reliability.NewSQLStore(sqlDB)
	if controlPlane {
		if err := tracker.LoadOverrides(context.Background(), overrides); err != nil {
			logger.Warn("reliability overrides", "category", "resilience", "err", err)
		}
	}
	play := playback.New(playback.Deps{
		Cfg: cfg, DB: sqlDB, Log: logger,
		Locator: libs, Grants: authSvc.Grants, Gate: shareSvc,
		Prober: ff, FF: ff, Progress: prog, Catalog: libs,
		Settings: kv,
		Flight:   flight,
		CacheDir: cfg.CacheDir, Slots: 2,
		// Workers get party access from the control plane's assertion.
		NoParties: worker || remoteParties,
	})
	var parties watchtogether.Coordinator
	if play.WT != nil {
		parties = play.WT
	}
	if remoteParties {
		base, err := httpapi.ParseUpstreamURL("VD_COORDINATOR_URL", cfg.CoordinatorURL)
		if err != nil {
			logger.Error("watch party coordinator", "err", err)
		} else {
			remote := watchtogether.NewRemote(base, cfg.CoordinatorSecret, logger)
			parties = remote
			play.Parties = remote
			play.PartyRelay = srv.CoordinatorRelay(base)
		}
	}

	rc := runtimecfg.New(sqlDB, kv, aud, append(configDefs(cfg), backup.ConfigDefs(cfg)...))
	if kv.Cipher() != nil && controlPlane {
		if n, err := rc.EncryptPlaintextSecrets(context.Background()); err != nil {
			logger.Error("encrypt stored secrets", "err", err)
		} else if n > 0 {
			logger.Info("encrypted stored secrets", "count", n)
		}
		if err := authSvc.EncryptDiscordSecret(context.Background()); err != nil {
			logger.Error("encrypt discord client secret", "err", err)
		}
	}
	if err := rc.Load(context.Background()); err != nil {
		logger.Error("runtime configuration", "err", err)
	}
	rc.Watch(context.Background(), 5*time.Second)
	rc.Bind(cfgTranscodeSlots, func(v string) { play.Lim.SetSlots(rc.Int(cfgTranscodeSlots)) })
	rc.Bind(cfgLogRetention, func(string) { logs.SetRetentionDays(rc.Int(cfgLogRetention)) })
	rc.Bind(cfgGuestHours, func(string) { usersAPI.SetMaxGuestHours(rc.Int(cfgGuestHours)) })
	rc.Bind(cfgBlockUnrated, func(string) { library.SetBlockUnrated(rc.Bool(cfgBlockUnrated)) })
	rc.Bind(cfgCertCountry, func(string) { meta.SetCertificationCountry(rc.String(cfgCertCountry)) })
	rc.Bind(cfgFlightRetention, func(string) { flight.SetRetention(time.Duration(rc.Int(cfgFlightRetention)) * time.Hour) })
	rc.Bind(cfgTelemetryBudget, func(string) {
		perMinute := rc.Int(cfgTelemetryBudget)
		flight.SetIngestBudget(perMinute, float64(perMinute)/60)
	})
	if play.WT != nil {
		play.WT.Enabled = func() bool { return rc.Bool(cfgWatchTogether) }
		rc.Bind(cfgHardDrift, func(string) { play.WT.SetHardDriftMS(rc.Int(cfgHardDrift)) })
	}
	rc.OnChange(cfgTMDBKey, func(string) { kickMeta() })
	play.DownloadsEnabled = func() bool { return rc.Bool(cfgDownloads) }
	offlinePolicy := &download.PolicyAPI{
		Grants:     authSvc.Grants,
		Enabled:    func() bool { return rc.Bool(cfgDownloads) },
		MaxItems:   func() int { return rc.Int(cfgOfflineMaxItems) },
		ExpiryDays: func() int { return rc.Int(cfgOfflineExpiryDays) },
		MaxItemGB:  func() int { return rc.Int(cfgOfflineMaxItemGB) },
	}
	authSvc.BotToken = func() string { return rc.String(cfgDiscordBot) }
	authSvc.ApplyConfig = func(ctx context.Context, actorID, ip string, values map[string]string) error {
		changes := make(map[string]runtimecfg.Change, len(values))
		for k, v := range values {
			changes[k] = runtimecfg.Change{Value: v}
		}
		_, err := rc.Apply(ctx, actorID, ip, changes, rc.Version(), "site settings")
		return err
	}
	srv.Features = func() map[string]bool {
		return map[string]bool{"watch_together": rc.Bool(cfgWatchTogether), "downloads": rc.Bool(cfgDownloads)}
	}

	var ix *interactions.Service
	var vcam *labs.Broadcaster
	if controlPlane {
		ix = interactions.New(interactions.Deps{
			DB: sqlDB, Cfg: cfg, Settings: kv, KV: kv, Audit: aud, Log: logger,
			PublicKey:          func() string { return rc.String(cfgDiscordPublicKey) },
			Bot:                authSvc.Bot,
			Accounts:           authSvc,
			Parties:            discordParties{hub: parties},
			Catalog:            discordCatalog{search: srch, grants: authSvc.Grants, libs: libs, hub: parties},
			PartiesEnabled:     func() bool { return parties != nil && rc.Bool(cfgWatchTogether) },
			SaveConfig:         authSvc.ApplyConfig,
			PublicKeyConfigKey: cfgDiscordPublicKey,
		})
		vcam = labs.New(ff.FFmpeg, newLabsSources(parties, libs), kv, filepath.Join(cfg.CacheDir, "labs-vcam"))
		vcam.Log = logger
		labsAPI := &labs.API{B: vcam, Audit: aud, Cfg: cfg, Log: logger}
		srv.APIMounts = append(srv.APIMounts, ix.AdminRoutes, labsAPI.Routes)
	}

	var backups *backup.Service
	if controlPlane {
		backups = backup.New(sqlDB, cfg, aud, logger)
		backups.KeyID = func() string {
			if c := kv.Cipher(); c != nil {
				return c.ID
			}
			return ""
		}
		backups.Settings = func() backup.Settings { return backup.SettingsFrom(rc.String) }
		for _, key := range []string{
			backup.KeySchedule, backup.KeyRetention, backup.KeyDestination,
			backup.KeyS3Endpoint, backup.KeyS3Bucket, backup.KeyS3Prefix, backup.KeyS3Region,
			backup.KeyS3AccessKey, backup.KeyS3SecretKey, backup.KeyS3UseSSL, backup.KeyS3PathStyle,
		} {
			rc.OnChange(key, func(string) { backups.Reschedule() })
		}
		backups.Start(context.Background())
	}

	var dispatcher *mesh.Dispatcher
	var workerAPI *mesh.Worker
	if worker {
		workerAPI = &mesh.Worker{Verifier: nodeauth.NewVerifier(cfg.NodeSecret), Play: play, Log: logger}
	} else if controlPlane {
		dispatcher = mesh.NewDispatcher(backendAPI.Router, cfg, logger)
		dispatcher.Reliability = tracker
		dispatcher.Ranker = tracker
		control := cfg.Role == config.RoleControl
		dispatcher.Enabled = func() bool { return control || rc.Bool(cfgMeshPlayback) }
		play.Remote = dispatcher
		play.RemoteDiag = dispatcher
		resil := resilience.New(resilience.Deps{
			DB: sqlDB, DBDriver: cfg.DatabaseDriver, Role: cfg.Role,
			Nodes: backendStore.List, Progress: play.ProgressStats,
			Flight: flight, Reliability: tracker, Log: logger,
			RemoteSessions: dispatcher.RemoteSessions,
			StorageFn:      backups.Store,
			Checks:         map[string]resilience.Check{"experimental_broadcast": broadcastCheck(vcam)},
			Coordinator: func(context.Context) (resilience.CoordinatorStatus, error) {
				if parties == nil {
					return resilience.CoordinatorStatus{}, nil
				}
				snap := parties.SyncSnapshot()
				if snap == nil {
					return resilience.CoordinatorStatus{}, watchtogether.ErrUnavailable
				}
				rooms, _ := snap["rooms"].(int)
				members, _ := snap["members"].(int)
				return resilience.CoordinatorStatus{
					Available: rc.Bool(cfgWatchTogether), Rooms: rooms, Participants: members,
					Details: map[string]any{
						"connected": snap["connected"], "max_drift_ms": snap["max_drift_ms"],
						"target_ms": snap["target_ms"], "hard_ms": snap["hard_ms"], "corrections": snap["stats"],
					},
				}, nil
			},
		})
		resilAPI := &resilience.API{Service: resil, Overrides: overrides, Audit: aud, Cfg: cfg}
		srv.APIMounts = append(srv.APIMounts, resilAPI.Routes)
	}

	catalogMW := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			p := auth.FromRequest(r)
			if p == nil || !p.IsUser() {
				httpapi.WriteErr(w, http.StatusUnauthorized, "unauthorized", "login required")
				return
			}
			path := r.URL.Path
			write := r.Method != http.MethodGet && r.Method != http.MethodHead
			if write && hasAnyPrefix(path, "/api/v1/libraries", "/api/v1/metadata", "/api/v1/artwork", "/api/v1/movies/", "/api/v1/series/", "/api/v1/episodes/", "/api/v1/collections") && !p.HasPerm(auth.PermLibrariesManage) {
				httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "permission required")
				return
			}
			if strings.HasPrefix(path, "/api/v1/uploads") && !p.IsAdmin {
				httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "admin required")
				return
			}
			ctx := library.WithUserID(r.Context(), p.UserID)
			if !p.IsAdmin {
				ids, err := authSvc.Grants.GrantedLibraryIDs(r.Context(), p.UserID)
				if err != nil || ids == nil {
					ids = []string{}
				}
				ctx = library.WithGrantedIDs(ctx, ids)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}

	srv.APIMounts = append(srv.APIMounts,
		authSvc.Routes,
		setupAPI.Routes,
		backendAPI.Routes,
		usersAPI.Routes,
		shareAPI.Routes,
		func(r chi.Router) {
			r.Group(func(r chi.Router) {
				r.Use(catalogMW)
				libs.Routes(r)
				sc.Routes(r)
				meta.Routes(r)
				art.Routes(r)
				up.Routes(r)
				srch.Routes(r)
				cols.Routes(r)
			})
		},
		play.Routes,
		offlinePolicy.Routes,
		rc.Routes(cfg),
		func(r chi.Router) {
			if backups != nil {
				backups.Routes(r)
			}
		},
		update.Routes(kv),
		logs.Routes,
	)
	return &app{Auth: authSvc, Backend: backendMonitor, Playback: play, Uploads: up, Meta: meta, Logs: logs, Users: usersAPI, Mesh: dispatcher, Worker: workerAPI, Interactions: ix, Labs: vcam}
}

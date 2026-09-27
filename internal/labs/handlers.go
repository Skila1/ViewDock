package labs

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/config"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Auditor records admin actions. *audit.Log implements it.
type Auditor interface {
	Event(ctx context.Context, actorID, action, target, ip, detail string)
}

// API serves the Labs admin endpoints. Every route requires an administrator.
type API struct {
	B     *Broadcaster
	Audit Auditor
	Cfg   config.Config
	Log   *slog.Logger
}

func (a *API) Routes(r chi.Router) {
	r.Group(func(r chi.Router) {
		r.Use(auth.RequireAdmin)
		r.Get("/admin/labs/vcam", a.handleGet)
		r.Get("/admin/labs/vcam/health", a.handleHealth)
		r.Get("/admin/labs/vcam/preview", a.handlePreview)
		r.Get("/admin/labs/vcam/parties", a.handleParties)
		r.Post("/admin/labs/vcam/acknowledge", a.handleAck)
		r.Delete("/admin/labs/vcam/acknowledge", a.handleRevoke)
		r.Put("/admin/labs/vcam/config", a.handleConfig)
		r.With(auth.RateLimit(a.Cfg, 20, time.Minute)).Post("/admin/labs/vcam/start", a.handleStart)
		r.Post("/admin/labs/vcam/stop", a.handleStop)
	})
}

func (a *API) log() *slog.Logger {
	if a.Log != nil {
		return a.Log
	}
	return slog.Default()
}

func (a *API) audit(r *http.Request, action, detail string) {
	if a.Audit == nil {
		return
	}
	actor := ""
	if p := auth.FromRequest(r); p != nil {
		actor = p.UserID
	}
	a.Audit.Event(r.Context(), actor, action, "labs.vcam", httpapi.ClientIPString(r, a.Cfg), detail)
}

type configView struct {
	Config
	OutputURLSet  bool   `json:"output_url_set"`
	OutputDisplay string `json:"output_display,omitempty"`
}

func viewOf(c Config) configView {
	return configView{Config: c, OutputURLSet: c.OutputURL != "", OutputDisplay: RedactURL(c.OutputURL)}
}

func (a *API) serverErr(w http.ResponseWriter, what string, err error) {
	a.log().Error("labs "+what, "category", "labs", "err", err)
	httpapi.WriteErr(w, http.StatusInternalServerError, "labs", what+" failed")
}

func (a *API) handleGet(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ack, err := loadAck(ctx, a.B.Store)
	if err != nil {
		a.serverErr(w, "loading the acknowledgment", err)
		return
	}
	cfg, cfgErr := loadConfig(ctx, a.B.Store)
	health := a.B.Health()
	if ack == nil && !a.B.Running() {
		health.State = StateDisabled
	}
	out := map[string]any{
		"notice":         map[string]string{"version": NoticeVersion, "text": NoticeText},
		"acknowledgment": ack,
		"config":         viewOf(cfg),
		"capabilities":   a.B.Capabilities(ctx, r.URL.Query().Get("refresh") == "1"),
		"health":         health,
		"running":        a.B.Running(),
		"master_key":     a.B.Store.Cipher() != nil,
	}
	if cfgErr != nil {
		a.log().Warn("labs configuration", "category", "labs", "err", cfgErr)
		out["config_error"] = "the stored configuration could not be read; save it again"
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (a *API) handleHealth(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, http.StatusOK, a.B.Health())
}

func (a *API) handlePreview(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "" && kind != "outgoing" && kind != "raw" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "kind must be outgoing or raw")
		return
	}
	data, at, err := a.B.Preview(kind)
	if errors.Is(err, ErrNoPreview) {
		httpapi.WriteErr(w, http.StatusNotFound, "no_preview", err.Error())
		return
	}
	if err != nil {
		a.serverErr(w, "reading the preview", err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Frame-Time", at.UTC().Format(time.RFC3339))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (a *API) handleParties(w http.ResponseWriter, r *http.Request) {
	if a.B.Sources == nil {
		httpapi.WriteJSON(w, http.StatusOK, []Party{})
		return
	}
	list, err := a.B.Sources.ActiveParties(r.Context())
	if err != nil {
		a.serverErr(w, "listing parties", err)
		return
	}
	if list == nil {
		list = []Party{}
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (a *API) handleAck(w http.ResponseWriter, r *http.Request) {
	var body struct {
		NoticeVersion string `json:"notice_version"`
		Accept        bool   `json:"accept"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if !body.Accept {
		httpapi.WriteErr(w, http.StatusBadRequest, "labs_ack", "the risk notice must be accepted explicitly")
		return
	}
	if body.NoticeVersion != NoticeVersion {
		httpapi.WriteErr(w, http.StatusConflict, "notice_changed", "the risk notice changed; review it again")
		return
	}
	ack := &Acknowledgment{UserID: auth.FromRequest(r).UserID, At: time.Now().UTC(), NoticeVersion: NoticeVersion}
	if err := saveAck(r.Context(), a.B.Store, ack); err != nil {
		a.serverErr(w, "saving the acknowledgment", err)
		return
	}
	a.audit(r, "labs.vcam.acknowledge", "notice="+NoticeVersion)
	httpapi.WriteJSON(w, http.StatusOK, ack)
}

func (a *API) handleRevoke(w http.ResponseWriter, r *http.Request) {
	if err := a.B.Stop(r.Context()); err != nil {
		a.serverErr(w, "stopping the broadcaster", err)
		return
	}
	if err := saveAck(r.Context(), a.B.Store, nil); err != nil {
		a.serverErr(w, "withdrawing the acknowledgment", err)
		return
	}
	a.audit(r, "labs.vcam.disable", "")
	httpapi.WriteOK(w)
}

func (a *API) handleConfig(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ack, err := loadAck(ctx, a.B.Store)
	if err != nil {
		a.serverErr(w, "loading the acknowledgment", err)
		return
	}
	if ack == nil {
		httpapi.WriteErr(w, http.StatusForbidden, "labs_not_acknowledged", ErrNotAcknowledged.Error())
		return
	}
	var body struct {
		Mode      string    `json:"mode"`
		Device    string    `json:"device"`
		Width     int       `json:"width"`
		Height    int       `json:"height"`
		FPS       int       `json:"fps"`
		VideoKbps int       `json:"video_kbps"`
		AudioKbps int       `json:"audio_kbps"`
		Threads   int       `json:"threads"`
		Selection Selection `json:"selection"`
		OutputURL *string   `json:"output_url"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	cur, err := loadConfig(ctx, a.B.Store)
	if err != nil {
		// An unreadable stored output URL is replaced by whatever is saved now.
		a.log().Warn("labs configuration", "category", "labs", "err", err)
	}
	next := Config{
		Mode: body.Mode, Device: body.Device, Width: body.Width, Height: body.Height, FPS: body.FPS,
		VideoKbps: body.VideoKbps, AudioKbps: body.AudioKbps, Threads: body.Threads, Selection: body.Selection,
		OutputURL: cur.OutputURL,
	}
	if err := next.Normalize(); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "labs_config", err.Error())
		return
	}
	outputChanged := body.OutputURL != nil
	if outputChanged {
		next.OutputURL = strings.TrimSpace(*body.OutputURL)
	}
	if next.Mode == ModeV4L2 {
		if next.OutputURL != "" {
			next.OutputURL, outputChanged = "", true
		}
	} else if next.OutputURL != "" {
		if _, err := ValidateOutputURL(ctx, next.Mode, next.OutputURL, a.B.Resolver); err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "labs_output", err.Error())
			return
		}
	}
	if !next.Selection.empty() && a.B.Sources != nil {
		if _, err := a.B.Sources.Resolve(ctx, next.Selection); err != nil {
			httpapi.WriteErr(w, http.StatusBadRequest, "labs_selection", "the selected party or title is not available")
			return
		}
	}
	next.UpdatedAt, next.UpdatedBy = time.Now().UTC(), auth.FromRequest(r).UserID
	if err := saveConfig(ctx, a.B.Store, next, outputChanged); err != nil {
		if errors.Is(err, ErrNoCipher) {
			httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_master_key", err.Error())
			return
		}
		a.serverErr(w, "saving the configuration", err)
		return
	}
	detail := "mode=" + next.Mode
	if outputChanged {
		detail += " output=" + RedactURL(next.OutputURL)
	}
	a.audit(r, "labs.vcam.config", detail)
	httpapi.WriteJSON(w, http.StatusOK, map[string]any{"config": viewOf(next), "restart_required": a.B.Running()})
}

func (a *API) handleStart(w http.ResponseWriter, r *http.Request) {
	err := a.B.Start(r.Context())
	var unavailable *UnavailableError
	var cfgErr *ConfigError
	switch {
	case err == nil:
		a.audit(r, "labs.vcam.start", "")
		httpapi.WriteJSON(w, http.StatusOK, a.B.Health())
	case errors.Is(err, ErrNotAcknowledged):
		httpapi.WriteErr(w, http.StatusForbidden, "labs_not_acknowledged", err.Error())
	case errors.Is(err, ErrAlreadyRunning):
		httpapi.WriteErr(w, http.StatusConflict, "labs_running", err.Error())
	case errors.Is(err, ErrNoSelection), errors.As(err, &cfgErr):
		httpapi.WriteErr(w, http.StatusBadRequest, "labs_config", err.Error())
	case errors.As(err, &unavailable):
		a.audit(r, "labs.vcam.start_blocked", unavailable.Reason)
		httpapi.WriteErr(w, http.StatusConflict, "labs_unavailable", unavailable.Reason)
	default:
		a.serverErr(w, "starting the broadcaster", err)
	}
}

func (a *API) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := a.B.Stop(r.Context()); err != nil {
		a.serverErr(w, "stopping the broadcaster", err)
		return
	}
	a.audit(r, "labs.vcam.stop", "")
	httpapi.WriteJSON(w, http.StatusOK, a.B.Health())
}

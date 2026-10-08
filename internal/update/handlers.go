package update

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/settings"
)

func Routes(kv *settings.Store) httpapi.RouteMount {
	return func(r chi.Router) {
		r.Group(func(r chi.Router) {
			r.Use(auth.RequireAdmin)
			r.Get("/admin/updates", func(w http.ResponseWriter, req *http.Request) {
				httpapi.WriteJSON(w, http.StatusOK, Load(req.Context(), kv))
			})
			r.Put("/admin/updates", func(w http.ResponseWriter, req *http.Request) {
				var body struct {
					AutoEnabled *bool `json:"auto_enabled"`
				}
				_ = json.NewDecoder(req.Body).Decode(&body)
				if body.AutoEnabled != nil {
					if err := SetAuto(req.Context(), kv, *body.AutoEnabled); err != nil {
						httpapi.WriteErr(w, http.StatusInternalServerError, "db", "could not save update settings")
						return
					}
				}
				httpapi.WriteJSON(w, http.StatusOK, Load(req.Context(), kv))
			})
			r.Post("/admin/updates/check", func(w http.ResponseWriter, req *http.Request) {
				st, _ := Check(req.Context(), kv)
				httpapi.WriteJSON(w, http.StatusOK, st)
			})
			r.Post("/admin/updates/apply", func(w http.ResponseWriter, req *http.Request) {
				if !CanApply() {
					httpapi.WriteErr(w, http.StatusServiceUnavailable, "no_helper", "The host update helper is not running. Re-run the installer so viewdock-update can pull images on the host.")
					return
				}
				who := "admin"
				if p := auth.FromRequest(req); p != nil && p.Username != "" {
					who = p.Username
				}
				started, err := BeginApply(req.Context(), kv, who)
				if err != nil {
					httpapi.WriteErr(w, http.StatusInternalServerError, "update", err.Error())
					return
				}
				if started {
					go func() {
						ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
						defer cancel()
						_ = RunApply(ctx, kv, who)
					}()
				}
				httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{
					"ok":      true,
					"message": "The host is pulling the new image. ViewDock stays up until the new container starts.",
				})
			})
		})
	}
}

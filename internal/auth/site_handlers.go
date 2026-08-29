package auth

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/viewdock/viewdock/internal/httpapi"
)

const (
	settingPublicURL = "app.public_url"
	settingTMDBKey   = "tmdb.api_key"
)

type siteSettings struct {
	PublicURL      string `json:"public_url"`
	TMDBConfigured bool   `json:"tmdb_configured"`
	TMDBKeySet     bool   `json:"tmdb_api_key_set"`
}

func (s *Service) siteSettings(r *http.Request) siteSettings {
	public := httpapi.ResolvePublicURL(r.Context(), s.Cfg, s.Settings)
	tmdb := strings.TrimSpace(s.Cfg.TMDBAPIKey)
	if tmdb == "" && s.Settings != nil {
		tmdb, _ = s.Settings.Get(r.Context(), settingTMDBKey)
	}
	return siteSettings{
		PublicURL:      public,
		TMDBConfigured: strings.TrimSpace(tmdb) != "",
		TMDBKeySet:     strings.TrimSpace(tmdb) != "",
	}
}

func (s *Service) handleAdminSiteGet(w http.ResponseWriter, r *http.Request) {
	httpapi.WriteJSON(w, 200, s.siteSettings(r))
}

func (s *Service) handleAdminSitePut(w http.ResponseWriter, r *http.Request) {
	if s.Settings == nil {
		httpapi.WriteErr(w, 500, "settings", "settings store unavailable")
		return
	}
	var body struct {
		PublicURL  *string `json:"public_url"`
		TMDBAPIKey *string `json:"tmdb_api_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if body.PublicURL != nil {
		u := strings.TrimRight(strings.TrimSpace(*body.PublicURL), "/")
		if err := s.Settings.Set(r.Context(), settingPublicURL, u); err != nil {
			httpapi.WriteErr(w, 500, "settings", err.Error())
			return
		}
	}
	if body.TMDBAPIKey != nil && strings.TrimSpace(*body.TMDBAPIKey) != "" {
		if err := s.Settings.Set(r.Context(), settingTMDBKey, strings.TrimSpace(*body.TMDBAPIKey)); err != nil {
			httpapi.WriteErr(w, 500, "settings", err.Error())
			return
		}
		if s.OnTMDBKey != nil {
			s.OnTMDBKey()
		}
	}
	httpapi.WriteJSON(w, 200, s.siteSettings(r))
}

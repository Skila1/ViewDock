package library

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// Profile-owned lists: My List (stored in favourites), playlists, watch
// history, Continue Watching dismissal and remembered tracks. Everything is
// keyed by the signed-in user, so each profile keeps its own.

const maxPlaylistName = 80

func (s *Service) profileRoutes(r chi.Router) {
	r.Get("/me/watchlist", s.handleWatchlist)
	r.Put("/me/watchlist/{kind}/{id}", s.handleWatchlistAdd)
	r.Delete("/me/watchlist/{kind}/{id}", s.handleWatchlistRemove)

	r.Get("/me/playlists", s.handlePlaylists)
	r.Post("/me/playlists", s.handlePlaylistCreate)
	r.Patch("/me/playlists/{id}", s.handlePlaylistRename)
	r.Delete("/me/playlists/{id}", s.handlePlaylistDelete)
	r.Post("/me/playlists/{id}/items", s.handlePlaylistAddItem)
	r.Delete("/me/playlists/{id}/items/{kind}/{itemID}", s.handlePlaylistRemoveItem)

	r.Get("/me/history", s.handleHistory)
	r.Delete("/me/history", s.handleHistoryClear)
	r.Delete("/me/history/{id}", s.handleHistoryDelete)

	r.Put("/titles/{kind}/{id}/continue", s.handleDismissContinue)
	r.Put("/titles/{kind}/{id}/tracks", s.handleTitleTracks)
}

// profileUser answers 403 for callers without a profile (guests, API clients
// acting for nobody).
func profileUser(w http.ResponseWriter, r *http.Request) string {
	id := UserIDFrom(r.Context())
	if id == "" {
		httpapi.WriteErr(w, http.StatusForbidden, "forbidden", "only signed-in profiles keep lists")
	}
	return id
}

func titleKind(w http.ResponseWriter, kind string) bool {
	if kind != "movie" && kind != "series" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "kind must be movie or series")
		return false
	}
	return true
}

type listEntry struct {
	Kind    string `json:"kind"`
	ID      string `json:"id"`
	AddedAt string `json:"added_at"`
}

// visibleEntries keeps the entries the caller may still see.
func (s *Service) visibleEntries(r *http.Request, rows *sql.Rows) ([]listEntry, error) {
	defer rows.Close()
	v, err := NewVisibility(r.Context(), s.DB)
	if err != nil {
		return nil, err
	}
	var all []listEntry
	for rows.Next() {
		var e listEntry
		if err := rows.Scan(&e.Kind, &e.ID, &e.AddedAt); err != nil {
			return nil, err
		}
		all = append(all, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []listEntry{}
	for _, e := range all {
		if ok, err := v.Item(r.Context(), e.Kind, e.ID); err == nil && ok {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *Service) handleWatchlist(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	if userID == "" {
		return
	}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT item_kind, item_id, created_at FROM favourites WHERE user_id = ? ORDER BY created_at DESC`, userID)
	if err != nil {
		writeListErr(w, err)
		return
	}
	list, err := s.visibleEntries(r, rows)
	if err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

func (s *Service) handleWatchlistAdd(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if userID == "" || !titleKind(w, kind) || !s.visibleTitle(w, r, kind, id) {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `
		INSERT INTO favourites(user_id, item_kind, item_id, created_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(user_id, item_kind, item_id) DO NOTHING
	`, userID, kind, id, time.Now().UTC().Format(time.RFC3339)); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleWatchlistRemove(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	if userID == "" {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM favourites WHERE user_id = ? AND item_kind = ? AND item_id = ?`,
		userID, chi.URLParam(r, "kind"), chi.URLParam(r, "id")); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

type playlist struct {
	ID        string      `json:"id"`
	Name      string      `json:"name"`
	CreatedAt string      `json:"created_at"`
	UpdatedAt string      `json:"updated_at"`
	Items     []listEntry `json:"items"`
}

func (s *Service) handlePlaylists(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	if userID == "" {
		return
	}
	rows, err := s.DB.QueryContext(r.Context(), `SELECT id, name, created_at, updated_at FROM user_playlists WHERE user_id = ? ORDER BY updated_at DESC`, userID)
	if err != nil {
		writeListErr(w, err)
		return
	}
	var lists []playlist
	for rows.Next() {
		var p playlist
		if err := rows.Scan(&p.ID, &p.Name, &p.CreatedAt, &p.UpdatedAt); err != nil {
			rows.Close()
			writeListErr(w, err)
			return
		}
		lists = append(lists, p)
	}
	rows.Close()
	out := []playlist{}
	for _, p := range lists {
		items, err := s.DB.QueryContext(r.Context(), `SELECT item_kind, item_id, added_at FROM user_playlist_items WHERE playlist_id = ? ORDER BY position, added_at`, p.ID)
		if err != nil {
			writeListErr(w, err)
			return
		}
		if p.Items, err = s.visibleEntries(r, items); err != nil {
			writeListErr(w, err)
			return
		}
		out = append(out, p)
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func playlistName(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Name string `json:"name"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return "", false
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || len(name) > maxPlaylistName {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "a playlist needs a name of up to 80 characters")
		return "", false
	}
	return name, true
}

func (s *Service) handlePlaylistCreate(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	if userID == "" {
		return
	}
	name, ok := playlistName(w, r)
	if !ok {
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	p := playlist{ID: uuid.NewString(), Name: name, CreatedAt: now, UpdatedAt: now, Items: []listEntry{}}
	if _, err := s.DB.ExecContext(r.Context(), `INSERT INTO user_playlists(id, user_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		p.ID, userID, p.Name, now, now); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusCreated, p)
}

// ownPlaylist answers 404 unless the playlist belongs to the caller.
func (s *Service) ownPlaylist(w http.ResponseWriter, r *http.Request, userID, id string) bool {
	var n int
	err := s.DB.QueryRowContext(r.Context(), `SELECT 1 FROM user_playlists WHERE id = ? AND user_id = ?`, id, userID).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		httpapi.WriteErr(w, http.StatusNotFound, "not_found", "not found")
		return false
	}
	if err != nil {
		writeListErr(w, err)
		return false
	}
	return true
}

func (s *Service) touchPlaylist(r *http.Request, id string) {
	_, _ = s.DB.ExecContext(r.Context(), `UPDATE user_playlists SET updated_at = ? WHERE id = ?`, time.Now().UTC().Format(time.RFC3339), id)
}

func (s *Service) handlePlaylistRename(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	id := chi.URLParam(r, "id")
	if userID == "" || !s.ownPlaylist(w, r, userID, id) {
		return
	}
	name, ok := playlistName(w, r)
	if !ok {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE user_playlists SET name = ?, updated_at = ? WHERE id = ?`,
		name, time.Now().UTC().Format(time.RFC3339), id); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handlePlaylistDelete(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	id := chi.URLParam(r, "id")
	if userID == "" || !s.ownPlaylist(w, r, userID, id) {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM user_playlists WHERE id = ?`, id); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handlePlaylistAddItem(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	id := chi.URLParam(r, "id")
	if userID == "" || !s.ownPlaylist(w, r, userID, id) {
		return
	}
	var body struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	if !titleKind(w, body.Kind) || !s.visibleTitle(w, r, body.Kind, body.ID) {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `
		INSERT INTO user_playlist_items(playlist_id, item_kind, item_id, position, added_at)
		VALUES (?, ?, ?, (SELECT COALESCE(MAX(position), 0) + 1 FROM user_playlist_items WHERE playlist_id = ?), ?)
		ON CONFLICT(playlist_id, item_kind, item_id) DO NOTHING
	`, id, body.Kind, body.ID, id, time.Now().UTC().Format(time.RFC3339)); err != nil {
		writeListErr(w, err)
		return
	}
	s.touchPlaylist(r, id)
	httpapi.WriteOK(w)
}

func (s *Service) handlePlaylistRemoveItem(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	id := chi.URLParam(r, "id")
	if userID == "" || !s.ownPlaylist(w, r, userID, id) {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM user_playlist_items WHERE playlist_id = ? AND item_kind = ? AND item_id = ?`,
		id, chi.URLParam(r, "kind"), chi.URLParam(r, "itemID")); err != nil {
		writeListErr(w, err)
		return
	}
	s.touchPlaylist(r, id)
	httpapi.WriteOK(w)
}

type historyEntry struct {
	ID         string `json:"id"`
	ItemKind   string `json:"item_kind"`
	ItemID     string `json:"item_id"`
	Title      string `json:"title"`
	SeriesID   string `json:"series_id,omitempty"`
	Season     int    `json:"season,omitempty"`
	Number     int    `json:"number,omitempty"`
	WatchedAt  string `json:"watched_at"`
	PositionMS int64  `json:"position_ms"`
	DurationMS int64  `json:"duration_ms"`
	Completed  bool   `json:"completed"`
}

const historyPage = 100

// handleHistory lists the profile's watch history, newest first, one entry
// per start or finish of a title. before pages back by watched_at.
func (s *Service) handleHistory(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	if userID == "" {
		return
	}
	before := r.URL.Query().Get("before")
	if before == "" {
		before = "9999"
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > historyPage {
		limit = historyPage
	}
	rows, err := s.DB.QueryContext(r.Context(), `
		SELECT h.id, h.item_kind, h.item_id, h.watched_at, h.position_ms,
			COALESCE(p.duration_ms, 0), COALESCE(p.completed, 0),
			COALESCE(m.title, s.title, ''), COALESCE(e.series_id, ''), COALESCE(e.season, 0), COALESCE(e.number, 0), COALESCE(e.title, '')
		FROM watch_history h
		LEFT JOIN playback_progress p ON p.user_id = h.user_id AND p.item_kind = h.item_kind AND p.item_id = h.item_id
		LEFT JOIN movies m ON h.item_kind = 'movie' AND m.id = h.item_id
		LEFT JOIN episodes e ON h.item_kind = 'episode' AND e.id = h.item_id
		LEFT JOIN series s ON s.id = e.series_id
		WHERE h.user_id = ? AND h.watched_at < ?
		ORDER BY h.watched_at DESC
		LIMIT ?
	`, userID, before, limit*2)
	if err != nil {
		writeListErr(w, err)
		return
	}
	defer rows.Close()
	v, err := NewVisibility(r.Context(), s.DB)
	if err != nil {
		writeListErr(w, err)
		return
	}
	var all []historyEntry
	for rows.Next() {
		var e historyEntry
		var done int
		var epTitle string
		if err := rows.Scan(&e.ID, &e.ItemKind, &e.ItemID, &e.WatchedAt, &e.PositionMS, &e.DurationMS, &done, &e.Title, &e.SeriesID, &e.Season, &e.Number, &epTitle); err != nil {
			writeListErr(w, err)
			return
		}
		e.Completed = done == 1
		if e.ItemKind == "episode" && epTitle != "" {
			e.Title += " - " + epTitle
		}
		all = append(all, e)
	}
	rows.Close()
	out := []historyEntry{}
	for _, e := range all {
		if len(out) == limit {
			break
		}
		if ok, err := v.Item(r.Context(), e.ItemKind, e.ItemID); err == nil && ok {
			out = append(out, e)
		}
	}
	httpapi.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) handleHistoryDelete(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	if userID == "" {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM watch_history WHERE id = ? AND user_id = ?`, chi.URLParam(r, "id"), userID); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

func (s *Service) handleHistoryClear(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	if userID == "" {
		return
	}
	if _, err := s.DB.ExecContext(r.Context(), `DELETE FROM watch_history WHERE user_id = ?`, userID); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

// handleDismissContinue hides a movie or episode from Continue Watching
// without marking it watched. Playing it again brings it back.
func (s *Service) handleDismissContinue(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if userID == "" {
		return
	}
	if kind != "movie" && kind != "episode" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "kind must be movie or episode")
		return
	}
	var body struct {
		Dismissed bool `json:"dismissed"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	flag := 0
	if body.Dismissed {
		flag = 1
	}
	if _, err := s.DB.ExecContext(r.Context(), `UPDATE playback_progress SET dismissed = ? WHERE user_id = ? AND item_kind = ? AND item_id = ?`,
		flag, userID, kind, id); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

// TitleTrackScope is where a remembered track choice applies: the movie, or
// the whole series for an episode, so a show keeps its tracks across episodes.
func TitleTrackScope(ctx context.Context, q rowQueryer, kind, id string) (string, string, error) {
	if kind != "episode" {
		return kind, id, nil
	}
	var seriesID string
	err := q.QueryRowContext(ctx, `SELECT series_id FROM episodes WHERE id = ?`, id).Scan(&seriesID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", ErrNotFound
	}
	if err != nil {
		return "", "", err
	}
	return "series", seriesID, nil
}

// ProfileTracks returns the tracks a profile picked for a title (or its
// series): audio -1 and subtitle -2 when none was picked, subtitle -1 for off.
func ProfileTracks(ctx context.Context, q rowQueryer, userID, kind, id string) (audio, subtitle int) {
	audio, subtitle = -1, -2
	scopeKind, scopeID, err := TitleTrackScope(ctx, q, kind, id)
	if err != nil {
		return
	}
	_ = q.QueryRowContext(ctx, `SELECT audio_index, subtitle_index FROM title_track_prefs WHERE user_id = ? AND scope_kind = ? AND scope_id = ?`,
		userID, scopeKind, scopeID).Scan(&audio, &subtitle)
	return
}

// handleTitleTracks remembers the audio and subtitle tracks a profile picked.
func (s *Service) handleTitleTracks(w http.ResponseWriter, r *http.Request) {
	userID := profileUser(w, r)
	kind, id := chi.URLParam(r, "kind"), chi.URLParam(r, "id")
	if userID == "" {
		return
	}
	if kind != "movie" && kind != "episode" {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "kind must be movie or episode")
		return
	}
	var body struct {
		AudioIndex    *int `json:"audio_index"`
		SubtitleIndex *int `json:"subtitle_index"`
	}
	if err := httpapi.ReadJSON(r, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	scopeKind, scopeID, err := TitleTrackScope(r.Context(), s.DB, kind, id)
	if err != nil {
		writeListErr(w, err)
		return
	}
	audio, sub := -1, -2
	if body.AudioIndex != nil && *body.AudioIndex >= 0 {
		audio = *body.AudioIndex
	}
	if body.SubtitleIndex != nil && *body.SubtitleIndex >= -1 {
		sub = *body.SubtitleIndex
	}
	if _, err := s.DB.ExecContext(r.Context(), `
		INSERT INTO title_track_prefs(user_id, scope_kind, scope_id, audio_index, subtitle_index, updated_at) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id, scope_kind, scope_id) DO UPDATE SET
			audio_index = CASE WHEN excluded.audio_index >= 0 THEN excluded.audio_index ELSE title_track_prefs.audio_index END,
			subtitle_index = CASE WHEN excluded.subtitle_index >= -1 THEN excluded.subtitle_index ELSE title_track_prefs.subtitle_index END,
			updated_at = excluded.updated_at
	`, userID, scopeKind, scopeID, audio, sub, time.Now().UTC().Format(time.RFC3339)); err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteOK(w)
}

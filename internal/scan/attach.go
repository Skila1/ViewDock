package scan

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/library"
)

func (s *Scanner) Routes(r chi.Router) {
	r.Post("/media-files/{id}/attach", s.handleAttach)
}

type attachBody struct {
	Kind     string `json:"kind"`
	MovieID  string `json:"movie_id"`
	SeriesID string `json:"series_id"`
	Title    string `json:"title"`
	Year     int    `json:"year"`
	Season   int    `json:"season"`
	Number   int    `json:"number"`
}

func (s *Scanner) handleAttach(w http.ResponseWriter, r *http.Request) {
	var body attachBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		httpapi.WriteErr(w, 400, "bad_request", "invalid json")
		return
	}
	if err := s.Attach(r.Context(), chi.URLParam(r, "id"), body); err != nil {
		httpapi.WriteErr(w, 400, "attach", err.Error())
		return
	}
	httpapi.WriteOK(w)
}

func (s *Scanner) Attach(ctx context.Context, fileID string, body attachBody) error {
	var libraryID string
	err := s.DB.QueryRowContext(ctx, `SELECT library_id FROM media_files WHERE id = ?`, fileID).Scan(&libraryID)
	if errors.Is(err, sql.ErrNoRows) {
		return library.ErrNotFound
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	kind := strings.ToLower(strings.TrimSpace(body.Kind))
	switch kind {
	case KindMovie, "movies":
		kind = KindMovie
		movieID := body.MovieID
		if movieID == "" {
			p := ParseResult{Title: strings.TrimSpace(body.Title), Year: body.Year, Confidence: ConfHigh}
			if p.Title == "" {
				return errors.New("title or movie_id required")
			}
			movieID, err = s.ensureMovie(ctx, tx, libraryID, p, now, "")
			if err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE media_files SET kind = 'movie', extra_kind = '', movie_id = ?, parse_confidence = 'high', updated_at = ?
			WHERE id = ?
		`, movieID, now, fileID)
	case KindEpisode, "tv", "episodes":
		kind = KindEpisode
		p := ParseResult{
			Title: strings.TrimSpace(body.Title), Year: body.Year,
			Season: body.Season, Episodes: []int{body.Number}, Confidence: ConfHigh,
		}
		if body.SeriesID != "" {
			var title string
			if err := tx.QueryRowContext(ctx, `SELECT title FROM series WHERE id = ?`, body.SeriesID).Scan(&title); err != nil {
				return err
			}
			p.Title = title
		}
		if p.Title == "" {
			return errors.New("title or series_id required")
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM media_file_episodes WHERE media_file_id = ?`, fileID); err != nil {
			return err
		}
		if err := s.ensureEpisodes(ctx, tx, libraryID, fileID, p, now); err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `
			UPDATE media_files SET kind = 'episode', extra_kind = '', movie_id = NULL, parse_confidence = 'high', updated_at = ?
			WHERE id = ?
		`, now, fileID)
	default:
		return errors.New("kind must be movie or episode")
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

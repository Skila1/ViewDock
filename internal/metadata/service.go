package metadata

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/viewdock/viewdock/internal/library"
)

type Artwork interface {
	FetchTMDB(ctx context.Context, kind, itemKind, itemID, tmdbPath string) error
}

type Service struct {
	DB   *sql.DB
	TMDB *Client
	Art  Artwork
	mu   sync.Mutex
}

func New(db *sql.DB, keys KeyStore, art Artwork) *Service {
	return &Service{DB: db, TMDB: NewClient(keys), Art: art}
}

func NewWithClient(db *sql.DB, c *Client, art Artwork) *Service {
	return &Service{DB: db, TMDB: c, Art: art}
}

func (s *Service) HasKey(ctx context.Context) bool {
	return s.TMDB != nil && s.TMDB.APIKey(ctx) != ""
}

// NotifyKey drains the match queue after a key is added (setup / settings).
func (s *Service) NotifyKey(ctx context.Context) {
	if !s.HasKey(ctx) {
		return
	}
	go func() { _ = s.RunOnce(context.Background()) }()
}

func (s *Service) TryAutoMatch(ctx context.Context, itemKind, itemID string) error {
	if !s.HasKey(ctx) {
		return errors.New("no tmdb key")
	}
	if s.locked(ctx, itemKind, itemID, "tmdb_id") || s.locked(ctx, itemKind, itemID, "match") {
		return nil
	}
	title, year, extra, conf, err := s.itemMeta(ctx, itemKind, itemID)
	if err != nil {
		return err
	}
	if extra {
		return nil
	}
	if conf != "high" && conf != "medium" && conf != "" {
		return nil
	}
	cands, err := s.searchCached(ctx, itemKind, title, year)
	if err != nil {
		return err
	}
	scored := Score(title, year, cands)
	win, ok := UniqueWinner(scored)
	if !ok {
		return nil
	}
	return s.ApplyMatch(ctx, itemKind, itemID, win.ID, false)
}

func (s *Service) ApplyMatch(ctx context.Context, itemKind, itemID string, tmdbID int, manual bool) error {
	det, err := s.detailsCached(ctx, itemKind, tmdbID)
	if err != nil {
		return err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	title := det.DisplayTitle()
	year := det.Year()
	overview := det.Overview
	if s.locked(ctx, itemKind, itemID, "title") {
		title = ""
	}
	if s.locked(ctx, itemKind, itemID, "overview") {
		overview = ""
	}
	if s.locked(ctx, itemKind, itemID, "year") {
		year = 0
	}

	table := "movies"
	if itemKind == "series" {
		table = "series"
	}
	sets := `metadata_source = 'tmdb', unmatched = 0, tmdb_id = ?, updated_at = ?`
	args := []any{tmdbID, now}
	if title != "" {
		sets += `, title = ?, sort_title = ?`
		args = append(args, title, sortTitle(title))
	}
	if overview != "" {
		sets += `, overview = ?`
		args = append(args, overview)
	}
	if year > 0 {
		sets += `, year = ?`
		args = append(args, year)
	}
	args = append(args, itemID)
	_, err = s.DB.ExecContext(ctx, `UPDATE `+table+` SET `+sets+` WHERE id = ?`, args...)
	if err != nil {
		return err
	}
	if title != "" {
		_ = library.UpsertFTS(ctx, s.DB, itemKind, itemID, title, year, "")
	}
	if manual {
		_, _ = s.DB.ExecContext(ctx, `
			INSERT OR IGNORE INTO metadata_locks(item_kind, item_id, field) VALUES (?, ?, 'tmdb_id')
		`, itemKind, itemID)
	}
	_, _ = s.DB.ExecContext(ctx, `DELETE FROM match_queue WHERE item_kind = ? AND item_id = ?`, itemKind, itemID)
	if s.Art != nil {
		if det.Poster != "" && !s.locked(ctx, itemKind, itemID, "poster") {
			_ = s.Art.FetchTMDB(ctx, "poster", itemKind, itemID, det.Poster)
		}
		if det.Backdrop != "" && !s.locked(ctx, itemKind, itemID, "backdrop") {
			_ = s.Art.FetchTMDB(ctx, "backdrop", itemKind, itemID, det.Backdrop)
		}
	}
	return nil
}

func (s *Service) itemMeta(ctx context.Context, itemKind, itemID string) (title string, year int, extra bool, conf string, err error) {
	switch itemKind {
	case "movie":
		var y sql.NullInt64
		err = s.DB.QueryRowContext(ctx, `SELECT title, year FROM movies WHERE id = ?`, itemID).Scan(&title, &y)
		if y.Valid {
			year = int(y.Int64)
		}
		_ = s.DB.QueryRowContext(ctx, `
			SELECT parse_confidence FROM media_files WHERE movie_id = ? AND extra_kind = '' LIMIT 1
		`, itemID).Scan(&conf)
	case "series":
		var y sql.NullInt64
		err = s.DB.QueryRowContext(ctx, `SELECT title, year FROM series WHERE id = ?`, itemID).Scan(&title, &y)
		if y.Valid {
			year = int(y.Int64)
		}
		conf = "medium"
	default:
		err = errors.New("unsupported item kind")
	}
	return title, year, extra, conf, err
}

func (s *Service) locked(ctx context.Context, itemKind, itemID, field string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `
		SELECT 1 FROM metadata_locks WHERE item_kind = ? AND item_id = ? AND field = ?
	`, itemKind, itemID, field).Scan(&n)
	return n == 1
}

func (s *Service) searchCached(ctx context.Context, kind, query string, year int) ([]SearchResult, error) {
	key := "search:" + kind + ":" + query + ":" + strconv.Itoa(year)
	if body, ok := s.cacheGet(ctx, key); ok {
		var cands []SearchResult
		if json.Unmarshal([]byte(body), &cands) == nil {
			return cands, nil
		}
	}
	cands, err := s.TMDB.Search(ctx, kind, query, year)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(cands)
	s.cachePut(ctx, key, string(raw), 24*time.Hour)
	return cands, nil
}

func (s *Service) detailsCached(ctx context.Context, kind string, id int) (SearchResult, error) {
	key := "details:" + kind + ":" + strconv.Itoa(id)
	if body, ok := s.cacheGet(ctx, key); ok {
		var r SearchResult
		if json.Unmarshal([]byte(body), &r) == nil {
			return r, nil
		}
	}
	r, err := s.TMDB.Details(ctx, kind, id)
	if err != nil {
		return SearchResult{}, err
	}
	raw, _ := json.Marshal(r)
	s.cachePut(ctx, key, string(raw), 7*24*time.Hour)
	return r, nil
}

func (s *Service) cacheGet(ctx context.Context, key string) (string, bool) {
	var body, exp string
	err := s.DB.QueryRowContext(ctx, `SELECT body, expires_at FROM tmdb_cache WHERE cache_key = ?`, key).Scan(&body, &exp)
	if err != nil {
		return "", false
	}
	if t, e := time.Parse(time.RFC3339, exp); e == nil && time.Now().UTC().After(t) {
		return "", false
	}
	return body, true
}

func (s *Service) cachePut(ctx context.Context, key, body string, ttl time.Duration) {
	now := time.Now().UTC()
	_, _ = s.DB.ExecContext(ctx, `
		INSERT INTO tmdb_cache(cache_key, body, fetched_at, expires_at) VALUES (?, ?, ?, ?)
		ON CONFLICT(cache_key) DO UPDATE SET body = excluded.body, fetched_at = excluded.fetched_at, expires_at = excluded.expires_at
	`, key, body, now.Format(time.RFC3339), now.Add(ttl).Format(time.RFC3339))
}

func sortTitle(s string) string {
	t := s
	for _, p := range []string{"The ", "A ", "An ", "the ", "a ", "an "} {
		if len(t) > len(p) && t[:len(p)] == p {
			return t[len(p):] + ", " + t[:len(p)-1]
		}
	}
	return t
}

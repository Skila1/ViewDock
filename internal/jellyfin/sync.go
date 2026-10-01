package jellyfin

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/library"
)

// StartSync resyncs a source in the background. It reports false when a
// sync of that source is already running.
func (s *Service) StartSync(id string) bool {
	if !s.acquire(id) {
		return false
	}
	go s.run(context.Background(), id)
	return true
}

// Sync imports the selected Jellyfin libraries of one source. Failures are
// recorded on the source and never touch local libraries.
func (s *Service) Sync(ctx context.Context, id string) {
	if s.acquire(id) {
		s.run(ctx, id)
	}
}

func (s *Service) acquire(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.syncing[id] {
		return false
	}
	s.syncing[id] = true
	return true
}

func (s *Service) run(ctx context.Context, id string) {
	defer func() {
		s.mu.Lock()
		delete(s.syncing, id)
		s.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(ctx, 2*time.Hour)
	defer cancel()

	src, err := s.get(ctx, id)
	if err != nil || !src.Enabled {
		return
	}
	s.setStatus(ctx, id, "syncing", "", 0, false)
	s.pruneEvents(ctx)
	count, err := s.syncSource(ctx, src)
	if err != nil {
		msg := err.Error()
		if errors.Is(err, errUnauthorized) {
			msg = "authentication failed: check the credentials"
			if src.AuthMode == AuthAPIKey {
				msg = "Jellyfin rejected the API key or the selected user no longer exists"
			}
		}
		s.setStatus(ctx, id, "error", msg, 0, false)
		s.event(ctx, id, "sync", false, msg)
		if s.Log != nil {
			s.Log.Warn("media source sync", "category", "media_sources", "id", id, "err", msg)
		}
		return
	}
	s.setStatus(ctx, id, "ok", "", count, true)
	s.event(ctx, id, "sync", true, fmt.Sprintf("imported %d titles", count))
	if s.Log != nil {
		s.Log.Info("media source synced", "category", "media_sources", "id", id, "items", count)
	}
}

type fetched struct {
	movies, series, episodes []item
}

func (s *Service) fetch(ctx context.Context, src Source) (fetched, error) {
	var out fetched
	err := s.withClient(ctx, src, func(c *client, userID string) error {
		out = fetched{}
		views, err := c.views(ctx, userID)
		if err != nil {
			return err
		}
		want := map[string]bool{}
		for _, v := range src.Views {
			want[v] = true
		}
		seen := map[string]bool{}
		for _, v := range views {
			if len(want) > 0 && !want[v.ID] {
				continue
			}
			items, err := c.items(ctx, userID, v.ID, "Movie,Series,Episode")
			if err != nil {
				return err
			}
			for _, it := range items {
				if it.ID == "" || seen[it.ID] {
					continue
				}
				seen[it.ID] = true
				it.view = v.Name
				switch it.Type {
				case "Movie":
					out.movies = append(out.movies, it)
				case "Series":
					out.series = append(out.series, it)
				case "Episode":
					out.episodes = append(out.episodes, it)
				}
			}
		}
		return nil
	})
	return out, err
}

type mapped struct {
	kind, itemID, imageTag string
}

type artJob struct {
	kind, itemKind, itemID, remoteID, tag string
	// imageType is the Jellyfin image to fetch; "" means Primary.
	imageType string
}

func (s *Service) syncSource(ctx context.Context, src Source) (int, error) {
	data, err := s.fetch(ctx, src)
	if err != nil {
		return 0, err
	}
	existing := map[string]mapped{}
	rows, err := s.DB.QueryContext(ctx, `SELECT remote_id, item_kind, item_id, image_tag FROM remote_items WHERE source_id = ?`, src.ID)
	if err != nil {
		return 0, err
	}
	for rows.Next() {
		var rid string
		var m mapped
		if err := rows.Scan(&rid, &m.kind, &m.itemID, &m.imageTag); err != nil {
			rows.Close()
			return 0, err
		}
		existing[rid] = m
	}
	rows.Close()

	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339)
	keep := map[string]bool{}
	var art []artJob
	upsert := func(kind string, it item, write func(id string, isNew bool) error) (string, error) {
		m, ok := existing[it.ID]
		id := m.itemID
		if !ok || m.kind != kind {
			id = uuid.NewString()
		}
		if err := write(id, !ok || m.kind != kind); err != nil {
			return "", err
		}
		tag := it.ImageTags["Primary"]
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO remote_items(source_id, remote_id, item_kind, item_id, image_tag, duration_ms) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(source_id, remote_id) DO UPDATE SET item_kind = excluded.item_kind, item_id = excluded.item_id, duration_ms = excluded.duration_ms
		`, src.ID, it.ID, kind, id, m.imageTag, it.durationMS()); err != nil {
			return "", err
		}
		keep[it.ID] = true
		artKind := "poster"
		if kind == "episode" {
			artKind = "thumb"
		}
		if src.Policy.Images && tag != "" && (tag != m.imageTag || !s.hasArtwork(ctx, kind, id, artKind)) {
			art = append(art, artJob{kind: artKind, itemKind: kind, itemID: id, remoteID: it.ID, tag: tag})
		}
		// Wide artwork for the home page's Continue Watching and library
		// tiles. Fetched once; Jellyfin rarely replaces a backdrop.
		if src.Policy.Images && kind != "episode" && len(it.BackdropImageTags) > 0 && !s.hasArtwork(ctx, kind, id, "backdrop") {
			art = append(art, artJob{kind: "backdrop", imageType: "Backdrop", itemKind: kind, itemID: id, remoteID: it.ID, tag: it.BackdropImageTags[0]})
		}
		return id, nil
	}

	titleRow := func(table, kind string) func(it item) func(id string, isNew bool) error {
		return func(it item) func(id string, isNew bool) error {
			return func(id string, isNew bool) error {
				tmdb := providerTMDB(it)
				rating, age := ratingOf(it.OfficialRating)
				sort := library.NormalTitle(it.Name)
				genres := library.EncodeGenres(it.Genres)
				_, err := tx.ExecContext(ctx, `
					INSERT INTO `+table+`(id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review,
						hint_mismatch, tmdb_id, content_rating, rating_age, rating_source, genres_json, collection, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, 'jellyfin', 0, 0, 0, ?, ?, ?, 'jellyfin', ?, ?, ?, ?)
					ON CONFLICT(id) DO UPDATE SET title = excluded.title, year = excluded.year, sort_title = excluded.sort_title,
						overview = excluded.overview, tmdb_id = excluded.tmdb_id, content_rating = excluded.content_rating,
						rating_age = excluded.rating_age, genres_json = excluded.genres_json, collection = excluded.collection,
						updated_at = excluded.updated_at
				`, id, src.LibraryID, it.Name, it.ProductionYear, sort, it.Overview, tmdb, rating, age, genres, it.view, now, now)
				if err != nil {
					return err
				}
				year := 0
				if it.ProductionYear != nil {
					year = *it.ProductionYear
				}
				return library.UpsertFTS(ctx, tx, kind, id, it.Name, year, strings.Join(it.Genres, " "))
			}
		}
	}
	movieRow, seriesRow := titleRow("movies", "movie"), titleRow("series", "series")

	for _, it := range data.movies {
		if _, err := upsert("movie", it, movieRow(it)); err != nil {
			return 0, err
		}
	}
	seriesIDs := map[string]string{}
	for _, it := range data.series {
		id, err := upsert("series", it, seriesRow(it))
		if err != nil {
			return 0, err
		}
		seriesIDs[it.ID] = id
	}
	seasons := map[string]string{}
	taken := map[string]bool{}
	for _, it := range data.episodes {
		seriesID := seriesIDs[it.SeriesID]
		if seriesID == "" || it.IndexNumber == nil {
			continue
		}
		season := 0
		if it.ParentIndexNumber != nil {
			season = *it.ParentIndexNumber
		}
		slot := fmt.Sprintf("%s:%d:%d", seriesID, season, *it.IndexNumber)
		if taken[slot] {
			continue
		}
		taken[slot] = true
		seasonID, err := ensureSeason(ctx, tx, seasons, seriesID, season)
		if err != nil {
			return 0, err
		}
		if _, err := upsert("episode", it, func(id string, _ bool) error {
			// Renumbered episodes can collide with a stale row in the slot.
			if _, err := tx.ExecContext(ctx, `DELETE FROM episodes WHERE id <> ? AND series_id = ? AND season = ? AND number = ?`,
				id, seriesID, season, *it.IndexNumber); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `
				INSERT INTO episodes(id, series_id, season_id, season, number, title, overview, intro_source)
				VALUES (?, ?, ?, ?, ?, ?, ?, '')
				ON CONFLICT(id) DO UPDATE SET series_id = excluded.series_id, season_id = excluded.season_id,
					season = excluded.season, number = excluded.number, title = excluded.title, overview = excluded.overview
			`, id, seriesID, seasonID, season, *it.IndexNumber, it.Name, it.Overview)
			return err
		}); err != nil {
			return 0, err
		}
	}

	for rid, m := range existing {
		if keep[rid] {
			continue
		}
		if err := removeItem(ctx, tx, m.kind, m.itemID); err != nil {
			return 0, err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM remote_items WHERE source_id = ? AND remote_id = ?`, src.ID, rid); err != nil {
			return 0, err
		}
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM seasons WHERE series_id IN (SELECT id FROM series WHERE library_id = ?)
		AND id NOT IN (SELECT season_id FROM episodes)
	`, src.LibraryID); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}

	for _, job := range art {
		if ctx.Err() != nil {
			break
		}
		if err := s.fetchArtwork(ctx, src, job); err != nil && s.Log != nil {
			s.Log.Debug("media source artwork", "category", "media_sources", "id", src.ID, "err", err.Error())
		}
	}
	return len(data.movies) + len(data.series), nil
}

func ensureSeason(ctx context.Context, tx *sql.Tx, cache map[string]string, seriesID string, number int) (string, error) {
	key := seriesID + ":" + strconv.Itoa(number)
	if id := cache[key]; id != "" {
		return id, nil
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM seasons WHERE series_id = ? AND number = ?`, seriesID, number).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = uuid.NewString()
		_, err = tx.ExecContext(ctx, `INSERT INTO seasons(id, series_id, number, title) VALUES (?, ?, ?, '')`, id, seriesID, number)
	}
	if err != nil {
		return "", err
	}
	cache[key] = id
	return id, nil
}

func removeItem(ctx context.Context, tx *sql.Tx, kind, id string) error {
	table := map[string]string{"movie": "movies", "series": "series", "episode": "episodes"}[kind]
	if table == "" {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE id = ?`, id); err != nil {
		return err
	}
	if err := library.DeleteFTS(ctx, tx, kind, id); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM artwork WHERE item_kind = ? AND item_id = ?`, kind, id)
	return err
}

func providerTMDB(it item) any {
	for k, v := range it.ProviderIDs {
		if strings.EqualFold(k, "tmdb") {
			if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
				return n
			}
		}
	}
	return nil
}

// ratingOf maps a Jellyfin official rating ("PG-13", "TV-MA", "AU-M") to
// the catalogue rating and minimum age. Unknown ratings stay unrated.
func ratingOf(official string) (string, any) {
	cert := strings.TrimSpace(official)
	if cert == "" {
		return "", nil
	}
	country := "US"
	if i := strings.Index(cert, "-"); i == 2 && !strings.HasPrefix(strings.ToUpper(cert), "TV-") {
		country, cert = strings.ToUpper(cert[:2]), cert[3:]
	}
	for _, c := range []string{country, "US", "AU", "GB"} {
		if age, ok := library.CertificationAge(c, cert); ok {
			return cert, age
		}
	}
	return cert, nil
}

func (s *Service) hasArtwork(ctx context.Context, itemKind, itemID, kind string) bool {
	var n int
	_ = s.DB.QueryRowContext(ctx, `SELECT 1 FROM artwork WHERE item_kind = ? AND item_id = ? AND kind = ?`, itemKind, itemID, kind).Scan(&n)
	return n == 1
}

// fetchArtwork stores a Jellyfin image in the artwork cache the catalogue
// already serves, so posters load without contacting Jellyfin.
func (s *Service) fetchArtwork(ctx context.Context, src Source, job artJob) error {
	if s.CacheDir == "" {
		return nil
	}
	var raw []byte
	var ctype string
	err := s.withClient(ctx, src, func(c *client, _ string) error {
		var err error
		raw, ctype, err = c.image(ctx, job.remoteID, job.imageType, job.tag)
		return err
	})
	if err != nil {
		return err
	}
	ext := ".jpg"
	switch {
	case strings.Contains(ctype, "png"):
		ext = ".png"
	case strings.Contains(ctype, "webp"):
		ext = ".webp"
	case strings.Contains(ctype, "jpeg"), strings.Contains(ctype, "jpg"):
	default:
		return fmt.Errorf("unsupported image type %q", ctype)
	}
	rel := filepath.Join("artwork", job.kind, job.itemKind, job.itemID+ext)
	dest := filepath.Join(s.CacheDir, rel)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if _, err := s.DB.ExecContext(ctx, `
		INSERT INTO artwork(id, item_kind, item_id, kind, path, source, locked) VALUES (?, ?, ?, ?, ?, 'jellyfin', 0)
		ON CONFLICT(item_kind, item_id, kind) DO UPDATE SET path = excluded.path, source = excluded.source
	`, uuid.NewString(), job.itemKind, job.itemID, job.kind, filepath.ToSlash(rel)); err != nil {
		return err
	}
	if job.imageType != "" {
		return nil // image_tag tracks the Primary image only
	}
	_, err = s.DB.ExecContext(ctx, `UPDATE remote_items SET image_tag = ? WHERE source_id = ? AND remote_id = ?`, job.tag, src.ID, job.remoteID)
	return err
}

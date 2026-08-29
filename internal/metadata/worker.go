package metadata

import (
	"context"
	"time"
)

const workerInterval = 20 * time.Second

func (s *Service) Start(ctx context.Context) {
	go s.loop(ctx)
}

func (s *Service) loop(ctx context.Context) {
	_ = s.RunOnce(ctx)
	t := time.NewTicker(workerInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = s.RunOnce(context.Background())
		}
	}
}

func (s *Service) RunOnce(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.enqueueUnmatched(ctx)
	if err := s.DrainQueue(ctx); err != nil {
		return err
	}
	return s.backfillArtwork(ctx)
}

func (s *Service) enqueueUnmatched(ctx context.Context) {
	type row struct {
		kind, id, title string
		year            int
	}
	var list []row
	for _, q := range []struct {
		kind, sql string
	}{
		{"movie", `SELECT id, title, COALESCE(year, 0) FROM movies WHERE unmatched = 1`},
		{"series", `SELECT id, title, COALESCE(year, 0) FROM series WHERE unmatched = 1`},
	} {
		rows, err := s.DB.QueryContext(ctx, q.sql)
		if err != nil {
			continue
		}
		for rows.Next() {
			var r row
			r.kind = q.kind
			if rows.Scan(&r.id, &r.title, &r.year) == nil {
				list = append(list, r)
			}
		}
		_ = rows.Close()
	}
	for _, r := range list {
		_ = s.Enqueue(ctx, r.kind, r.id, r.title, r.year)
	}
}

func (s *Service) backfillArtwork(ctx context.Context) error {
	if !s.HasKey(ctx) || s.Art == nil {
		return nil
	}
	type row struct {
		kind, id string
		tmdbID   int
	}
	var list []row
	for _, q := range []struct {
		kind, sql string
	}{
		{"movie", `
			SELECT m.id, m.tmdb_id FROM movies m
			WHERE m.tmdb_id IS NOT NULL AND m.tmdb_id > 0
			AND NOT EXISTS (
				SELECT 1 FROM artwork a WHERE a.item_kind = 'movie' AND a.item_id = m.id AND a.kind = 'poster'
			)
			LIMIT 20
		`},
		{"series", `
			SELECT se.id, se.tmdb_id FROM series se
			WHERE se.tmdb_id IS NOT NULL AND se.tmdb_id > 0
			AND NOT EXISTS (
				SELECT 1 FROM artwork a WHERE a.item_kind = 'series' AND a.item_id = se.id AND a.kind = 'poster'
			)
			LIMIT 20
		`},
	} {
		rows, err := s.DB.QueryContext(ctx, q.sql)
		if err != nil {
			continue
		}
		for rows.Next() {
			var r row
			r.kind = q.kind
			if rows.Scan(&r.id, &r.tmdbID) == nil {
				list = append(list, r)
			}
		}
		_ = rows.Close()
	}
	for _, r := range list {
		det, err := s.detailsCached(ctx, r.kind, r.tmdbID)
		if err != nil || det.Poster == "" {
			continue
		}
		_ = s.Art.FetchTMDB(ctx, "poster", r.kind, r.id, det.Poster)
		if det.Backdrop != "" {
			_ = s.Art.FetchTMDB(ctx, "backdrop", r.kind, r.id, det.Backdrop)
		}
	}
	return nil
}

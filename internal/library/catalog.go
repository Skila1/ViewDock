package library

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strconv"
)

func (s *Service) Contains(libraryID, absPath string) error {
	lib, err := s.Get(context.Background(), libraryID)
	if err != nil {
		return err
	}
	return ContainsPath(lib.RootPath, absPath)
}

func (s *Service) LocateFile(ctx context.Context, mediaFileID string) (*LocatedFile, error) {
	var f LocatedFile
	var movieID, extraKind sql.NullString
	err := s.DB.QueryRowContext(ctx, `
		SELECT id, library_id, abs_path, rel_path, kind, COALESCE(movie_id, ''), size_bytes,
		       duration_ms, container, video_codec, audio_codec, width, height, availability
		FROM media_files WHERE id = ?
	`, mediaFileID).Scan(
		&f.ID, &f.LibraryID, &f.AbsPath, &f.RelPath, &f.ItemKind, &movieID,
		&f.Size, &f.DurationMS, &f.Container, &f.VideoCodec, &f.AudioCodec,
		&f.Width, &f.Height, &f.Availability,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if movieID.Valid {
		f.MovieID = movieID.String
	}
	if f.ItemKind == "movie" {
		f.ItemID = f.MovieID
	} else if f.ItemKind == "episode" {
		var epID string
		_ = s.DB.QueryRowContext(ctx, `
			SELECT episode_id FROM media_file_episodes WHERE media_file_id = ? LIMIT 1
		`, mediaFileID).Scan(&epID)
		f.ItemID = epID
	}
	_ = extraKind
	f.AbsPath = filepath.Clean(f.AbsPath)
	return &f, nil
}

func (s *Service) LocateItem(ctx context.Context, itemKind, itemID string) (*LocatedFile, error) {
	switch itemKind {
	case "movie":
		return s.locateMovieFile(ctx, itemID)
	case "episode":
		return s.locateEpisodeFile(ctx, itemID)
	default:
		return nil, ErrNotFound
	}
}

func (s *Service) locateMovieFile(ctx context.Context, movieID string) (*LocatedFile, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `
		SELECT id FROM media_files
		WHERE movie_id = ? AND extra_kind = '' AND kind = 'movie'
		ORDER BY CASE availability WHEN 'online' THEN 0 ELSE 1 END, size_bytes DESC
		LIMIT 1
	`, movieID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.LocateFile(ctx, id)
}

func (s *Service) locateEpisodeFile(ctx context.Context, episodeID string) (*LocatedFile, error) {
	var id string
	err := s.DB.QueryRowContext(ctx, `
		SELECT mf.id FROM media_files mf
		JOIN media_file_episodes mfe ON mfe.media_file_id = mf.id
		WHERE mfe.episode_id = ?
		ORDER BY CASE mf.availability WHEN 'online' THEN 0 ELSE 1 END, mf.size_bytes DESC
		LIMIT 1
	`, episodeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return s.LocateFile(ctx, id)
}

func (s *Service) Open(ctx context.Context, mediaFileID string) (*os.File, error) {
	loc, err := s.LocateFile(ctx, mediaFileID)
	if err != nil {
		return nil, err
	}
	if err := s.Contains(loc.LibraryID, loc.AbsPath); err != nil {
		return nil, err
	}
	return os.Open(loc.AbsPath)
}

func (s *Service) ItemTitle(ctx context.Context, itemKind, itemID string) (string, error) {
	var title string
	var err error
	switch itemKind {
	case "movie":
		err = s.DB.QueryRowContext(ctx, `SELECT title FROM movies WHERE id = ?`, itemID).Scan(&title)
	case "series":
		err = s.DB.QueryRowContext(ctx, `SELECT title FROM series WHERE id = ?`, itemID).Scan(&title)
	case "episode":
		err = s.DB.QueryRowContext(ctx, `
			SELECT COALESCE(NULLIF(e.title, ''), s.title || ' S' || printf('%02d', e.season) || 'E' || printf('%02d', e.number))
			FROM episodes e JOIN series s ON s.id = e.series_id WHERE e.id = ?
		`, itemID).Scan(&title)
	default:
		return "", ErrNotFound
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return title, err
}

func (s *Service) Exists(ctx context.Context, itemKind, itemID string) bool {
	var n int
	var err error
	switch itemKind {
	case "movie":
		err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM movies WHERE id = ?`, itemID).Scan(&n)
	case "series":
		err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM series WHERE id = ?`, itemID).Scan(&n)
	case "episode":
		err = s.DB.QueryRowContext(ctx, `SELECT 1 FROM episodes WHERE id = ?`, itemID).Scan(&n)
	default:
		return false
	}
	return err == nil && n == 1
}

func (s *Service) LibraryIDForItem(ctx context.Context, itemKind, itemID string) (string, error) {
	var id string
	var err error
	switch itemKind {
	case "movie":
		err = s.DB.QueryRowContext(ctx, `SELECT library_id FROM movies WHERE id = ?`, itemID).Scan(&id)
	case "series":
		err = s.DB.QueryRowContext(ctx, `SELECT library_id FROM series WHERE id = ?`, itemID).Scan(&id)
	case "episode":
		err = s.DB.QueryRowContext(ctx, `
			SELECT s.library_id FROM episodes e JOIN series s ON s.id = e.series_id WHERE e.id = ?
		`, itemID).Scan(&id)
	default:
		return "", ErrNotFound
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return id, err
}

// DeleteUserOwned implements CollectionAdmin. Collections have no owner column
// in 0010, so this is a no-op until a later schema can record ownership.
func (s *Service) DeleteUserOwned(ctx context.Context, userID string) error {
	return nil
}

// UpsertFTS inserts or replaces a media_fts row.
func (s *Service) UpsertFTS(ctx context.Context, itemKind, itemID, title string, year int, extra string) error {
	return UpsertFTS(ctx, s.DB, itemKind, itemID, title, year, extra)
}

type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func UpsertFTS(ctx context.Context, db execer, itemKind, itemID, title string, year int, extra string) error {
	_, _ = db.ExecContext(ctx, `DELETE FROM media_fts WHERE item_kind = ? AND item_id = ?`, itemKind, itemID)
	y := ""
	if year > 0 {
		y = itoa(year)
	}
	_, err := db.ExecContext(ctx, `
		INSERT INTO media_fts(item_kind, item_id, title, year, extra) VALUES (?, ?, ?, ?, ?)
	`, itemKind, itemID, title, y, extra)
	return err
}

func DeleteFTS(ctx context.Context, db execer, itemKind, itemID string) error {
	_, err := db.ExecContext(ctx, `DELETE FROM media_fts WHERE item_kind = ? AND item_id = ?`, itemKind, itemID)
	return err
}

func itoa(n int) string {
	if n == 0 {
		return ""
	}
	return strconv.Itoa(n)
}

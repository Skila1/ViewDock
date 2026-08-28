package library

import (
	"context"
	"database/sql"
	"errors"
)

type Movie struct {
	ID             string  `json:"id"`
	LibraryID      string  `json:"library_id"`
	Title          string  `json:"title"`
	Year           *int    `json:"year"`
	SortTitle      string  `json:"sort_title,omitempty"`
	Overview       string  `json:"overview,omitempty"`
	MetadataSource string  `json:"metadata_source"`
	Unmatched      bool    `json:"unmatched"`
	NeedsReview    bool    `json:"needs_review,omitempty"`
	TMDBID         *int    `json:"tmdb_id,omitempty"`
	PosterURL      *string `json:"poster_url"`
	Files          []File  `json:"files,omitempty"`
	Extras         []File  `json:"extras,omitempty"`
}

type Series struct {
	ID             string   `json:"id"`
	LibraryID      string   `json:"library_id"`
	Title          string   `json:"title"`
	Year           *int     `json:"year"`
	SortTitle      string   `json:"sort_title,omitempty"`
	Overview       string   `json:"overview,omitempty"`
	MetadataSource string   `json:"metadata_source"`
	Unmatched      bool     `json:"unmatched"`
	NeedsReview    bool     `json:"needs_review,omitempty"`
	TMDBID         *int     `json:"tmdb_id,omitempty"`
	PosterURL      *string  `json:"poster_url"`
	Seasons        []Season `json:"seasons,omitempty"`
}

type Season struct {
	ID       string    `json:"id"`
	Number   int       `json:"number"`
	Title    string    `json:"title,omitempty"`
	Episodes []Episode `json:"episodes,omitempty"`
}

type Episode struct {
	ID        string  `json:"id"`
	SeriesID  string  `json:"series_id"`
	SeasonID  string  `json:"season_id,omitempty"`
	Season    int     `json:"season"`
	Number    int     `json:"number"`
	Title     string  `json:"title"`
	Overview  string  `json:"overview,omitempty"`
	PosterURL *string `json:"poster_url,omitempty"`
}

type File struct {
	ID           string `json:"id"`
	RelPath      string `json:"rel_path"`
	Availability string `json:"availability"`
	DurationMS   int64  `json:"duration_ms"`
	ExtraKind    string `json:"extra_kind,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}

func (s *Service) ListMovies(ctx context.Context, grantedIDs []string) ([]Movie, error) {
	ids := grantedFilter(ctx, grantedIDs)
	if ids != nil && len(ids) == 0 {
		return []Movie{}, nil
	}
	q := `
		SELECT id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review, tmdb_id
		FROM movies`
	args := []any{}
	if ids != nil {
		q += ` WHERE library_id IN (` + inClause(len(ids)) + `)`
		args = asAny(ids)
	}
	q += ` ORDER BY sort_title, title`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Movie{}
	for rows.Next() {
		m, err := scanMovie(rows)
		if err != nil {
			return nil, err
		}
		m.PosterURL = s.artworkURL(ctx, "poster", "movie", m.ID)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Service) GetMovie(ctx context.Context, id string) (Movie, error) {
	m, err := scanMovie(s.DB.QueryRowContext(ctx, `
		SELECT id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review, tmdb_id
		FROM movies WHERE id = ?
	`, id))
	if err != nil {
		return Movie{}, err
	}
	m.PosterURL = s.artworkURL(ctx, "poster", "movie", m.ID)
	files, extras, err := s.movieFiles(ctx, id)
	if err != nil {
		return Movie{}, err
	}
	m.Files = files
	m.Extras = extras
	return m, nil
}

func (s *Service) ListSeries(ctx context.Context, grantedIDs []string) ([]Series, error) {
	ids := grantedFilter(ctx, grantedIDs)
	if ids != nil && len(ids) == 0 {
		return []Series{}, nil
	}
	q := `
		SELECT id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review, tmdb_id
		FROM series`
	args := []any{}
	if ids != nil {
		q += ` WHERE library_id IN (` + inClause(len(ids)) + `)`
		args = asAny(ids)
	}
	q += ` ORDER BY sort_title, title`
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Series{}
	for rows.Next() {
		ser, err := scanSeries(rows)
		if err != nil {
			return nil, err
		}
		ser.PosterURL = s.artworkURL(ctx, "poster", "series", ser.ID)
		out = append(out, ser)
	}
	return out, rows.Err()
}

func (s *Service) GetSeries(ctx context.Context, id string) (Series, error) {
	ser, err := scanSeries(s.DB.QueryRowContext(ctx, `
		SELECT id, library_id, title, year, sort_title, overview, metadata_source, unmatched, needs_review, tmdb_id
		FROM series WHERE id = ?
	`, id))
	if err != nil {
		return Series{}, err
	}
	ser.PosterURL = s.artworkURL(ctx, "poster", "series", ser.ID)
	seasons, err := s.seriesSeasons(ctx, id)
	if err != nil {
		return Series{}, err
	}
	ser.Seasons = seasons
	return ser, nil
}

func (s *Service) GetEpisode(ctx context.Context, id string) (Episode, error) {
	ep, err := scanEpisode(s.DB.QueryRowContext(ctx, `
		SELECT id, series_id, season_id, season, number, title, overview FROM episodes WHERE id = ?
	`, id))
	if err != nil {
		return Episode{}, err
	}
	ep.PosterURL = s.artworkURL(ctx, "thumb", "episode", ep.ID)
	return ep, nil
}

func (s *Service) NextEpisode(ctx context.Context, seriesID, userID string) (Episode, error) {
	if userID == "" {
		userID = UserIDFrom(ctx)
	}
	if userID != "" {
		if ep, err := s.nextFromProgress(ctx, seriesID, userID); err == nil {
			return ep, nil
		}
	}
	return scanEpisode(s.DB.QueryRowContext(ctx, `
		SELECT id, series_id, season_id, season, number, title, overview
		FROM episodes WHERE series_id = ?
		ORDER BY season, number LIMIT 1
	`, seriesID))
}

func (s *Service) nextFromProgress(ctx context.Context, seriesID, userID string) (Episode, error) {
	var season, number int
	err := s.DB.QueryRowContext(ctx, `
		SELECT e.season, e.number
		FROM playback_progress p
		JOIN episodes e ON e.id = p.item_id
		WHERE p.user_id = ? AND p.item_kind = 'episode' AND e.series_id = ?
		ORDER BY p.updated_at DESC LIMIT 1
	`, userID, seriesID).Scan(&season, &number)
	if err != nil {
		return Episode{}, err
	}
	var completed int
	_ = s.DB.QueryRowContext(ctx, `
		SELECT p.completed FROM playback_progress p
		JOIN episodes e ON e.id = p.item_id
		WHERE p.user_id = ? AND p.item_kind = 'episode' AND e.series_id = ? AND e.season = ? AND e.number = ?
	`, userID, seriesID, season, number).Scan(&completed)
	if completed == 0 {
		return scanEpisode(s.DB.QueryRowContext(ctx, `
			SELECT id, series_id, season_id, season, number, title, overview
			FROM episodes WHERE series_id = ? AND season = ? AND number = ?
		`, seriesID, season, number))
	}
	ep, err := scanEpisode(s.DB.QueryRowContext(ctx, `
		SELECT id, series_id, season_id, season, number, title, overview
		FROM episodes WHERE series_id = ? AND (season > ? OR (season = ? AND number > ?))
		ORDER BY season, number LIMIT 1
	`, seriesID, season, season, number))
	if err == nil {
		return ep, nil
	}
	return scanEpisode(s.DB.QueryRowContext(ctx, `
		SELECT id, series_id, season_id, season, number, title, overview
		FROM episodes WHERE series_id = ? ORDER BY season, number LIMIT 1
	`, seriesID))
}

func (s *Service) movieFiles(ctx context.Context, movieID string) (files, extras []File, err error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, rel_path, availability, duration_ms, extra_kind, width, height
		FROM media_files WHERE movie_id = ? ORDER BY extra_kind, rel_path
	`, movieID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	files, extras = []File{}, []File{}
	for rows.Next() {
		var f File
		if err := rows.Scan(&f.ID, &f.RelPath, &f.Availability, &f.DurationMS, &f.ExtraKind, &f.Width, &f.Height); err != nil {
			return nil, nil, err
		}
		if f.ExtraKind != "" {
			extras = append(extras, f)
		} else {
			files = append(files, f)
		}
	}
	return files, extras, rows.Err()
}

func (s *Service) seriesSeasons(ctx context.Context, seriesID string) ([]Season, error) {
	rows, err := s.DB.QueryContext(ctx, `
		SELECT id, number, title FROM seasons WHERE series_id = ? ORDER BY number
	`, seriesID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var seasons []Season
	for rows.Next() {
		var se Season
		if err := rows.Scan(&se.ID, &se.Number, &se.Title); err != nil {
			return nil, err
		}
		se.Episodes = []Episode{}
		seasons = append(seasons, se)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	epRows, err := s.DB.QueryContext(ctx, `
		SELECT id, series_id, season_id, season, number, title, overview
		FROM episodes WHERE series_id = ? ORDER BY season, number
	`, seriesID)
	if err != nil {
		return nil, err
	}
	defer epRows.Close()
	bySeason := map[int][]Episode{}
	for epRows.Next() {
		ep, err := scanEpisode(epRows)
		if err != nil {
			return nil, err
		}
		bySeason[ep.Season] = append(bySeason[ep.Season], ep)
	}
	if seasons == nil {
		seasons = []Season{}
	}
	for i := range seasons {
		if eps := bySeason[seasons[i].Number]; eps != nil {
			seasons[i].Episodes = eps
		}
	}
	return seasons, epRows.Err()
}

func scanMovie(row rowScanner) (Movie, error) {
	var m Movie
	var year, tmdb sql.NullInt64
	var unmatched, review int
	err := row.Scan(&m.ID, &m.LibraryID, &m.Title, &year, &m.SortTitle, &m.Overview, &m.MetadataSource, &unmatched, &review, &tmdb)
	if errors.Is(err, sql.ErrNoRows) {
		return Movie{}, ErrNotFound
	}
	m.Year = nullInt(year)
	m.TMDBID = nullInt(tmdb)
	m.Unmatched = unmatched == 1
	m.NeedsReview = review == 1
	return m, err
}

func scanSeries(row rowScanner) (Series, error) {
	var ser Series
	var year, tmdb sql.NullInt64
	var unmatched, review int
	err := row.Scan(&ser.ID, &ser.LibraryID, &ser.Title, &year, &ser.SortTitle, &ser.Overview, &ser.MetadataSource, &unmatched, &review, &tmdb)
	if errors.Is(err, sql.ErrNoRows) {
		return Series{}, ErrNotFound
	}
	ser.Year = nullInt(year)
	ser.TMDBID = nullInt(tmdb)
	ser.Unmatched = unmatched == 1
	ser.NeedsReview = review == 1
	return ser, err
}

func scanEpisode(row rowScanner) (Episode, error) {
	var ep Episode
	err := row.Scan(&ep.ID, &ep.SeriesID, &ep.SeasonID, &ep.Season, &ep.Number, &ep.Title, &ep.Overview)
	if errors.Is(err, sql.ErrNoRows) {
		return Episode{}, ErrNotFound
	}
	return ep, err
}

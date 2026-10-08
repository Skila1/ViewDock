package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/viewdock/viewdock/internal/httpapi"
)

// Card describes a catalogue item for the home page rails: what to call it,
// the line under it ("S1:E5 - Chapter 5" or the year) and the artwork a
// wide card or a poster card can use.
type Card struct {
	Kind         string  `json:"kind"` // movie or episode
	ID           string  `json:"id"`
	Title        string  `json:"title"` // the movie, or the episode's show
	Subtitle     string  `json:"subtitle,omitempty"`
	Year         *int    `json:"year,omitempty"`
	SeriesID     string  `json:"series_id,omitempty"`
	Season       *int    `json:"season,omitempty"`
	Number       *int    `json:"number,omitempty"`
	EpisodeTitle string  `json:"episode_title,omitempty"`
	PosterURL    *string `json:"poster_url,omitempty"`
	BackdropURL  *string `json:"backdrop_url,omitempty"`
	ThumbURL     *string `json:"thumb_url,omitempty"`
}

// EpisodeLabel is how the home page names an episode: "S1:E5 - Chapter 5",
// or just "S1:E5" when the episode has no title of its own.
func EpisodeLabel(season, number int, title string) string {
	label := fmt.Sprintf("S%d:E%d", season, number)
	if title != "" && title != "Episode "+strconv.Itoa(number) {
		label += " - " + title
	}
	return label
}

// ItemCard describes a movie or an episode for a home page card.
func (s *Service) ItemCard(ctx context.Context, itemKind, itemID string) (Card, error) {
	switch itemKind {
	case "movie":
		var c Card
		var year sql.NullInt64
		err := s.DB.QueryRowContext(ctx, `SELECT id, title, year FROM movies WHERE id = ?`, itemID).Scan(&c.ID, &c.Title, &year)
		if errors.Is(err, sql.ErrNoRows) {
			return Card{}, ErrNotFound
		}
		if err != nil {
			return Card{}, err
		}
		c.Kind = "movie"
		c.Year = nullInt(year)
		if c.Year != nil {
			c.Subtitle = strconv.Itoa(*c.Year)
		}
		c.PosterURL = s.artworkURL(ctx, "poster", "movie", c.ID)
		c.BackdropURL = s.artworkURL(ctx, "backdrop", "movie", c.ID)
		return c, nil
	case "episode":
		var c Card
		var year sql.NullInt64
		var season, number int
		err := s.DB.QueryRowContext(ctx, `
			SELECT e.id, e.series_id, s.title, s.year, e.season, e.number, e.title
			FROM episodes e JOIN series s ON s.id = e.series_id WHERE e.id = ?
		`, itemID).Scan(&c.ID, &c.SeriesID, &c.Title, &year, &season, &number, &c.EpisodeTitle)
		if errors.Is(err, sql.ErrNoRows) {
			return Card{}, ErrNotFound
		}
		if err != nil {
			return Card{}, err
		}
		c.Kind = "episode"
		c.Year = nullInt(year)
		c.Season, c.Number = &season, &number
		c.Subtitle = EpisodeLabel(season, number, c.EpisodeTitle)
		c.ThumbURL = s.artworkURL(ctx, "thumb", "episode", c.ID)
		c.PosterURL = s.artworkURL(ctx, "poster", "series", c.SeriesID)
		c.BackdropURL = s.artworkURL(ctx, "backdrop", "series", c.SeriesID)
		return c, nil
	}
	return Card{}, ErrNotFound
}

// nextUpScan bounds how many recently watched shows NextUp looks at.
const nextUpScan = 60

// NextUp lists, for the shows userID watched most recently, the episode to
// watch next: the first unwatched episode after the last finished one.
// Shows with an episode in progress are left to Continue Watching, finished
// shows are left out, and specials are only offered while watching specials.
func (s *Service) NextUp(ctx context.Context, userID string, limit int) ([]Card, error) {
	out := []Card{}
	if userID == "" {
		return out, nil
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.DB.QueryContext(ctx, `
		SELECT e.series_id, MAX(p.updated_at) AS last_seen
		FROM playback_progress p JOIN episodes e ON e.id = p.item_id
		WHERE p.user_id = ? AND p.item_kind = 'episode'
		GROUP BY e.series_id ORDER BY last_seen DESC LIMIT ?
	`, userID, nextUpScan)
	if err != nil {
		return nil, err
	}
	var series []string
	for rows.Next() {
		var id, last string
		if err := rows.Scan(&id, &last); err != nil {
			rows.Close()
			return nil, err
		}
		series = append(series, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	ctx = WithUserID(ctx, userID)
	for _, seriesID := range series {
		if len(out) == limit {
			break
		}
		if s.visible(ctx, "series", seriesID) != nil {
			continue
		}
		epID, ok, err := s.nextUpEpisode(ctx, userID, seriesID)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		card, err := s.ItemCard(ctx, "episode", epID)
		if err != nil {
			continue
		}
		out = append(out, card)
	}
	return out, nil
}

func (s *Service) nextUpEpisode(ctx context.Context, userID, seriesID string) (string, bool, error) {
	var lastID string
	var season, number int
	var completed int
	var position int64
	err := s.DB.QueryRowContext(ctx, `
		SELECT e.id, e.season, e.number, p.completed, p.position_ms
		FROM playback_progress p JOIN episodes e ON e.id = p.item_id
		WHERE p.user_id = ? AND p.item_kind = 'episode' AND e.series_id = ?
		ORDER BY p.updated_at DESC LIMIT 1
	`, userID, seriesID).Scan(&lastID, &season, &number, &completed, &position)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	if completed == 0 {
		// Well under way: that episode is in Continue Watching instead.
		if position > continueMinMS {
			return "", false, nil
		}
		return lastID, true, nil
	}
	var next string
	err = s.DB.QueryRowContext(ctx, `
		SELECT e.id FROM episodes e
		WHERE e.series_id = ? AND (e.season > ? OR (e.season = ? AND e.number > ?))
		  AND (e.season > 0 OR ? = 0)
		  AND e.id NOT IN (SELECT item_id FROM playback_progress WHERE user_id = ? AND item_kind = 'episode' AND completed = 1)
		  AND EXISTS (SELECT 1 FROM media_file_episodes mfe WHERE mfe.episode_id = e.id
		              UNION ALL SELECT 1 FROM remote_items ri WHERE ri.item_kind = 'episode' AND ri.item_id = e.id)
		ORDER BY e.season, e.number LIMIT 1
	`, seriesID, season, season, number, season, userID).Scan(&next)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return next, true, nil
}

// continueMinMS matches the progress store: positions past five seconds are
// listed under Continue Watching.
const continueMinMS = 5000

func (s *Service) handleNextUp(w http.ResponseWriter, r *http.Request) {
	list, err := s.NextUp(r.Context(), UserIDFrom(r.Context()), 20)
	if err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, list)
}

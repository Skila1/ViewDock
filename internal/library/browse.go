package library

import (
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/viewdock/viewdock/internal/httpapi"
)

// ParseGenres decodes a genres_json column. Empty means not looked up yet.
func ParseGenres(raw string) []string {
	out := []string{}
	if strings.TrimSpace(raw) == "" {
		return out
	}
	var list []string
	if json.Unmarshal([]byte(raw), &list) != nil {
		return out
	}
	seen := map[string]bool{}
	for _, g := range list {
		g = strings.TrimSpace(g)
		if g == "" || seen[strings.ToLower(g)] {
			continue
		}
		seen[strings.ToLower(g)] = true
		out = append(out, g)
	}
	return out
}

// EncodeGenres produces the genres_json value for a looked-up title.
func EncodeGenres(genres []string) string {
	raw, _ := json.Marshal(genres)
	clean, _ := json.Marshal(ParseGenres(string(raw)))
	return string(clean)
}

var animeName = regexp.MustCompile(`(?i)\banime\b`)

// IsAnime reports whether a title is anime: tagged with an Anime genre, or
// stored in a library (or Jellyfin library) whose name says anime.
func IsAnime(genres []string, collection, libraryName string) bool {
	for _, g := range genres {
		if strings.EqualFold(g, "anime") {
			return true
		}
	}
	return animeName.MatchString(collection) || animeName.MatchString(libraryName)
}

// Signals are the per-viewer facts behind the browse filters. Keys are
// "movie:<id>" or "series:<id>" and only cover titles the caller can see.
type Signals struct {
	Views   map[string]int `json:"views"`
	Watched []string       `json:"watched"`
	// Finished lists titles marked or played to the end ("movie:<id>", or a
	// series whose every episode is finished).
	Finished      []string `json:"finished"`
	Liked         []string `json:"liked"`
	Disliked      []string `json:"disliked"`
	NotInterested []string `json:"not_interested"`
	// Watchlist is the profile's My List.
	Watchlist   []string `json:"watchlist"`
	Recommended []string `json:"recommended"`
}

const recommendLimit = 30

type browseTitle struct {
	key, title, added string
	genres            []string
}

func (s *Service) BrowseSignals(ctx context.Context) (Signals, error) {
	granted := GrantedIDsFrom(ctx)
	movies, err := s.ListMovies(ctx, granted)
	if err != nil {
		return Signals{}, err
	}
	series, err := s.ListSeries(ctx, granted)
	if err != nil {
		return Signals{}, err
	}
	visible := make([]browseTitle, 0, len(movies)+len(series))
	for _, m := range movies {
		visible = append(visible, browseTitle{key: "movie:" + m.ID, title: m.Title, added: m.AddedAt, genres: m.Genres})
	}
	for _, se := range series {
		visible = append(visible, browseTitle{key: "series:" + se.ID, title: se.Title, added: se.AddedAt, genres: se.Genres})
	}
	known := make(map[string]bool, len(visible))
	for _, t := range visible {
		known[t.key] = true
	}

	views := map[string]int{}
	if err := s.countInto(ctx, views, known, "movie:", `
		SELECT item_id, COUNT(DISTINCT user_id) FROM playback_progress WHERE item_kind = 'movie' GROUP BY item_id`); err != nil {
		return Signals{}, err
	}
	if err := s.countInto(ctx, views, known, "series:", `
		SELECT e.series_id, COUNT(DISTINCT p.user_id) FROM playback_progress p
		JOIN episodes e ON e.id = p.item_id WHERE p.item_kind = 'episode' GROUP BY e.series_id`); err != nil {
		return Signals{}, err
	}

	watched := map[string]bool{}
	finished := map[string]bool{}
	reactions := map[string]int{}
	if userID := UserIDFrom(ctx); userID != "" {
		if err := s.keysInto(ctx, finished, known, "movie:", `
			SELECT item_id FROM playback_progress WHERE user_id = ? AND item_kind = 'movie' AND completed = 1`, userID); err != nil {
			return Signals{}, err
		}
		if err := s.keysInto(ctx, finished, known, "series:", `
			SELECT e.series_id FROM episodes e
			LEFT JOIN playback_progress p ON p.item_kind = 'episode' AND p.item_id = e.id AND p.user_id = ? AND p.completed = 1
			GROUP BY e.series_id HAVING COUNT(*) = COUNT(p.item_id)`, userID); err != nil {
			return Signals{}, err
		}
		rows, err := s.DB.QueryContext(ctx, `SELECT item_kind, item_id, reaction FROM title_reactions WHERE user_id = ?`, userID)
		if err != nil {
			return Signals{}, err
		}
		for rows.Next() {
			var kind, id string
			var v int
			if err := rows.Scan(&kind, &id, &v); err != nil {
				rows.Close()
				return Signals{}, err
			}
			if known[kind+":"+id] {
				reactions[kind+":"+id] = v
			}
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return Signals{}, err
		}
		if err := s.keysInto(ctx, watched, known, "movie:", `
			SELECT item_id FROM playback_progress WHERE user_id = ? AND item_kind = 'movie'`, userID); err != nil {
			return Signals{}, err
		}
		if err := s.keysInto(ctx, watched, known, "series:", `
			SELECT DISTINCT e.series_id FROM playback_progress p
			JOIN episodes e ON e.id = p.item_id WHERE p.user_id = ? AND p.item_kind = 'episode'`, userID); err != nil {
			return Signals{}, err
		}
	}

	listed := map[string]bool{}
	if userID := UserIDFrom(ctx); userID != "" {
		for _, kind := range []string{"movie", "series"} {
			if err := s.keysInto(ctx, listed, known, kind+":", `SELECT item_id FROM favourites WHERE user_id = ? AND item_kind = '`+kind+`'`, userID); err != nil {
				return Signals{}, err
			}
		}
	}
	out := Signals{Views: views, Watched: sortedKeys(watched), Finished: sortedKeys(finished), Liked: []string{}, Disliked: []string{},
		NotInterested: []string{}, Watchlist: sortedKeys(listed), Recommended: recommend(visible, watched, reactions, views)}
	for k, v := range reactions {
		switch v {
		case ReactionLike:
			out.Liked = append(out.Liked, k)
		case ReactionDislike:
			out.Disliked = append(out.Disliked, k)
		case ReactionNotInterested:
			out.NotInterested = append(out.NotInterested, k)
		}
	}
	sort.Strings(out.Liked)
	sort.Strings(out.Disliked)
	sort.Strings(out.NotInterested)
	return out, nil
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Genre weights: a liked title counts three times a watched one, and a
// disliked title counts against its genres.
const (
	watchedWeight  = 1
	likedWeight    = 3
	dislikedWeight = -3
)

// recommend ranks titles the viewer has not watched or disliked by how their
// genres appear in what they watched, liked and disliked, then by how many
// people watched them, then newest.
func recommend(visible []browseTitle, watched map[string]bool, reactions map[string]int, views map[string]int) []string {
	weight := map[string]int{}
	for _, t := range visible {
		w := 0
		if watched[t.key] {
			w += watchedWeight
		}
		switch reactions[t.key] {
		case ReactionLike:
			w += likedWeight
		case ReactionDislike:
			w += dislikedWeight
		}
		if w == 0 {
			continue
		}
		for _, g := range t.genres {
			weight[strings.ToLower(g)] += w
		}
	}
	type scored struct {
		browseTitle
		score int
	}
	var cands []scored
	for _, t := range visible {
		// Watched, liked and rejected titles are known already.
		if watched[t.key] || reactions[t.key] != 0 {
			continue
		}
		sc := 0
		for _, g := range t.genres {
			sc += weight[strings.ToLower(g)]
		}
		cands = append(cands, scored{t, sc})
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.score != b.score {
			return a.score > b.score
		}
		if views[a.key] != views[b.key] {
			return views[a.key] > views[b.key]
		}
		if a.added != b.added {
			return a.added > b.added
		}
		return a.title < b.title
	})
	out := []string{}
	for _, c := range cands {
		if len(out) == recommendLimit {
			break
		}
		out = append(out, c.key)
	}
	return out
}

func (s *Service) countInto(ctx context.Context, dst map[string]int, known map[string]bool, prefix, query string, args ...any) error {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return err
		}
		if known[prefix+id] {
			dst[prefix+id] = n
		}
	}
	return rows.Err()
}

func (s *Service) keysInto(ctx context.Context, dst map[string]bool, known map[string]bool, prefix, query string, args ...any) error {
	rows, err := s.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return err
		}
		if known[prefix+id] {
			dst[prefix+id] = true
		}
	}
	return rows.Err()
}

func (s *Service) handleBrowseSignals(w http.ResponseWriter, r *http.Request) {
	sig, err := s.BrowseSignals(r.Context())
	if err != nil {
		writeListErr(w, err)
		return
	}
	httpapi.WriteJSON(w, http.StatusOK, sig)
}

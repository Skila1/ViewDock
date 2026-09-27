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
	Views       map[string]int `json:"views"`
	Watched     []string       `json:"watched"`
	Recommended []string       `json:"recommended"`
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
	if userID := UserIDFrom(ctx); userID != "" {
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

	out := Signals{Views: views, Watched: []string{}, Recommended: recommend(visible, watched, views)}
	for k := range watched {
		out.Watched = append(out.Watched, k)
	}
	sort.Strings(out.Watched)
	return out, nil
}

// recommend ranks unwatched titles by how often their genres appear in what
// the viewer has watched, then by how many people watched them, then newest.
func recommend(visible []browseTitle, watched map[string]bool, views map[string]int) []string {
	weight := map[string]int{}
	for _, t := range visible {
		if watched[t.key] {
			for _, g := range t.genres {
				weight[strings.ToLower(g)]++
			}
		}
	}
	type scored struct {
		browseTitle
		score int
	}
	var cands []scored
	for _, t := range visible {
		if watched[t.key] {
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

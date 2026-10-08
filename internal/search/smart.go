package search

import (
	"context"
	"strconv"
	"strings"
)

type smartFilters struct {
	text       string
	minYear    int
	maxYear    int
	minRuntime int64
	maxRuntime int64
	unwatched  bool
}

func parseSmart(q string) smartFilters {
	filters := smartFilters{}
	words := strings.Fields(strings.ToLower(q))
	text := make([]string, 0, len(words))
	for _, word := range words {
		switch word {
		case "unwatched", "unplayed":
			filters.unwatched = true
			continue
		case "hour", "hours":
			continue
		}
		if strings.HasSuffix(word, "s") && len(word) == 5 {
			if decade, err := strconv.Atoi(strings.TrimSuffix(word, "s")); err == nil && decade >= 1900 && decade <= 2100 {
				filters.minYear, filters.maxYear = decade, decade+9
				continue
			}
		}
		if strings.HasSuffix(word, "-hour") || strings.HasSuffix(word, "-hours") {
			value := strings.TrimSuffix(strings.TrimSuffix(word, "-hours"), "-hour")
			hours, err := strconv.ParseInt(value, 10, 64)
			if err != nil {
				hours = map[string]int64{"one": 1, "two": 2, "three": 3, "four": 4}[value]
			}
			if hours > 0 {
				filters.minRuntime, filters.maxRuntime = hours*60*60*1000-15*60*1000, hours*60*60*1000+15*60*1000
				continue
			}
		}
		text = append(text, word)
	}
	filters.text = strings.Join(text, " ")
	return filters
}

func (s *Service) Smart(ctx context.Context, q string, grantedIDs []string, userID string) ([]Hit, error) {
	filters := parseSmart(q)
	hits, err := s.Query(ctx, filters.text, grantedIDs)
	if err != nil || (filters.minYear == 0 && filters.minRuntime == 0 && !filters.unwatched) {
		return hits, err
	}
	out := make([]Hit, 0, len(hits))
	for _, hit := range hits {
		year, _ := strconv.Atoi(hit.Year)
		if filters.minYear > 0 && (year < filters.minYear || year > filters.maxYear) {
			continue
		}
		if filters.minRuntime > 0 {
			duration, ok := s.duration(ctx, hit)
			if !ok || duration < filters.minRuntime || duration > filters.maxRuntime {
				continue
			}
		}
		if filters.unwatched && userID != "" {
			var watched int
			if err := s.DB.QueryRowContext(ctx, `SELECT COUNT(*) FROM playback_progress WHERE user_id = ? AND item_kind = ? AND item_id = ? AND completed = 1`, userID, hit.ItemKind, hit.ItemID).Scan(&watched); err == nil && watched > 0 {
				continue
			}
		}
		out = append(out, hit)
	}
	return out, nil
}

func (s *Service) duration(ctx context.Context, hit Hit) (int64, bool) {
	var duration int64
	var err error
	if hit.ItemKind == "movie" {
		err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(duration_ms), 0) FROM media_files WHERE movie_id = ?`, hit.ItemID).Scan(&duration)
	} else if hit.ItemKind == "episode" {
		err = s.DB.QueryRowContext(ctx, `SELECT COALESCE(MAX(mf.duration_ms), 0) FROM media_files mf JOIN media_file_episodes mfe ON mfe.media_file_id = mf.id WHERE mfe.episode_id = ?`, hit.ItemID).Scan(&duration)
	}
	return duration, err == nil
}

package metadata

import (
	"strings"
	"unicode"
)

const AutoMatchMin = 80

type Scored struct {
	Candidate SearchResult
	Score     int
}

// Score ranks TMDB candidates against a filename parse. Year agreement and
// normalized title similarity drive the score (0–100).
func Score(query string, year int, cands []SearchResult) []Scored {
	q := normalizeTitle(query)
	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		s := titleScore(q, normalizeTitle(c.DisplayTitle()))
		cy := c.Year()
		if year > 0 && cy > 0 {
			switch {
			case cy == year:
				s += 20
			case abs(cy-year) == 1:
				s += 5
			default:
				s -= 10
			}
		}
		if s > 100 {
			s = 100
		}
		if s < 0 {
			s = 0
		}
		out = append(out, Scored{Candidate: c, Score: s})
	}
	// sort desc
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].Score > out[i].Score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// UniqueWinner returns the unique auto-match candidate when score >= 80
// and no other candidate is within 5 points at or above the threshold.
func UniqueWinner(scored []Scored) (SearchResult, bool) {
	if len(scored) == 0 || scored[0].Score < AutoMatchMin {
		return SearchResult{}, false
	}
	if len(scored) > 1 && scored[1].Score >= AutoMatchMin {
		return SearchResult{}, false
	}
	return scored[0].Candidate, true
}

func titleScore(a, b string) int {
	if a == "" || b == "" {
		return 0
	}
	if a == b {
		return 80
	}
	at, bt := tokens(a), tokens(b)
	if len(at) == 0 || len(bt) == 0 {
		return 0
	}
	inter := 0
	used := map[int]bool{}
	for _, x := range at {
		for i, y := range bt {
			if used[i] {
				continue
			}
			if x == y || (len(x) > 3 && strings.HasPrefix(y, x)) || (len(y) > 3 && strings.HasPrefix(x, y)) {
				inter++
				used[i] = true
				break
			}
		}
	}
	union := len(at) + len(bt) - inter
	if union == 0 {
		return 0
	}
	j := (inter * 80) / union
	// bonus if one contains the other
	if strings.Contains(a, b) || strings.Contains(b, a) {
		j += 10
	}
	if j > 80 {
		j = 80
	}
	return j
}

func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		} else {
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func tokens(s string) []string {
	var out []string
	for _, t := range strings.Fields(s) {
		if t == "the" || t == "a" || t == "an" || t == "and" || t == "of" {
			continue
		}
		out = append(out, t)
	}
	return out
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}

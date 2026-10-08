package mesh

import (
	"net/http"

	"github.com/go-chi/chi/v5/middleware"
	"github.com/viewdock/viewdock/internal/backend"
	"github.com/viewdock/viewdock/internal/reliability"
)

// Ranker orders candidate sources by measured reliability.
type Ranker interface {
	Rank(reliability.RankRequest) reliability.Decision
}

// reliabilityBand is how far below the best score in a tier a worker may be
// and still take part in weighted load balancing. Workers further behind are
// tried only after the balanced set, so a small score difference never turns
// weighted balancing into "always the best node".
const reliabilityBand = 10.0

// rankCandidates keeps the configured priority tiers and reorders within each
// tier: administrator preferences first, then workers within the band in
// their weighted order, then weaker workers by score, then avoided workers.
// Workers excluded by an administrator are removed.
func (d *Dispatcher) rankCandidates(r *http.Request, cands []backend.Node) []backend.Node {
	if d.Ranker == nil || len(cands) == 0 {
		return cands
	}
	device := reliability.DeviceClass(r.UserAgent())
	out := make([]backend.Node, 0, len(cands))
	changed := false
	var explanations []string
	for start := 0; start < len(cands); {
		end := start
		for end < len(cands) && cands[end].Priority == cands[start].Priority {
			end++
		}
		tier, moved, why := rankTier(d.Ranker, cands[start:end], device)
		out = append(out, tier...)
		if moved {
			changed = true
			explanations = append(explanations, why)
		}
		start = end
	}
	if changed && d.Log != nil {
		d.Log.Info("placement reordered by reliability", "category", "mesh",
			"request_id", middleware.GetReqID(r.Context()), "explanation", explanations)
	}
	return out
}

func rankTier(rk Ranker, tier []backend.Node, device string) ([]backend.Node, bool, string) {
	ids := make([]string, len(tier))
	byID := make(map[string]backend.Node, len(tier))
	order := make(map[string]int, len(tier))
	for i, n := range tier {
		id := "node:" + n.ID
		ids[i] = id
		byID[id] = n
		order[id] = i
	}
	dec := rk.Rank(reliability.RankRequest{Candidates: ids, DeviceClass: device})
	ranked := make(map[string]reliability.Ranked, len(dec.Ranking))
	best, haveBest := 0.0, false
	for _, rr := range dec.Ranking {
		ranked[rr.Source] = rr
		if !rr.Excluded && rr.Override == "" && (!haveBest || rr.Metrics.Score > best) {
			best, haveBest = rr.Metrics.Score, true
		}
	}

	var preferred, balanced, weaker, avoided []backend.Node
	for _, id := range ids {
		rr, ok := ranked[id]
		switch {
		case !ok:
			balanced = append(balanced, byID[id])
		case rr.Excluded:
		case rr.Override == reliability.ModePrefer:
			preferred = append(preferred, byID[id])
		case rr.Override == reliability.ModeAvoid:
		case !haveBest || rr.Metrics.Score >= best-reliabilityBand:
			balanced = append(balanced, byID[id])
		}
	}
	for _, rr := range dec.Ranking {
		if rr.Excluded || rr.Override == reliability.ModePrefer {
			continue
		}
		n, ok := byID[rr.Source]
		if !ok {
			continue
		}
		if rr.Override == reliability.ModeAvoid {
			avoided = append(avoided, n)
		} else if haveBest && rr.Metrics.Score < best-reliabilityBand {
			weaker = append(weaker, n)
		}
	}
	out := make([]backend.Node, 0, len(tier))
	out = append(out, preferred...)
	out = append(out, balanced...)
	out = append(out, weaker...)
	out = append(out, avoided...)

	moved := len(out) != len(tier)
	for i := range out {
		if !moved && order["node:"+out[i].ID] != i {
			moved = true
		}
	}
	return out, moved, dec.Explanation
}

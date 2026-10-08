package reliability

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

// RankRequest asks for candidates to be ordered for a viewer. Candidates are
// given in the caller's existing preference order, which breaks ties.
type RankRequest struct {
	Candidates  []string `json:"candidates"`
	Region      string   `json:"region,omitempty"`
	DeviceClass string   `json:"device_class,omitempty"`
}

// Ranked is one candidate's position and the evidence behind it.
type Ranked struct {
	Source string `json:"source"`
	// Rank is 1-based; excluded candidates have rank 0.
	Rank        int        `json:"rank"`
	Excluded    bool       `json:"excluded"`
	Override    string     `json:"override,omitempty"`
	Metrics     ScopeScore `json:"metrics"`
	Explanation string     `json:"explanation"`
}

// Decision is the ordered result with a summary of why the first candidate
// was selected.
type Decision struct {
	Selected    string   `json:"selected"`
	Region      string   `json:"region,omitempty"`
	DeviceClass string   `json:"device_class,omitempty"`
	Ranking     []Ranked `json:"ranking"`
	Explanation string   `json:"explanation"`
}

func overrideGroup(mode string) int {
	switch mode {
	case ModePrefer:
		return 0
	case ModeAvoid:
		return 2
	case ModeExclude:
		return 3
	}
	return 1
}

// Rank orders candidates: administrator preferences first, then score within
// the most specific scope with enough evidence, then the caller's order.
func (t *Tracker) Rank(req RankRequest) Decision {
	region, device := NormalizeLabel(req.Region), NormalizeLabel(req.DeviceClass)
	out := Decision{Region: region, DeviceClass: device, Ranking: []Ranked{}}
	if t == nil {
		out.Explanation = "no reliability data available"
		return out
	}
	now := t.now()
	type cand struct {
		r     Ranked
		order int
	}
	seen := map[string]bool{}
	var cands []cand
	t.mu.RLock()
	for i, raw := range req.Candidates {
		id := cleanSource(raw)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		m := t.pickScopeLocked(id, region, device, now)
		c := cand{r: Ranked{Source: id, Metrics: m}, order: i}
		if ov, ok := t.overrides[id]; ok {
			c.r.Override = ov.Mode
			c.r.Excluded = ov.Mode == ModeExclude
		}
		cands = append(cands, c)
	}
	notes := map[string]string{}
	for id, ov := range t.overrides {
		if seen[id] && ov.Note != "" {
			notes[id] = ov.Note
		}
	}
	t.mu.RUnlock()

	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if ga, gb := overrideGroup(a.r.Override), overrideGroup(b.r.Override); ga != gb {
			return ga < gb
		}
		if a.r.Metrics.Score != b.r.Metrics.Score {
			return a.r.Metrics.Score > b.r.Metrics.Score
		}
		return a.order < b.order
	})
	rank := 0
	for _, c := range cands {
		r := c.r
		if !r.Excluded {
			rank++
			r.Rank = rank
		}
		r.Explanation = explainOne(r, notes[r.Source], t.cfg)
		out.Ranking = append(out.Ranking, r)
	}
	out.Explanation = t.summarize(out)
	if len(out.Ranking) > 0 && !out.Ranking[0].Excluded {
		out.Selected = out.Ranking[0].Source
	}
	return out
}

// pickScopeLocked uses the narrowest scope that has at least MinEvidence,
// falling back to the global scope, then to the neutral prior.
func (t *Tracker) pickScopeLocked(id, region, device string, now time.Time) ScopeScore {
	e := t.sources[id]
	if e == nil {
		return t.neutral()
	}
	var order []string
	if region != "" && device != "" {
		order = append(order, scopeKey(region, device))
	}
	if region != "" {
		order = append(order, scopeKey(region, ""))
	}
	if device != "" {
		order = append(order, scopeKey("", device))
	}
	for _, key := range order {
		if sc := e.scopes[key]; sc != nil {
			if s := t.scopeScoreLocked(sc, now); s.Evidence >= t.cfg.MinEvidence {
				return s
			}
		}
	}
	if sc := e.scopes["global"]; sc != nil {
		return t.scopeScoreLocked(sc, now)
	}
	return t.neutral()
}

func scopeLabel(s ScopeScore) string {
	switch {
	case s.Region != "" && s.DeviceClass != "":
		return fmt.Sprintf("region %s on %s devices", s.Region, s.DeviceClass)
	case s.Region != "":
		return "region " + s.Region
	case s.DeviceClass != "":
		return s.DeviceClass + " devices"
	}
	return "all viewers"
}

func explainOne(r Ranked, note string, cfg Config) string {
	m := r.Metrics
	var b strings.Builder
	if r.Excluded {
		b.WriteString("excluded by administrator")
		if note != "" {
			fmt.Fprintf(&b, " (%s)", note)
		}
		return b.String()
	}
	fmt.Fprintf(&b, "rank %d, score %.1f", r.Rank, m.Score)
	if m.Evidence < minEvidenceShown {
		b.WriteString(", no recent evidence so the neutral prior applies")
	} else {
		fmt.Fprintf(&b, " from %.1f weighted sessions (%s): %.0f%% success, %.2f stalls per session",
			m.Evidence, scopeLabel(m), m.SuccessRate*100, m.StallsPerSession)
		if m.LatencyMS > 0 {
			fmt.Fprintf(&b, ", %.0f ms start latency", m.LatencyMS)
		}
		if m.TTFFMS > 0 {
			fmt.Fprintf(&b, ", %.0f ms to first frame", m.TTFFMS)
		}
		if m.Evidence < cfg.MinEvidence {
			b.WriteString(" (limited evidence)")
		}
	}
	switch r.Override {
	case ModePrefer:
		b.WriteString("; preferred by administrator")
	case ModeAvoid:
		b.WriteString("; avoided by administrator")
	}
	if note != "" && r.Override != "" {
		fmt.Fprintf(&b, " (%s)", note)
	}
	return b.String()
}

const minEvidenceShown = 0.05

// summarize explains the first choice relative to the runner-up by naming
// the factor that separated them.
func (t *Tracker) summarize(d Decision) string {
	var active []Ranked
	excluded := 0
	for _, r := range d.Ranking {
		if r.Excluded {
			excluded++
			continue
		}
		active = append(active, r)
	}
	if len(active) == 0 {
		if excluded > 0 {
			return "every candidate is excluded by an administrator override"
		}
		return "no candidates"
	}
	top := active[0]
	var b strings.Builder
	fmt.Fprintf(&b, "selected %s (score %.1f)", top.Source, top.Metrics.Score)
	if len(active) == 1 {
		b.WriteString(" as the only eligible candidate")
	} else {
		next := active[1]
		fmt.Fprintf(&b, " over %s (score %.1f): ", next.Source, next.Metrics.Score)
		switch {
		case top.Override == ModePrefer && next.Override != ModePrefer:
			b.WriteString("administrator preference")
		case next.Override == ModeAvoid && top.Override != ModeAvoid:
			fmt.Fprintf(&b, "%s is avoided by an administrator", next.Source)
		case top.Metrics.Score == next.Metrics.Score:
			b.WriteString("equal scores, kept the configured order")
		default:
			b.WriteString(t.mainFactor(top.Metrics, next.Metrics))
		}
	}
	if excluded > 0 {
		fmt.Fprintf(&b, "; %d excluded by administrator", excluded)
	}
	return b.String()
}

func (t *Tracker) mainFactor(a, b ScopeScore) string {
	cfg := t.cfg
	pen := func(v, ref float64) float64 {
		if v <= 0 {
			v = ref
		}
		return v / (v + ref)
	}
	type factor struct {
		gain float64
		text string
	}
	factors := []factor{
		{weightSuccess * (a.SuccessRate - b.SuccessRate), fmt.Sprintf("higher success rate (%.0f%% vs %.0f%%)", a.SuccessRate*100, b.SuccessRate*100)},
		{weightStalls * (b.StallsPerSession/(b.StallsPerSession+1) - a.StallsPerSession/(a.StallsPerSession+1)), fmt.Sprintf("fewer stalls (%.2f vs %.2f per session)", a.StallsPerSession, b.StallsPerSession)},
		{weightLatency * (pen(b.LatencyMS, cfg.LatencyRefMS) - pen(a.LatencyMS, cfg.LatencyRefMS)), fmt.Sprintf("lower start latency (%.0f ms vs %.0f ms)", a.LatencyMS, b.LatencyMS)},
		{weightTTFF * (pen(b.TTFFMS, cfg.TTFFRefMS) - pen(a.TTFFMS, cfg.TTFFRefMS)), fmt.Sprintf("faster first frame (%.0f ms vs %.0f ms)", a.TTFFMS, b.TTFFMS)},
	}
	best := factors[0]
	for _, f := range factors[1:] {
		if f.gain > best.gain {
			best = f
		}
	}
	if best.gain <= 0 || math.IsNaN(best.gain) {
		return "higher overall score"
	}
	return best.text
}

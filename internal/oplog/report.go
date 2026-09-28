package oplog

import (
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/httpapi"
)

// CategoryClientError holds error reports sent by browsers, such as a
// player that failed to start or stopped during playback.
const CategoryClientError = "client_error"

const (
	maxReportBody     = 48 << 10
	maxReportMessage  = 300
	maxReportTrace    = 24 << 10
	maxReportKeys     = 24
	maxReportValueLen = 300
	reportsPerMinute  = 20
)

var reportKeyRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

var reportLimit = newIngestLimiterWithCap(reportsPerMinute)

type reportBody struct {
	Message string         `json:"message"`
	Code    string         `json:"code"`
	Stage   string         `json:"stage"`
	Context map[string]any `json:"context"`
	Trace   string         `json:"trace"`
}

func (s *Store) handleReport(w http.ResponseWriter, r *http.Request) {
	if !reportLimit.allow(ingestKey(r)) {
		httpapi.WriteErr(w, http.StatusTooManyRequests, "rate_limited", "too many error reports")
		return
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxReportBody+1))
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid body")
		return
	}
	if len(raw) > maxReportBody {
		httpapi.WriteErr(w, http.StatusRequestEntityTooLarge, "too_large", "error report is too large")
		return
	}
	var body reportBody
	if err := json.Unmarshal(raw, &body); err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid json")
		return
	}
	e, err := buildReport(body)
	if err != nil {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	if p := auth.FromRequest(r); p != nil && p.IsUser() {
		e.ActorID = p.UserID
	}
	ua := r.UserAgent()
	if len(ua) > 160 {
		ua = ua[:160]
	}
	e.Details["ua"] = ua
	s.Write(r.Context(), e)
	httpapi.WriteJSON(w, http.StatusCreated, map[string]string{"id": e.ID})
}

// buildReport validates a client error report. Context keeps scalar values
// only, so reports cannot carry arbitrary nested payloads.
func buildReport(b reportBody) (Entry, error) {
	msg := clip(oneLine(b.Message), maxReportMessage)
	if msg == "" {
		return Entry{}, errors.New("message required")
	}
	details := map[string]any{}
	if code := clip(oneLine(b.Code), 64); code != "" {
		details["code"] = code
	}
	if stage := clip(oneLine(b.Stage), 64); stage != "" {
		details["stage"] = stage
	}
	keys := make([]string, 0, len(b.Context))
	for k := range b.Context {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kept := 0
	for _, k := range keys {
		if kept >= maxReportKeys {
			break
		}
		if !reportKeyRe.MatchString(k) || details[k] != nil {
			continue
		}
		switch v := b.Context[k].(type) {
		case string:
			details[k] = clip(oneLine(v), maxReportValueLen)
		case bool:
			details[k] = v
		case float64:
			if math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			details[k] = v
		default:
			continue
		}
		kept++
	}
	if trace := strings.TrimSpace(b.Trace); trace != "" {
		details["trace"] = clip(trace, maxReportTrace)
	}
	return Entry{
		ID:       uuid.NewString(),
		Level:    "error",
		Category: CategoryClientError,
		Message:  msg,
		Details:  details,
	}, nil
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

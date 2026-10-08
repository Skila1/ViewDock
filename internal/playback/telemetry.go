package playback

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/viewdock/viewdock/internal/diagnostics"
	"github.com/viewdock/viewdock/internal/httpapi"
	"github.com/viewdock/viewdock/internal/reliability"
)

// Client telemetry is kept only in the in-memory flight recorder. It is
// never written to the database.
const (
	telemetryMaxBody      = 32 << 10
	telemetryMaxEvents    = 50
	telemetryMaxKeys      = 16
	telemetryMaxStringLen = 200
	telemetryMaxSkew      = 5 * time.Minute
	telemetryRetryAfter   = "5"
)

// telemetryTypes is the client event vocabulary. Anything else is rejected
// so the recorder cannot be used as free-form storage.
var telemetryTypes = map[string]bool{
	"source_selected":  true,
	"first_frame":      true,
	"startup_failed":   true,
	"stall":            true,
	"stall_end":        true,
	"buffer":           true,
	"bitrate_change":   true,
	"quality_change":   true,
	"failover":         true,
	"codec_error":      true,
	"error":            true,
	"drift_correction": true,
	"reconnect":        true,
	"recovered":        true,
	"seek":             true,
}

// Keys the server fills from the session; client values are discarded.
var telemetryReserved = map[string]bool{"source": true, "device_class": true, "region": true, "clock_adjusted": true}

var telemetryKeyRE = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

func telemetrySensitiveKey(k string) bool {
	for _, s := range []string{"token", "secret", "password", "passwd", "cookie", "auth", "email", "session_key", "credential"} {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

type telemetryEvent struct {
	Type string         `json:"type"`
	At   float64        `json:"at"`
	Data map[string]any `json:"data"`
}

type telemetryBody struct {
	Events        []telemetryEvent `json:"events"`
	CorrelationID string           `json:"correlation_id"`
}

type telemetryResult struct {
	Accepted int `json:"accepted"`
	Rejected int `json:"rejected"`
	Dropped  int `json:"dropped"`
}

func (a *API) handleTelemetry(w http.ResponseWriter, r *http.Request) {
	s := a.live(w, r)
	if s == nil {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, telemetryMaxBody)
	var body telemetryBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpapi.WriteErr(w, http.StatusRequestEntityTooLarge, "payload_too_large", "telemetry batch is too large")
			return
		}
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "invalid telemetry payload")
		return
	}
	if len(body.Events) == 0 {
		httpapi.WriteErr(w, http.StatusBadRequest, "bad_request", "events required")
		return
	}
	if len(body.Events) > telemetryMaxEvents {
		httpapi.WriteErr(w, http.StatusRequestEntityTooLarge, "too_many_events", "send at most 50 events per batch")
		return
	}
	if a.Flight == nil {
		httpapi.WriteJSON(w, http.StatusAccepted, telemetryResult{Dropped: len(body.Events)})
		return
	}

	s.mu.Lock()
	source := s.MediaFileID
	s.mu.Unlock()
	device := reliability.DeviceClass(s.Client.UserAgent)
	now := time.Now().UTC()
	requestID := diagnostics.RequestID(r.Context())
	correlationID := diagnostics.CleanRequestID(strings.TrimSpace(body.CorrelationID))

	valid := make([]diagnostics.Event, 0, len(body.Events))
	res := telemetryResult{}
	for _, ev := range body.Events {
		e, ok := buildTelemetryEvent(ev, now)
		if !ok {
			res.Rejected++
			continue
		}
		e.SessionID, e.RequestID, e.CorrelationID = s.ID, requestID, correlationID
		if source != "" {
			e.Data["source"] = source
		}
		if device != "" {
			e.Data["device_class"] = device
		}
		valid = append(valid, e)
	}
	if len(valid) == 0 {
		httpapi.WriteJSON(w, http.StatusBadRequest, map[string]any{"code": "bad_request", "message": "no valid telemetry events", "rejected": res.Rejected})
		return
	}
	granted := a.Flight.Admit(s.ID, len(valid))
	if granted == 0 {
		w.Header().Set("Retry-After", telemetryRetryAfter)
		httpapi.WriteErr(w, http.StatusTooManyRequests, "rate_limited", "telemetry rate limit reached")
		return
	}
	res.Dropped = len(valid) - granted
	for _, e := range valid[:granted] {
		if a.Flight.RecordEvent(e) {
			res.Accepted++
		} else {
			res.Dropped++
		}
	}
	httpapi.WriteJSON(w, http.StatusAccepted, res)
}

// buildTelemetryEvent validates one client event and returns a privacy-safe
// flight recorder event. Client clocks are trusted only within a skew window.
func buildTelemetryEvent(ev telemetryEvent, now time.Time) (diagnostics.Event, bool) {
	typ := strings.TrimSpace(ev.Type)
	if !telemetryTypes[typ] {
		return diagnostics.Event{}, false
	}
	e := diagnostics.Event{Type: typ, Origin: diagnostics.OriginClient, At: now, Data: map[string]any{}}
	if ev.At > 0 && !math.IsInf(ev.At, 0) && !math.IsNaN(ev.At) {
		at := time.UnixMilli(int64(ev.At)).UTC()
		if d := at.Sub(now); d <= telemetryMaxSkew && d >= -telemetryMaxSkew {
			e.At = at
		} else {
			e.Data["clock_adjusted"] = true
		}
	}
	keys := make([]string, 0, len(ev.Data))
	for k := range ev.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	kept := 0
	for _, k := range keys {
		if kept >= telemetryMaxKeys {
			break
		}
		if !telemetryKeyRE.MatchString(k) || telemetryReserved[k] || telemetrySensitiveKey(k) {
			continue
		}
		clean, ok := telemetryValue(ev.Data[k])
		if !ok {
			continue
		}
		e.Data[k] = clean
		kept++
	}
	return e, true
}

// telemetryValue keeps scalars only. Strings lose control characters and
// query strings (which may hold stream tokens) and are length capped.
func telemetryValue(v any) (any, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return nil, false
		}
		return x, true
	case string:
		return telemetryString(x)
	}
	return nil, false
}

func telemetryString(s string) (any, bool) {
	if strings.Contains(strings.ToLower(s), "stoken=") {
		if i := strings.IndexByte(s, '?'); i >= 0 {
			s = s[:i]
		} else {
			return nil, false
		}
	}
	if i := strings.IndexByte(s, '?'); i >= 0 && looksLikeURL(s) {
		s = s[:i]
	}
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, strings.TrimSpace(s))
	if r := []rune(s); len(r) > telemetryMaxStringLen {
		s = string(r[:telemetryMaxStringLen])
	}
	return s, true
}

func looksLikeURL(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "/") || strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "blob:")
}

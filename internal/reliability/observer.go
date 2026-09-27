package reliability

import (
	"strings"

	"github.com/viewdock/viewdock/internal/diagnostics"
)

// FlightObserver feeds a Recorder from the playback flight recorder, so
// existing server events and client telemetry update scores without the
// producers knowing about this package. Register it with
// diagnostics.Recorder.AddObserver.
type FlightObserver struct {
	Recorder Recorder
}

// SourceObserved maps server-side source outcomes: a success is a start that
// began, a failure is playback that could not continue on that source.
func (f FlightObserver) SourceObserved(o diagnostics.SourceObservation) {
	if f.Recorder == nil {
		return
	}
	out := Outcome{Source: o.Source, At: o.At}
	if o.Success {
		out.Kind, out.Success, out.LatencyMS = KindStart, true, o.Latency.Milliseconds()
	} else {
		out.Kind, out.Reason = KindFailure, "server pipeline failure"
	}
	f.Recorder.Observe(out)
	if o.Stall {
		f.Recorder.Observe(Outcome{Source: o.Source, Kind: KindStall, At: o.At})
	}
}

// FlightEvent maps client telemetry. Events need a "source" data field,
// which the telemetry endpoint fills from the session.
func (f FlightObserver) FlightEvent(e diagnostics.Event) {
	if f.Recorder == nil || e.Origin != diagnostics.OriginClient {
		return
	}
	source, _ := e.Data["source"].(string)
	if source == "" {
		return
	}
	out := Outcome{
		Source: source, SessionID: e.SessionID, RequestID: e.RequestID, At: e.At,
		Region: str(e.Data["region"]), DeviceClass: str(e.Data["device_class"]),
	}
	switch e.Type {
	case "first_frame":
		out.Kind, out.TTFFMS = KindFirstFrame, num(e.Data["ttff_ms"])
		if out.TTFFMS <= 0 {
			return
		}
	case "stall":
		out.Kind = KindStall
	case "startup_failed", "codec_error", "failover":
		out.Kind, out.Reason = KindFailure, reason(e)
	case "error":
		if fatal, _ := e.Data["fatal"].(bool); !fatal {
			return
		}
		out.Kind, out.Reason = KindFailure, reason(e)
	default:
		return
	}
	f.Recorder.Observe(out)
}

func reason(e diagnostics.Event) string {
	if code := str(e.Data["code"]); code != "" {
		return e.Type + ": " + code
	}
	return e.Type
}

func str(v any) string {
	s, _ := v.(string)
	return s
}

func num(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

// DeviceClass buckets a user agent into tv, mobile, tablet or desktop, or
// returns "" when the user agent is empty.
func DeviceClass(userAgent string) string {
	u := strings.ToLower(userAgent)
	if strings.TrimSpace(u) == "" {
		return ""
	}
	for _, tv := range []string{"smart-tv", "smarttv", "tizen", "web0s", "webos", "appletv", "apple tv", "android tv", "googletv", "crkey", "bravia", "aftb", "aftm", "aftt", "afts", "roku", "hbbtv", "playstation", "xbox"} {
		if strings.Contains(u, tv) {
			return "tv"
		}
	}
	if strings.Contains(u, "ipad") || (strings.Contains(u, "android") && !strings.Contains(u, "mobile")) {
		return "tablet"
	}
	if strings.Contains(u, "iphone") || strings.Contains(u, "ipod") || strings.Contains(u, "mobile") {
		return "mobile"
	}
	return "desktop"
}

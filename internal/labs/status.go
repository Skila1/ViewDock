package labs

import "context"

// DashboardStatus maps the broadcaster health to a resilience dashboard
// status: "unconfigured" until an administrator acknowledges the risk notice,
// "ok" when idle or healthy, "degraded" while stalled or backing off, and
// "down" after a failure. The pipeline runs in its own process, so its state
// never affects core playback.
func (b *Broadcaster) DashboardStatus(ctx context.Context) (string, Health) {
	h := b.Health()
	if !b.Running() {
		if ack, err := loadAck(ctx, b.Store); err == nil && ack == nil {
			h.State = StateDisabled
		}
	}
	switch h.State {
	case StateDisabled:
		return "unconfigured", h
	case StateFailed:
		return "down", h
	case StateBackoff:
		return "degraded", h
	case StateRunning:
		if h.Stalled {
			return "degraded", h
		}
	}
	return "ok", h
}

package interactions

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot/gateway"
)

func TestDiagnosticsWithoutGatewayOmitChecks(t *testing.T) {
	d := newDiagHarness(t)
	out, _ := d.request(http.MethodGet)
	if out.Gateway != nil {
		t.Fatalf("gateway = %+v", out.Gateway)
	}
	for _, c := range out.Checks {
		if c.Group == "gateway" {
			t.Fatalf("unexpected gateway check %+v", c)
		}
	}
}

func TestDiagnosticsReportLiveGatewayWithoutContactingDiscord(t *testing.T) {
	d := newDiagHarness(t)
	since := time.Date(2026, 9, 27, 10, 0, 0, 0, time.UTC)
	st := gateway.Status{State: gateway.StateOnline, Presence: "online", Activity: gateway.ActivityText, BotName: "ViewDock", ConnectedSince: &since, LastConnectedAt: &since, LatencyMS: 42}
	d.svc.Gateway = func() gateway.Status { return st }
	d.svc.ReconnectGateway = func() {}

	out, body := d.request(http.MethodGet)
	if d.discord.count() != 0 {
		t.Fatalf("gateway checks made %d Discord requests", d.discord.count())
	}
	if out.Gateway == nil || out.Gateway.State != gateway.StateOnline || out.Gateway.Activity != gateway.ActivityText {
		t.Fatalf("gateway = %+v", out.Gateway)
	}
	conn := checkByID(t, out, "gateway_connection")
	if conn.Status != CheckOK || conn.Action != nil || !strings.Contains(conn.Detail, "ViewDock") || !strings.Contains(conn.Detail, "HTTP interactions") {
		t.Fatalf("connection = %+v", conn)
	}
	if p := checkByID(t, out, "gateway_presence"); p.Status != CheckOK || !strings.Contains(p.Detail, "Watching movies & TV") {
		t.Fatalf("presence = %+v", p)
	}
	assertNoSecrets(t, body)

	// Gateway checks are grouped after the bot and before the command checks.
	groups := []string{}
	for _, c := range out.Checks {
		if len(groups) == 0 || groups[len(groups)-1] != c.Group {
			groups = append(groups, c.Group)
		}
	}
	if g := strings.Join(groups, ","); !strings.Contains(g, "oauth,gateway,commands") {
		t.Fatalf("group order = %v", groups)
	}

	// The check reads live state, so a change shows on the next load.
	st = gateway.Status{State: gateway.StateReconnecting, Attempts: 3, LastError: "The connection to Discord dropped.", LastConnectedAt: &since}
	out, _ = d.request(http.MethodGet)
	conn = checkByID(t, out, "gateway_connection")
	if conn.Status != CheckWarn || conn.Action == nil || conn.Action.ID != ActionReconnect || !strings.Contains(conn.Detail, "Last connected") {
		t.Fatalf("reconnecting = %+v", conn)
	}
	if !strings.Contains(conn.Detail, "Slash commands") {
		t.Fatalf("reconnecting detail should explain HTTP interactions still work: %q", conn.Detail)
	}
}

func TestGatewayChecksExplainStates(t *testing.T) {
	retry := time.Date(2026, 9, 27, 11, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		st     gateway.Status
		status string
		action string
	}{
		{"disabled", gateway.Status{State: gateway.StateDisabled}, CheckSkipped, ActionEditBot},
		{"connecting", gateway.Status{State: gateway.StateConnecting}, CheckInfo, ""},
		{"first reconnect", gateway.Status{State: gateway.StateReconnecting, Attempts: 1}, CheckInfo, ActionReconnect},
		{"rejected token", gateway.Status{State: gateway.StateFailed, TokenRejected: true, CloseCode: 4004, LastError: "Discord rejected the bot token."}, CheckError, ActionEditBot},
		{"other failure", gateway.Status{State: gateway.StateFailed, CloseCode: 4014, LastError: "Disallowed intents.", NextRetryAt: &retry}, CheckError, ActionReconnect},
		{"stopped", gateway.Status{State: gateway.StateStopped}, CheckInfo, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			checks := gatewayChecks(tc.st, false, true)
			if len(checks) != 2 {
				t.Fatalf("checks = %+v", checks)
			}
			c := checks[0]
			if c.Status != tc.status || c.Detail == "" {
				t.Fatalf("connection = %+v", c)
			}
			got := ""
			if c.Action != nil {
				got = c.Action.ID
			}
			if got != tc.action {
				t.Fatalf("action = %q, want %q", got, tc.action)
			}
			if tc.st.LastError != "" && !strings.Contains(c.Detail, tc.st.LastError) {
				t.Fatalf("detail %q lacks the error", c.Detail)
			}
		})
	}
	if c := gatewayChecks(gateway.Status{State: gateway.StateDisabled}, true, true)[0]; !strings.Contains(c.Detail, "separate bot application") {
		t.Fatalf("separate mode detail = %q", c.Detail)
	}
	if c := gatewayChecks(gateway.Status{State: gateway.StateFailed}, false, false)[0]; c.Action != nil {
		t.Fatalf("reconnect offered without a handler: %+v", c.Action)
	}
}

func TestReconnectGatewayEndpoint(t *testing.T) {
	d := newDiagHarness(t)
	req := httptest.NewRequest(http.MethodPost, "/admin/integrations/discord/gateway/reconnect", nil)
	req = req.WithContext(auth.WithPrincipal(req.Context(), &auth.Principal{Kind: auth.KindUser, UserID: "u-admin"}))
	rec := httptest.NewRecorder()
	d.svc.handleReconnectGateway(rec, req)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("without gateway = %d %s", rec.Code, rec.Body.String())
	}

	calls := 0
	d.svc.ReconnectGateway = func() { calls++ }
	rec = httptest.NewRecorder()
	d.svc.handleReconnectGateway(rec, req)
	if rec.Code != http.StatusAccepted || calls != 1 {
		t.Fatalf("reconnect = %d %s, calls = %d", rec.Code, rec.Body.String(), calls)
	}
}

package interactions

import (
	"fmt"
	"net/http"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
	"github.com/viewdock/viewdock/internal/discordbot/gateway"
	"github.com/viewdock/viewdock/internal/httpapi"
)

const gatewayIndependent = " Slash commands and party invites do not depend on it; they use the HTTP interactions endpoint and the bot API."

func stamp(t *time.Time) string {
	if t == nil {
		return ""
	}
	return t.UTC().Format("2 Jan 2006 15:04 MST")
}

// gatewayChecks describe the live presence connection. They read in-memory
// state only, so they are current on every page load without contacting Discord.
func gatewayChecks(st gateway.Status, separate, canReconnect bool) []Check {
	conn := Check{ID: "gateway_connection", Group: "gateway", Label: "Gateway connection"}
	pres := Check{ID: "gateway_presence", Group: "gateway", Label: "Presence"}
	var reconnect *CheckAction
	if canReconnect {
		reconnect = &CheckAction{ID: ActionReconnect, Label: "Reconnect now"}
	}
	last := ""
	if st.LastConnectedAt != nil {
		last = " Last connected " + stamp(st.LastConnectedAt) + "."
	}
	retry := ""
	if st.NextRetryAt != nil {
		retry = " Next attempt " + stamp(st.NextRetryAt) + "."
	}
	switch st.State {
	case gateway.StateOnline:
		who := "the bot"
		if st.BotName != "" {
			who = st.BotName
		}
		conn.Status = CheckOK
		conn.Detail = fmt.Sprintf("Connected as %s since %s. The Gateway only keeps the bot online; slash commands use the HTTP interactions endpoint.", who, stamp(st.ConnectedSince))
		if st.LatencyMS > 0 {
			conn.Detail += fmt.Sprintf(" Heartbeat latency %d ms.", st.LatencyMS)
		}
		pres.Status, pres.Detail = CheckOK, "Online, "+st.Activity+"."
	case gateway.StateDisabled:
		conn.Status = CheckSkipped
		conn.Detail = "No bot token is set for the " + botCredentialOwner(separate) + ", so the bot shows offline in Discord."
		conn.Action = &CheckAction{ID: ActionEditBot, Label: "Add bot token"}
		pres.Status, pres.Detail = CheckSkipped, "Offline."
	case gateway.StateConnecting:
		conn.Status, conn.Detail = CheckInfo, "Connecting to Discord."+last
		pres.Status, pres.Detail = CheckInfo, "Offline until the connection is ready."
	case gateway.StateReconnecting:
		conn.Status = CheckWarn
		if st.Attempts <= 1 {
			conn.Status = CheckInfo
		}
		conn.Detail = fmt.Sprintf("Reconnecting (attempt %d). %s%s%s%s", st.Attempts, st.LastError, retry, last, gatewayIndependent)
		conn.Action = reconnect
		pres.Status, pres.Detail = conn.Status, "Offline while reconnecting."
	case gateway.StateFailed:
		conn.Status = CheckError
		conn.Detail = st.LastError + retry + last + gatewayIndependent
		if st.TokenRejected {
			conn.Action = &CheckAction{ID: ActionEditBot, Label: "Replace bot token"}
		} else {
			conn.Action = reconnect
		}
		pres.Status, pres.Detail = CheckError, "Offline."
	default:
		conn.Status, conn.Detail = CheckInfo, "Not running on this server."+last
		pres.Status, pres.Detail = CheckSkipped, "Offline."
	}
	return []Check{conn, pres}
}

func (s *Service) handleReconnectGateway(w http.ResponseWriter, r *http.Request) {
	if s.ReconnectGateway == nil {
		httpapi.WriteErr(w, http.StatusServiceUnavailable, "gateway_unavailable", "The Discord Gateway does not run on this server.")
		return
	}
	s.ReconnectGateway()
	if p := auth.FromRequest(r); p != nil && s.Audit != nil {
		s.Audit.Event(r.Context(), p.UserID, "discord.gateway_reconnect", "", httpapi.ClientIPString(r, s.Cfg), "")
	}
	httpapi.WriteJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

// Package gateway keeps the official Discord bot online through one Discord
// Gateway session. The session carries presence only: it requests no intents
// and ignores dispatches, because slash commands, autocomplete and buttons
// keep arriving through the HTTP interactions endpoint.
package gateway

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

// DefaultURL is Discord's Gateway entry point for API v10 with JSON encoding.
const DefaultURL = "wss://gateway.discord.gg/?v=10&encoding=json"

// ActivityName with the Watching activity type shows as "Watching movies & TV".
const (
	ActivityName     = "movies & TV"
	ActivityText     = "Watching " + ActivityName
	activityWatching = 3
)

// Connection states reported by Status.
const (
	// StateDisabled means no bot token is configured.
	StateDisabled     = "disabled"
	StateConnecting   = "connecting"
	StateOnline       = "online"
	StateReconnecting = "reconnecting"
	// StateFailed means Discord refused the session; it is retried after a
	// configuration change or failedRetry.
	StateFailed  = "failed"
	StateStopped = "stopped"
)

const (
	opDispatch       = 0
	opHeartbeat      = 1
	opIdentify       = 2
	opResume         = 6
	opReconnect      = 7
	opInvalidSession = 9
	opHello          = 10
	opHeartbeatAck   = 11
)

const (
	userAgent    = "DiscordBot (https://github.com/viewdock/viewdock, 2)"
	dialTimeout  = 15 * time.Second
	helloTimeout = 20 * time.Second
	writeTimeout = 10 * time.Second
	maxMessage   = 8 << 20
	maxBackoff   = 2 * time.Minute
	failedRetry  = 30 * time.Minute
	// A session that stayed up this long resets the backoff.
	healthyAfter = time.Minute
	// closeResume ends a connection while keeping the session resumable;
	// closing with 1000 or 1001 would end the session and show the bot offline.
	closeResume = 4000
)

// Status describes the Gateway session. It never contains the bot token.
type Status struct {
	State string `json:"state"`
	// Presence is what Discord shows for the bot: "online" or "offline".
	Presence string `json:"presence"`
	Activity string `json:"activity"`
	BotID    string `json:"bot_id,omitempty"`
	BotName  string `json:"bot_name,omitempty"`
	// ConnectedSince is when the current session came online.
	ConnectedSince *time.Time `json:"connected_since,omitempty"`
	// LastConnectedAt is the last successful identify or resume.
	LastConnectedAt *time.Time `json:"last_connected_at,omitempty"`
	LastError       string     `json:"last_error,omitempty"`
	LastErrorAt     *time.Time `json:"last_error_at,omitempty"`
	CloseCode       int        `json:"close_code,omitempty"`
	// TokenRejected is true when Discord refused the bot token.
	TokenRejected bool       `json:"token_rejected"`
	Attempts      int        `json:"attempts"`
	NextRetryAt   *time.Time `json:"next_retry_at,omitempty"`
	LatencyMS     int64      `json:"latency_ms,omitempty"`
}

// Manager owns at most one Gateway connection at a time.
type Manager struct {
	// Token returns the active bot token, empty when none is configured.
	Token  func() string
	URL    string
	Dialer *websocket.Dialer
	Log    *slog.Logger

	backoff     func(attempt int) time.Duration
	invalidWait func() time.Duration
	now         func() time.Time

	mu     sync.Mutex
	status Status
	cancel context.CancelFunc
	done   chan struct{}
	reload chan struct{}
}

func New(token func() string, log *slog.Logger) *Manager {
	if log == nil {
		log = slog.Default()
	}
	return &Manager{
		Token:       token,
		URL:         DefaultURL,
		Dialer:      &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: dialTimeout},
		Log:         log,
		backoff:     defaultBackoff,
		invalidWait: func() time.Duration { return time.Second + rand.N(4*time.Second) },
		now:         time.Now,
		status:      Status{State: StateStopped, Presence: "offline", Activity: ActivityText},
		reload:      make(chan struct{}, 1),
	}
}

func defaultBackoff(attempt int) time.Duration {
	d := time.Second
	for i := 1; i < attempt && d < maxBackoff; i++ {
		d *= 2
	}
	d = min(d, maxBackoff)
	// Up to 20% jitter so many installs do not reconnect in lockstep.
	return d + rand.N(d/5+1)
}

// Start connects in the background. It never blocks and never fails; problems
// are reported through Status. Calling Start twice has no effect.
func (m *Manager) Start(parent context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	m.cancel, m.done = cancel, done
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				m.Log.Error("discord gateway stopped unexpectedly", "category", "discord", "panic", fmt.Sprint(r))
				m.update(func(s *Status) {
					s.State, s.Presence, s.ConnectedSince = StateFailed, "offline", nil
					s.LastError = "The Gateway connection stopped unexpectedly. Restart ViewDock to reconnect."
				})
			}
		}()
		m.run(ctx)
	}()
}

// Stop closes the session with a normal close, so Discord shows the bot
// offline right away, and waits briefly for the connection to end.
func (m *Manager) Stop() {
	m.mu.Lock()
	cancel, done := m.cancel, m.done
	m.cancel, m.done = nil, nil
	m.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		m.Log.Warn("discord gateway did not stop in time", "category", "discord")
	}
}

// Reload re-reads the bot token. The session restarts only when the token
// changed; a failed or waiting connection retries now.
func (m *Manager) Reload() {
	select {
	case m.reload <- struct{}{}:
	default:
	}
}

// Status returns a copy of the current state.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.status
	for _, p := range []**time.Time{&s.ConnectedSince, &s.LastConnectedAt, &s.LastErrorAt, &s.NextRetryAt} {
		if *p != nil {
			t := **p
			*p = &t
		}
	}
	return s
}

func (m *Manager) update(fn func(*Status)) {
	m.mu.Lock()
	fn(&m.status)
	m.mu.Unlock()
}

func (m *Manager) token() string {
	if m.Token == nil {
		return ""
	}
	return strings.TrimSpace(m.Token())
}

func fingerprint(token string) [32]byte { return sha256.Sum256([]byte(token)) }

type session struct {
	id        string
	resumeURL string
	seq       int64
	hasSeq    bool
}

type outcomeKind int

const (
	outStopped outcomeKind = iota
	outReload
	outRetry
	outFatal
)

type outcome struct {
	kind   outcomeKind
	resume bool
	// delay before the next attempt; negative means use the backoff.
	delay         time.Duration
	err           string
	code          int
	tokenRejected bool
	healthy       bool
}

func (m *Manager) run(ctx context.Context) {
	var (
		sess    session
		current [32]byte
		first   = true
		attempt int
	)
	for ctx.Err() == nil {
		token := m.token()
		fp := fingerprint(token)
		if first || fp != current {
			current, first, sess, attempt = fp, false, session{}, 0
			m.update(func(s *Status) {
				s.BotID, s.BotName, s.LastError, s.LastErrorAt, s.CloseCode, s.TokenRejected, s.Attempts = "", "", "", nil, 0, false, 0
			})
		}
		if token == "" {
			m.update(func(s *Status) {
				s.State, s.Presence, s.ConnectedSince, s.NextRetryAt, s.LatencyMS = StateDisabled, "offline", nil, nil, 0
			})
			if !m.wait(ctx, untilReload) {
				break
			}
			continue
		}
		m.update(func(s *Status) {
			s.State, s.Presence, s.ConnectedSince, s.NextRetryAt = StateConnecting, "offline", nil, nil
			if attempt > 0 {
				s.State = StateReconnecting
			}
		})
		out := m.session(ctx, token, &sess)
		switch out.kind {
		case outStopped:
			m.update(func(s *Status) {
				s.State, s.Presence, s.ConnectedSince, s.NextRetryAt = StateStopped, "offline", nil, nil
			})
			return
		case outReload:
			continue
		case outFatal:
			sess = session{}
			retryAt := m.now().Add(failedRetry)
			m.Log.Warn("discord gateway refused", "category", "discord", "code", out.code, "err", out.err)
			m.update(func(s *Status) {
				now := m.now()
				s.State, s.Presence, s.ConnectedSince = StateFailed, "offline", nil
				s.LastError, s.LastErrorAt, s.CloseCode, s.TokenRejected, s.NextRetryAt = out.err, &now, out.code, out.tokenRejected, &retryAt
			})
			if !m.wait(ctx, failedRetry) {
				m.update(func(s *Status) { s.State, s.NextRetryAt = StateStopped, nil })
				return
			}
		case outRetry:
			if !out.resume {
				sess = session{}
			}
			if out.healthy {
				attempt = 0
			}
			attempt++
			delay := out.delay
			if delay < 0 {
				delay = m.backoff(attempt)
			}
			retryAt := m.now().Add(delay)
			m.Log.Warn("discord gateway disconnected", "category", "discord", "code", out.code, "err", out.err, "attempt", attempt, "retry_in", delay.Round(time.Millisecond).String(), "resume", out.resume)
			m.update(func(s *Status) {
				now := m.now()
				s.State, s.Presence, s.ConnectedSince = StateReconnecting, "offline", nil
				s.LastError, s.LastErrorAt, s.CloseCode, s.Attempts, s.NextRetryAt = out.err, &now, out.code, attempt, &retryAt
			})
			if !m.wait(ctx, delay) {
				m.update(func(s *Status) { s.State, s.NextRetryAt = StateStopped, nil })
				return
			}
		}
	}
	m.update(func(s *Status) {
		s.State, s.Presence, s.ConnectedSince, s.NextRetryAt = StateStopped, "offline", nil, nil
	})
}

// untilReload makes wait block until Reload or shutdown.
const untilReload = time.Duration(-1)

// wait returns false when ctx ends, after d, or on Reload.
func (m *Manager) wait(ctx context.Context, d time.Duration) bool {
	if d == 0 {
		return ctx.Err() == nil
	}
	var timer <-chan time.Time
	if d > 0 {
		t := time.NewTimer(d)
		defer t.Stop()
		timer = t.C
	}
	select {
	case <-ctx.Done():
		return false
	case <-m.reload:
		return true
	case <-timer:
		return true
	}
}

type inbound struct {
	Op int             `json:"op"`
	D  json.RawMessage `json:"d"`
	S  *int64          `json:"s"`
	T  string          `json:"t"`
}

type outbound struct {
	Op int `json:"op"`
	D  any `json:"d"`
}

type activity struct {
	Name string `json:"name"`
	Type int    `json:"type"`
}

type presence struct {
	Since      *int64     `json:"since"`
	Activities []activity `json:"activities"`
	Status     string     `json:"status"`
	AFK        bool       `json:"afk"`
}

func onlinePresence() presence {
	return presence{Activities: []activity{{Name: ActivityName, Type: activityWatching}}, Status: "online"}
}

type conn struct{ ws *websocket.Conn }

func (c conn) send(op int, d any) error {
	_ = c.ws.SetWriteDeadline(time.Now().Add(writeTimeout))
	return c.ws.WriteJSON(outbound{Op: op, D: d})
}

func (c conn) close(code int, reason string) {
	_ = c.ws.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
	_ = c.ws.Close()
}

// endpoint is the resume URL Discord gave, or the entry point. A resume URL
// must use the entry point's scheme, so a session never downgrades from wss.
func (m *Manager) endpoint(sess *session) string {
	base := m.URL
	if base == "" {
		base = DefaultURL
	}
	if sess.id == "" || sess.resumeURL == "" {
		return base
	}
	u, err := url.Parse(sess.resumeURL)
	b, berr := url.Parse(base)
	if err != nil || berr != nil || u.Host == "" || u.Scheme != b.Scheme {
		return base
	}
	q := u.Query()
	q.Set("v", "10")
	q.Set("encoding", "json")
	u.RawQuery = q.Encode()
	if u.Path == "" {
		u.Path = "/"
	}
	return u.String()
}

func (m *Manager) session(ctx context.Context, token string, sess *session) outcome {
	resuming := sess.id != ""
	dialer := m.Dialer
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	ws, resp, err := dialer.DialContext(dctx, m.endpoint(sess), http.Header{"User-Agent": {userAgent}})
	cancel()
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return outcome{kind: outStopped}
		}
		return outcome{kind: outRetry, resume: resuming, delay: -1, err: "Could not reach the Discord Gateway. Check this server's internet access and DNS (" + shortErr(err) + ")."}
	}
	c := conn{ws: ws}
	defer ws.Close()
	ws.SetReadLimit(maxMessage)

	msgs := make(chan inbound, 16)
	readErr := make(chan error, 1)
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			var p inbound
			if err := ws.ReadJSON(&p); err != nil {
				readErr <- err
				return
			}
			select {
			case msgs <- p:
			case <-stop:
				return
			}
		}
	}()

	var interval time.Duration
	helloWait := time.NewTimer(helloTimeout)
	defer helloWait.Stop()
	for interval == 0 {
		select {
		case <-ctx.Done():
			c.close(websocket.CloseNormalClosure, "shutting down")
			return outcome{kind: outStopped}
		case <-helloWait.C:
			c.close(closeResume, "no hello")
			return outcome{kind: outRetry, resume: resuming, delay: -1, err: "Discord did not start the Gateway session in time."}
		case err := <-readErr:
			return m.closed(ctx, err, resuming, false)
		case p := <-msgs:
			if p.Op != opHello {
				continue
			}
			var hello struct {
				HeartbeatInterval int64 `json:"heartbeat_interval"`
			}
			if json.Unmarshal(p.D, &hello) != nil || hello.HeartbeatInterval <= 0 {
				c.close(closeResume, "bad hello")
				return outcome{kind: outRetry, resume: resuming, delay: -1, err: "Discord sent an unexpected Gateway greeting."}
			}
			interval = time.Duration(hello.HeartbeatInterval) * time.Millisecond
		}
	}

	if resuming {
		err = c.send(opResume, map[string]any{"token": token, "session_id": sess.id, "seq": sess.seq})
	} else {
		err = c.send(opIdentify, map[string]any{
			"token":      token,
			"intents":    0,
			"properties": map[string]string{"os": runtime.GOOS, "browser": "viewdock", "device": "viewdock"},
			"presence":   onlinePresence(),
		})
	}
	if err != nil {
		return outcome{kind: outRetry, resume: resuming, delay: -1, err: "The connection to Discord was lost while signing in (" + shortErr(err) + ")."}
	}

	var (
		connectedAt time.Time
		acked       = true
		sentAt      time.Time
	)
	healthy := func() bool { return !connectedAt.IsZero() && m.now().Sub(connectedAt) >= healthyAfter }
	heartbeat := func() error {
		var seq any
		if sess.hasSeq {
			seq = sess.seq
		}
		sentAt = m.now()
		return c.send(opHeartbeat, seq)
	}
	// The first heartbeat is jittered, as Discord requires.
	beat := time.NewTimer(time.Duration(rand.Int64N(int64(interval)) + 1))
	defer beat.Stop()
	for {
		select {
		case <-ctx.Done():
			c.close(websocket.CloseNormalClosure, "shutting down")
			return outcome{kind: outStopped}
		case <-m.reload:
			if fingerprint(m.token()) != fingerprint(token) {
				c.close(websocket.CloseNormalClosure, "configuration changed")
				return outcome{kind: outReload}
			}
		case <-beat.C:
			if !acked {
				c.close(closeResume, "heartbeat not acknowledged")
				return outcome{kind: outRetry, resume: true, delay: -1, healthy: healthy(), err: "Discord stopped acknowledging heartbeats, so the connection was restarted."}
			}
			acked = false
			if err := heartbeat(); err != nil {
				return outcome{kind: outRetry, resume: true, delay: -1, healthy: healthy(), err: "The connection to Discord was lost (" + shortErr(err) + ")."}
			}
			beat.Reset(interval)
		case err := <-readErr:
			return m.closed(ctx, err, true, healthy())
		case p := <-msgs:
			if p.Op == opDispatch && p.S != nil {
				sess.seq, sess.hasSeq = *p.S, true
			}
			switch p.Op {
			case opHeartbeat:
				if err := heartbeat(); err != nil {
					return outcome{kind: outRetry, resume: true, delay: -1, healthy: healthy(), err: "The connection to Discord was lost (" + shortErr(err) + ")."}
				}
			case opHeartbeatAck:
				acked = true
				if !sentAt.IsZero() {
					latency := m.now().Sub(sentAt).Milliseconds()
					m.update(func(s *Status) { s.LatencyMS = latency })
				}
			case opDispatch:
				switch p.T {
				case "READY":
					var ready struct {
						SessionID        string `json:"session_id"`
						ResumeGatewayURL string `json:"resume_gateway_url"`
						User             struct {
							ID       string `json:"id"`
							Username string `json:"username"`
						} `json:"user"`
					}
					_ = json.Unmarshal(p.D, &ready)
					sess.id, sess.resumeURL = ready.SessionID, ready.ResumeGatewayURL
					connectedAt = m.now()
					m.online(connectedAt, ready.User.ID, ready.User.Username, false)
				case "RESUMED":
					connectedAt = m.now()
					m.online(connectedAt, "", "", true)
				}
			case opReconnect:
				c.close(closeResume, "reconnect requested")
				return outcome{kind: outRetry, resume: true, delay: 0, healthy: true, err: "Discord asked ViewDock to reconnect."}
			case opInvalidSession:
				var resumable bool
				_ = json.Unmarshal(p.D, &resumable)
				c.close(closeResume, "invalid session")
				return outcome{kind: outRetry, resume: resumable, delay: m.invalidWait(), healthy: healthy(), err: "Discord ended the Gateway session; starting a new one."}
			}
		}
	}
}

func (m *Manager) online(at time.Time, id, name string, resumed bool) {
	m.update(func(s *Status) {
		s.State, s.Presence = StateOnline, "online"
		s.ConnectedSince, s.LastConnectedAt = &at, &at
		s.LastError, s.LastErrorAt, s.CloseCode, s.TokenRejected, s.Attempts, s.NextRetryAt = "", nil, 0, false, 0, nil
		if id != "" {
			s.BotID, s.BotName = id, name
		}
	})
	m.Log.Info("discord gateway online", "category", "discord", "bot", m.Status().BotName, "resumed", resumed)
}

// closed maps a read failure to the next step. Discord's close codes are
// documented at https://discord.com/developers/docs/topics/opcodes-and-status-codes.
func (m *Manager) closed(ctx context.Context, err error, resume, healthy bool) outcome {
	if ctx.Err() != nil {
		return outcome{kind: outStopped}
	}
	var ce *websocket.CloseError
	if !errors.As(err, &ce) {
		return outcome{kind: outRetry, resume: resume, delay: -1, healthy: healthy, err: "The connection to Discord was lost (" + shortErr(err) + ")."}
	}
	o := outcome{kind: outRetry, resume: resume, delay: -1, healthy: healthy, code: ce.Code}
	switch ce.Code {
	case 4004:
		o.kind, o.tokenRejected = outFatal, true
		o.err = "Discord rejected the bot token (4004 Authentication failed). It may have been reset or revoked; save the current token from the Bot page."
	case 4010, 4011:
		o.kind = outFatal
		o.err = fmt.Sprintf("Discord requires sharding for this bot (%d). ViewDock keeps one Gateway session, which Discord allows for bots in fewer than 2,500 servers.", ce.Code)
	case 4012:
		o.kind = outFatal
		o.err = "Discord no longer accepts this Gateway version (4012). Update ViewDock."
	case 4013, 4014:
		o.kind = outFatal
		o.err = fmt.Sprintf("Discord refused the Gateway intents (%d). ViewDock requests none; update ViewDock if this persists.", ce.Code)
	case 4007, 4009:
		o.resume = false
		o.err = fmt.Sprintf("Discord ended the Gateway session (%d); starting a new one.", ce.Code)
	case 4008:
		o.err = "Discord rate limited the Gateway connection (4008)."
	default:
		o.err = fmt.Sprintf("Discord closed the Gateway connection (%d%s).", ce.Code, reasonSuffix(ce.Text))
	}
	return o
}

func reasonSuffix(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if len(text) > 120 {
		text = text[:120]
	}
	return " " + text
}

// shortErr keeps network error text readable. Gateway URLs and errors never
// contain the token, which is only sent inside identify and resume payloads.
func shortErr(err error) string {
	s := err.Error()
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}

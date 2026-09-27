package discordbot

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendRoomInviteUsesOfficialBotAuthorization(t *testing.T) {
	var gotAuth, gotBody string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer ts.Close()
	client := New("bot-secret")
	client.BaseURL = ts.URL
	var gotPath string
	ts.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.WriteHeader(http.StatusNoContent)
	})
	if err := client.SendRoomInvite(context.Background(), "112233445566778899", "https://viewdock.example/together/abc", "Friday Cinema"); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bot bot-secret" || !strings.Contains(gotBody, "https://viewdock.example/together/abc") || gotPath != "/channels/112233445566778899/messages" {
		t.Fatalf("request auth/path/body = %q / %q / %q", gotAuth, gotPath, gotBody)
	}
	for _, bad := range []string{"123", "../../users/@me", "112233445566778899/messages?x=", "112233445566778899001"} {
		if err := client.SendRoomInvite(context.Background(), bad, "https://viewdock.example/together/abc", ""); err == nil {
			t.Fatalf("channel id %q accepted", bad)
		}
	}
	if err := client.SendRoomInvite(context.Background(), "112233445566778899", "javascript:alert(1)", ""); err == nil {
		t.Fatal("non-http invite accepted")
	}
}

func TestApplicationAndCommandRegistration(t *testing.T) {
	var calls []string
	var body string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method+" "+r.URL.Path)
		if r.Header.Get("Authorization") != "Bot bot-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/applications/@me":
			_, _ = w.Write([]byte(`{"id":"223344556677889900","name":"ViewDock","verify_key":"abcd"}`))
		case r.Method == http.MethodPut:
			_, _ = w.Write([]byte(`[{"id":"1","name":"party","description":"x"}]`))
		case r.Method == http.MethodPatch:
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"code":50035,"message":"Invalid Form Body"}`))
		}
	}))
	defer ts.Close()
	c := New("bot-secret")
	c.BaseURL = ts.URL
	app, err := c.CurrentApplication(context.Background())
	if err != nil || app.ID != "223344556677889900" || app.VerifyKey != "abcd" {
		t.Fatalf("application = %+v, %v", app, err)
	}
	cmds := []Command{{Name: "party", Description: "x"}}
	out, err := c.OverwriteCommands(context.Background(), app.ID, "", cmds)
	if err != nil || len(out) != 1 {
		t.Fatalf("global overwrite = %v, %v", out, err)
	}
	if _, err := c.OverwriteCommands(context.Background(), app.ID, "112233445566778899", cmds); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, `"name":"party"`) {
		t.Fatalf("command body %q", body)
	}
	want := []string{"GET /applications/@me", "PUT /applications/223344556677889900/commands", "PUT /applications/223344556677889900/guilds/112233445566778899/commands"}
	for i, w := range want {
		if calls[i] != w {
			t.Fatalf("call %d = %q, want %q", i, calls[i], w)
		}
	}
	if _, err := c.OverwriteCommands(context.Background(), "../x", "", cmds); err == nil {
		t.Fatal("invalid application id accepted")
	}
	if _, err := c.OverwriteCommands(context.Background(), app.ID, "../../users", cmds); err == nil {
		t.Fatal("invalid guild id accepted")
	}
	if err := c.SetInteractionsEndpoint(context.Background(), "http://insecure.example/x"); err == nil {
		t.Fatal("non-https endpoint accepted")
	}
	err = c.SetInteractionsEndpoint(context.Background(), "https://viewdock.example/api/v1/integrations/discord/interactions")
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest || !strings.Contains(apiErr.Message, "Invalid Form Body") {
		t.Fatalf("endpoint error = %v", err)
	}
	if strings.Contains(err.Error(), "bot-secret") {
		t.Fatal("error leaks the token")
	}
	var nilClient *Client
	if _, err := nilClient.CurrentApplication(context.Background()); !errors.Is(err, ErrDisabled) {
		t.Fatalf("nil client = %v", err)
	}
}

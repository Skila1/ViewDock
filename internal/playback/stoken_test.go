package playback

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/viewdock/viewdock/internal/auth"
)

func TestStreamTokenAuthorization(t *testing.T) {
	a := &API{}
	owner := &auth.Principal{Kind: "user", UserID: "u1"}
	s := &Session{ID: "s1", Kind: "user", UserID: "u1", Owner: owner, Stoken: "good", StokenExp: time.Now().Add(time.Minute)}
	other := &auth.Principal{Kind: "user", UserID: "u2"}

	cases := []struct {
		name   string
		method string
		query  string
		p      *auth.Principal
		want   bool
	}{
		{"owner cookie read", http.MethodGet, "", owner, true},
		{"owner cookie write", http.MethodPut, "", owner, true},
		{"token only write", http.MethodPut, "?stoken=good", nil, true},
		{"token only read", http.MethodGet, "?stoken=good", nil, true},
		{"bad token read falls back to owner", http.MethodGet, "?stoken=bad", owner, true},
		{"bad token write is refused even for the owner", http.MethodPut, "?stoken=bad", owner, false},
		{"other user", http.MethodPut, "", other, false},
		{"other user with bad token", http.MethodDelete, "?stoken=bad", other, false},
		{"no credentials", http.MethodGet, "", nil, false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "/sessions/s1/progress"+c.query, nil)
		if got := a.authorized(r, s, c.p); got != c.want {
			t.Errorf("%s: authorized = %v, want %v", c.name, got, c.want)
		}
	}

	r := httptest.NewRequest(http.MethodPut, "/sessions/s1/progress?stoken=good", nil)
	if p := a.actor(r, s); p != owner {
		t.Fatal("token request must act as the session owner")
	}
	s.StokenExp = time.Now().Add(-time.Second)
	if a.authorized(httptest.NewRequest(http.MethodGet, "/x?stoken=good", nil), s, nil) {
		t.Fatal("expired token accepted")
	}
}

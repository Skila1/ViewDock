package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
)

func TestLiveWorkerHealthFailureSelectsStandby(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	standby := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	primaryHost, primaryPort := splitTestURL(t, primary.URL)
	standbyHost, standbyPort := splitTestURL(t, standby.URL)
	path := t.TempDir() + "/workers.db"
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := New(sqlDB)
	for _, node := range []Node{
		{ID: "primary", Name: "primary", Host: primaryHost, Port: primaryPort, Scheme: "http", Role: "media-worker", Priority: 10, Weight: 1, Enabled: true},
		{ID: "standby", Name: "standby", Host: standbyHost, Port: standbyPort, Scheme: "http", Role: "media-worker", Priority: 5, Weight: 1, Enabled: true},
	} {
		if _, err := store.Upsert(context.Background(), node); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(store)
	if err := router.Probe(context.Background(), "primary"); err != nil {
		t.Fatal(err)
	}
	if err := router.Probe(context.Background(), "standby"); err != nil {
		t.Fatal(err)
	}
	selected, err := router.Select(context.Background(), RouteRequest{Role: "media-worker"})
	if err != nil || selected.ID != "primary" {
		t.Fatalf("initial live route = %#v, %v", selected, err)
	}
	primary.Close()
	if err := router.Probe(context.Background(), "primary"); err == nil {
		t.Fatal("closed primary health probe unexpectedly succeeded")
	}
	selected, err = router.Select(context.Background(), RouteRequest{Role: "media-worker"})
	if err != nil || selected.ID != "standby" {
		t.Fatalf("standby route after worker failure = %#v, %v", selected, err)
	}
	standby.Close()
}

func splitTestURL(t *testing.T, raw string) (string, int) {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	host, port, ok := strings.Cut(u.Host, ":")
	if !ok {
		t.Fatalf("worker URL has no port: %s", raw)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return host, n
}

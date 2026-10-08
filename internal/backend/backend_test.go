package backend

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/viewdock/viewdock/internal/db"
)

func TestNodeStoreAndRoutes(t *testing.T) {
	path := t.TempDir() + "/viewdock.db"
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()

	st := New(sqlDB)
	node, err := st.Upsert(context.Background(), Node{
		ID:           "node-1",
		Name:         "primary-media",
		Host:         "10.0.0.5",
		Port:         8080,
		Scheme:       "https",
		Role:         "media-worker",
		Region:       "us-east",
		Capabilities: "transcode,storage",
		Priority:     10,
		Weight:       50,
		Capacity:     4,
		Enabled:      true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if node.Status != "unknown" {
		t.Fatalf("default status = %q; want unknown", node.Status)
	}

	if err := st.SetHealth(context.Background(), "node-1", "healthy", 125, "ok"); err != nil {
		t.Fatal(err)
	}
	list, err := st.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != "healthy" || list[0].LatencyMS != 125 {
		t.Fatalf("unexpected list: %#v", list)
	}

	r := chi.NewRouter()
	api := NewAPI(st)
	api.Routes(r)
	req := httptest.NewRequest(http.MethodGet, "/admin/nodes", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthenticated admin status %d; got %d", http.StatusUnauthorized, w.Code)
	}

	if err := st.Delete(context.Background(), "node-1"); err != nil {
		t.Fatal(err)
	}
	if rows, _ := st.List(context.Background()); len(rows) != 0 {
		t.Fatalf("delete failed: %#v", rows)
	}
}

func TestRouterFailsOverToHealthyStandby(t *testing.T) {
	path := t.TempDir() + "/viewdock.db"
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
		{ID: "primary", Name: "primary", Host: "primary", Port: 8080, Role: "media-worker", Priority: 10, Weight: 100, Enabled: true, Status: "healthy"},
		{ID: "standby", Name: "standby", Host: "standby", Port: 8081, Role: "media-worker", Priority: 5, Weight: 100, Enabled: true, Status: "healthy"},
	} {
		if _, err := store.Upsert(context.Background(), node); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(store)
	got, err := router.Select(context.Background(), RouteRequest{Role: "media-worker"})
	if err != nil || got.ID != "primary" {
		t.Fatalf("initial route = %#v, %v", got, err)
	}
	if err := store.SetHealth(context.Background(), "primary", "unhealthy", 10, "connection refused"); err != nil {
		t.Fatal(err)
	}
	router.Invalidate()
	got, err = router.Select(context.Background(), RouteRequest{Role: "media-worker"})
	if err != nil || got.ID != "standby" {
		t.Fatalf("failover route = %#v, %v", got, err)
	}
}

func TestRouterWeightsWithinTierAndSkipsIneligible(t *testing.T) {
	path := t.TempDir() + "/viewdock.db"
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
		{ID: "heavy", Name: "heavy", Host: "heavy", Port: 8080, Role: RoleMediaWorker, Priority: 10, Weight: 3, Enabled: true, Status: "healthy"},
		{ID: "light", Name: "light", Host: "light", Port: 8080, Role: RoleTranscodeWorker, Priority: 10, Weight: 1, Enabled: true, Status: "healthy"},
		{ID: "off", Name: "off", Host: "off", Port: 8080, Role: RoleMediaWorker, Priority: 20, Weight: 1, Enabled: false, Status: "healthy"},
		{ID: "store", Name: "store", Host: "store", Port: 8080, Role: RoleStorageWorker, Priority: 20, Weight: 1, Enabled: true, Status: "healthy"},
	} {
		if _, err := store.Upsert(context.Background(), node); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRouter(store)
	req := RouteRequest{Roles: []string{RoleMediaWorker, RoleTranscodeWorker}}
	counts := map[string]int{}
	for i := 0; i < 8; i++ {
		got, err := router.Select(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		counts[got.ID]++
	}
	if counts["heavy"] != 6 || counts["light"] != 2 || len(counts) != 2 {
		t.Fatalf("weighted selection = %v, want heavy 6 light 2", counts)
	}
	cands, err := router.Candidates(context.Background(), req)
	if err != nil || len(cands) != 2 {
		t.Fatalf("candidates = %#v, %v", cands, err)
	}
}

func TestRouterFiltersRoleCapabilityAndDrain(t *testing.T) {
	path := t.TempDir() + "/viewdock.db"
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	store := New(sqlDB)
	node := Node{ID: "gpu", Name: "gpu", Host: "gpu", Port: 8080, Role: "transcoder", Capabilities: "gpu,h264", Priority: 10, Weight: 1, Enabled: true, Status: "healthy"}
	if _, err := store.Upsert(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	selected, err := NewRouter(store).Select(context.Background(), RouteRequest{Role: "transcoder", Capability: "h264"})
	if err != nil || selected.ID != "gpu" {
		t.Fatalf("capability route = %#v, %v", selected, err)
	}
	node.Draining = true
	if _, err := store.Upsert(context.Background(), node); err != nil {
		t.Fatal(err)
	}
	if _, err := NewRouter(store).Select(context.Background(), RouteRequest{Role: "transcoder", Capability: "h264"}); err != ErrNoHealthyNode {
		t.Fatalf("draining node error = %v, want %v", err, ErrNoHealthyNode)
	}
}

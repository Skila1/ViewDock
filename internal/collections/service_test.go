package collections

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/viewdock/viewdock/internal/db"
	"github.com/viewdock/viewdock/internal/library"
)

func TestGetHidesUngrantedAndRestrictedTitles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.db")
	if err := db.Migrate(path); err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.Open(path, 20000)
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, root_path, content_type, uploads_enabled, created_at, updated_at) VALUES ('l1','A','/a','mixed',0,'t','t'), ('l2','B','/b','mixed',0,'t','t')`,
		`INSERT INTO movies(id, library_id, title, sort_title, created_at, updated_at, rating_age) VALUES ('kids','l1','Kids','kids','t','t',0), ('grim','l1','Grim','grim','t','t',18), ('other','l2','Other','other','t','t',0)`,
		`INSERT INTO users(id, username, password_hash, display_name, email, is_admin, disabled, pin_hash, created_at, updated_at, content_age_limit) VALUES ('child','child','x','C','',0,0,'','t','t',12)`,
	} {
		if _, err := sqlDB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	svc := New(sqlDB, nil)
	ctx := context.Background()
	c, err := svc.Create(ctx, "Mixed")
	if err != nil {
		t.Fatal(err)
	}
	for i, id := range []string{"kids", "grim", "other"} {
		if err := svc.AddItem(ctx, c.ID, "movie", id, i+1); err != nil {
			t.Fatal(err)
		}
	}

	all, err := svc.Get(ctx, c.ID)
	if err != nil || len(all.Items) != 3 {
		t.Fatalf("unrestricted viewer: %+v %v", all.Items, err)
	}
	childCtx := library.WithGrantedIDs(library.WithUserID(ctx, "child"), []string{"l1"})
	got, err := svc.Get(childCtx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 1 || got.Items[0].ItemID != "kids" {
		t.Fatalf("restricted viewer sees %+v", got.Items)
	}
}

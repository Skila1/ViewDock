package storage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLocalStoreLifecycle(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "artwork/movie/a.jpg", strings.NewReader("image"), 5, "image/jpeg"); err != nil {
		t.Fatal(err)
	}
	obj, err := store.Get(context.Background(), "artwork/movie/a.jpg")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(obj.Body)
	closeErr := obj.Body.Close()
	if err != nil || closeErr != nil || string(body) != "image" || obj.Size != 5 {
		t.Fatalf("object body=%q size=%d err=%v", body, obj.Size, err)
	}
	if err := store.Delete(context.Background(), "artwork/movie/a.jpg"); err != nil {
		t.Fatal(err)
	}
}

func TestLocalStoreRejectsTraversalAndSizeMismatch(t *testing.T) {
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), "../escape", strings.NewReader("x"), 1, ""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("traversal error: %v", err)
	}
	if err := store.Put(context.Background(), "file", strings.NewReader("long"), 2, ""); err == nil {
		t.Fatal("expected size mismatch")
	}
}

func TestS3RequiresConfiguration(t *testing.T) {
	if _, err := NewS3(S3Config{}); err == nil {
		t.Fatal("expected missing S3 configuration error")
	}
}

func TestS3EndpointForms(t *testing.T) {
	base := S3Config{Bucket: "b", AccessKey: "a", SecretKey: "s", PathStyle: true}
	for _, ep := range []string{"minio:9000", "http://minio:9000", "https://s3.example.com/"} {
		cfg := base
		cfg.Endpoint = ep
		if _, err := NewS3(cfg); err != nil {
			t.Fatalf("%s: %v", ep, err)
		}
	}
	for _, ep := range []string{"ftp://minio:9000", "http://minio:9000/bucket", "http://user:pw@minio:9000"} {
		cfg := base
		cfg.Endpoint = ep
		if _, err := NewS3(cfg); err == nil {
			t.Fatalf("%s: expected error", ep)
		}
	}
}

func TestLocalListCheckAndNotFound(t *testing.T) {
	ctx := context.Background()
	store, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"b/2.txt", "a/1.txt", "a/sub/3.txt", "ab.txt"} {
		if err := store.Put(ctx, key, strings.NewReader("x"), 1, ""); err != nil {
			t.Fatal(err)
		}
	}
	all, err := store.List(ctx, "")
	if err != nil || len(all) != 4 || all[0].Key != "a/1.txt" {
		t.Fatalf("list all %+v %v", all, err)
	}
	sub, err := store.List(ctx, "a/")
	if err != nil || len(sub) != 2 || sub[1].Key != "a/sub/3.txt" || sub[0].Size != 1 {
		t.Fatalf("list a/ %+v %v", sub, err)
	}
	none, err := store.List(ctx, "missing/")
	if err != nil || len(none) != 0 {
		t.Fatalf("list missing %+v %v", none, err)
	}
	if _, err := store.List(ctx, "../"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("traversal list: %v", err)
	}
	if err := store.Check(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, "a/none.txt"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("not found error: %v", err)
	}
}

func TestPrefixedStore(t *testing.T) {
	ctx := context.Background()
	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WithPrefix(local, "../x"); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("bad prefix: %v", err)
	}
	same, err := WithPrefix(local, " / ")
	if err != nil || same != Store(local) {
		t.Fatalf("empty prefix should return store: %v", err)
	}
	scoped, err := WithPrefix(local, "/viewdock/backups/")
	if err != nil {
		t.Fatal(err)
	}
	if err := local.Put(ctx, "other/file", strings.NewReader("o"), 1, ""); err != nil {
		t.Fatal(err)
	}
	if err := scoped.Put(ctx, "id/manifest.json", strings.NewReader("{}"), 2, "application/json"); err != nil {
		t.Fatal(err)
	}
	obj, err := local.Get(ctx, "viewdock/backups/id/manifest.json")
	if err != nil {
		t.Fatalf("underlying key: %v", err)
	}
	_ = obj.Body.Close()
	items, err := scoped.(Lister).List(ctx, "")
	if err != nil || len(items) != 1 || items[0].Key != "id/manifest.json" {
		t.Fatalf("scoped list %+v %v", items, err)
	}
	if err := scoped.Put(ctx, "../escape", strings.NewReader("x"), 1, ""); !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("scoped traversal: %v", err)
	}
	if err := scoped.Delete(ctx, "id/manifest.json"); err != nil {
		t.Fatal(err)
	}
}

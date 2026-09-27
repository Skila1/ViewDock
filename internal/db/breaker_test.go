package db

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestBreakerCancelsInflightAndFailsFast(t *testing.T) {
	b := newBreaker()
	ctx, done, err := b.guard(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	b.trip()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("in-flight statement was not cancelled on trip")
	}
	if _, _, err := b.guard(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("open breaker admitted a statement: %v", err)
	}
	b.reset()
	ctx2, done2, err := b.guard(context.Background())
	if err != nil {
		t.Fatalf("closed breaker rejected: %v", err)
	}
	done2()
	if ctx2.Err() == nil {
		t.Fatal("done did not release the statement context")
	}
	if b.trips.Load() != 1 {
		t.Fatalf("trips %d", b.trips.Load())
	}
	var nilBreaker *breaker
	if _, release, err := nilBreaker.guard(context.Background()); err != nil {
		t.Fatal(err)
	} else {
		release()
	}
}

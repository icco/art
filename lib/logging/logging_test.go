package logging

import (
	"context"
	"testing"
)

func TestNewAndSync(t *testing.T) {
	l, err := New("info")
	if err != nil || l == nil {
		t.Fatalf("New: %v", err)
	}
	Sync(l) // shouldn't panic
}

func TestInjectFrom(t *testing.T) {
	l, _ := New("info")
	ctx := Inject(context.Background(), l)
	got := From(ctx)
	if got != l {
		t.Fatal("From should return the injected logger")
	}
	// No logger in context falls back to no-op (non-nil).
	if From(context.Background()) == nil {
		t.Fatal("fallback logger should not be nil")
	}
}

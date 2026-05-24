package logging

import (
	"context"
	"testing"
)

func TestNewAndSync(t *testing.T) {
	l, err := New()
	if err != nil || l == nil {
		t.Fatalf("New: %v", err)
	}
	Sync(l) // shouldn't panic
}

func TestInjectFrom(t *testing.T) {
	l, _ := New()
	ctx := Inject(context.Background(), l)
	// gutil.NewContext returns logger.With(...), so we don't compare
	// pointers; we just check that something usable came back.
	if From(ctx) == nil {
		t.Fatal("From should return a non-nil logger")
	}
	if From(context.Background()) == nil {
		t.Fatal("fallback logger should not be nil")
	}
}

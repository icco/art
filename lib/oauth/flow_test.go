package oauth

import (
	"context"
	"net/url"
	"strings"
	"testing"
)

func TestStartURL(t *testing.T) {
	f := NewFlow("cid", "csec", "http://localhost/cb", nil)
	got, err := f.StartURL("personal")
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if u.Query().Get("state") == "" {
		t.Fatal("state missing")
	}
	if u.Query().Get("access_type") != "offline" {
		t.Fatal("offline access not requested")
	}
	if u.Query().Get("prompt") != "consent" {
		t.Fatal("prompt=consent missing")
	}
	if !strings.Contains(u.Query().Get("scope"), "calendar") {
		t.Fatal("scope missing calendar")
	}
}

func TestStartURLBadAccount(t *testing.T) {
	f := NewFlow("cid", "csec", "http://localhost/cb", nil)
	if _, err := f.StartURL("nope"); err == nil {
		t.Fatal("expected error for invalid account")
	}
}

func TestCompleteUnknownState(t *testing.T) {
	f := NewFlow("cid", "csec", "http://localhost/cb", nil)
	if _, _, err := f.Complete(context.Background(), "nope", "code"); err == nil {
		t.Fatal("expected unknown-state error")
	}
}

func TestRandStateUnique(t *testing.T) {
	a, err := randState()
	if err != nil {
		t.Fatal(err)
	}
	b, _ := randState()
	if a == b {
		t.Fatal("randState collision")
	}
	if len(a) < 30 {
		t.Fatalf("state too short: %d", len(a))
	}
}

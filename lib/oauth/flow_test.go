package oauth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
	"golang.org/x/oauth2"
)

// Refresh grants happen per client build today; the source must be cached per
// account, and a rotated refresh token must land in the DB or the next
// restart hits permanent invalid_grant.
func TestTokenSourceCachesAndPersistsRotation(t *testing.T) {
	db := testdb.Open(t)
	sealer, err := NewSealer(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{DB: db, Sealer: sealer}
	f := NewFlow("cid", "csec", "http://localhost/cb", store)

	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"at1","token_type":"Bearer","expires_in":3600,"refresh_token":"r2"}`)
	}))
	defer srv.Close()
	f.OAuth.Endpoint = oauth2.Endpoint{TokenURL: srv.URL}

	ctx := context.Background()
	if err := store.Save(ctx, models.AccountPersonal, "a@b.com", "primary", &oauth2.Token{RefreshToken: "r1"}); err != nil {
		t.Fatal(err)
	}

	ts, _, err := f.TokenSource(ctx, models.AccountPersonal)
	if err != nil {
		t.Fatal(err)
	}
	tok, err := ts.Token()
	if err != nil || tok.AccessToken != "at1" {
		t.Fatalf("token: %v %v", tok, err)
	}

	loaded, _, err := store.Load(ctx, models.AccountPersonal)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RefreshToken != "r2" {
		t.Errorf("rotated refresh token not persisted: %q", loaded.RefreshToken)
	}

	ts2, _, err := f.TokenSource(ctx, models.AccountPersonal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ts2.Token(); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("token endpoint hit %d times; cached source should refresh once", hits)
	}
}

func TestRevokeHitsEndpoint(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		got = r.Form.Get("token")
	}))
	defer srv.Close()

	f := NewFlow("cid", "csec", "http://localhost/cb", nil)
	f.RevokeURL = srv.URL
	f.revoke(context.Background(), "r1")
	if got != "r1" {
		t.Fatalf("revoke endpoint got token %q, want r1", got)
	}
}

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

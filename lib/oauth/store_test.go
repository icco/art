package oauth_test

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"github.com/icco/art/lib/testdb"
	"golang.org/x/oauth2"
	"gorm.io/gorm"
)

func newStore(t *testing.T) *oauth.Store {
	t.Helper()
	db := testdb.Open(t)
	sealer, err := oauth.NewSealer(bytes.Repeat([]byte{0x02}, 32))
	if err != nil {
		t.Fatal(err)
	}
	return &oauth.Store{DB: db, Sealer: sealer}
}

func TestStoreSaveLoad(t *testing.T) {
	s := newStore(t)
	tok := &oauth2.Token{
		AccessToken:  "a",
		RefreshToken: "r",
		Expiry:       time.Now().Add(time.Hour),
	}
	if err := s.Save(context.Background(), models.AccountPersonal, "you@x.com", "primary", tok); err != nil {
		t.Fatal(err)
	}
	got, acct, err := s.Load(context.Background(), models.AccountPersonal)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "r" || acct.Email != "you@x.com" {
		t.Fatalf("roundtrip mismatch: %+v / %+v", got, acct)
	}
}

func TestStoreSaveMissingRefresh(t *testing.T) {
	s := newStore(t)
	err := s.Save(context.Background(), models.AccountPersonal, "you@x.com", "primary", &oauth2.Token{})
	if err == nil {
		t.Fatal("expected error when refresh token missing")
	}
}

func TestStoreLoadMissing(t *testing.T) {
	s := newStore(t)
	_, _, err := s.Load(context.Background(), models.AccountWork)
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected ErrRecordNotFound, got %v", err)
	}
}

func TestStoreAllOrdered(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	tok := &oauth2.Token{RefreshToken: "r"}
	_ = s.Save(ctx, models.AccountPersonal, "p@x.com", "p", tok)
	_ = s.Save(ctx, models.AccountWork, "w@x.com", "w", tok)
	got, err := s.All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 accounts, got %d", len(got))
	}
}

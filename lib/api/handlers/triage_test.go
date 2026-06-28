package handlers_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/icco/art/lib/api/handlers"
	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/testdb"
)

type fakeTriage struct {
	called bool
	err    error
}

func (f *fakeTriage) RunAll(context.Context) error {
	f.called = true
	return f.err
}

func TestTriageRun(t *testing.T) {
	ft := &fakeTriage{}
	h := &handlers.Handlers{Triage: ft}
	r := newRouter(h)

	w := do(t, r, "POST", "/triage/run", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("triage run: %d %s", w.Code, w.Body)
	}
	if !ft.called {
		t.Error("RunAll was not invoked")
	}
}

func TestEmailsList(t *testing.T) {
	db := testdb.Open(t)
	h := &handlers.Handlers{DB: db}
	r := newRouter(h)

	const runID = "00000000-0000-0000-0000-000000000001"
	rows := []models.EmailMessage{
		{RunID: runID, AccountKind: models.AccountWork, GmailMessageID: "w1", Subject: "work", Category: models.EmailArchive, Action: models.ActionArchived},
		{RunID: runID, AccountKind: models.AccountPersonal, GmailMessageID: "p1", Subject: "personal", Category: models.EmailReply, Action: models.ActionReply},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}

	w := do(t, r, "GET", "/emails", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("list: %d", w.Code)
	}
	var got []models.EmailMessage
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil || len(got) != 2 {
		t.Fatalf("list: err=%v len=%d", err, len(got))
	}

	w = do(t, r, "GET", "/emails?account=work", nil)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || len(got) != 1 {
		t.Fatalf("account filter: code=%d len=%d", w.Code, len(got))
	}

	w = do(t, r, "GET", "/emails?account=invalid", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad account should 400: %d", w.Code)
	}
	w = do(t, r, "GET", "/emails?category=nonsense", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad category should 400: %d", w.Code)
	}
}

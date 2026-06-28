package tui

import (
	"strings"
	"testing"
)

func TestRenderDigestEmpty(t *testing.T) {
	a := &App{}
	if got := a.renderDigest(); !strings.Contains(got, "No triaged mail") {
		t.Errorf("empty digest: %q", got)
	}
}

func TestRenderDigest(t *testing.T) {
	a := &App{
		emails: []Email{
			{AccountKind: "work", From: "boss@corp.com", Subject: "Quarterly plan", Summary: "needs your input", Action: "reply", Applied: true},
			{AccountKind: "personal", From: "news@list.com", Subject: "Weekly digest", Action: "archived", Applied: true},
		},
		emailCursor: 0,
	}
	out := a.renderDigest()
	if !strings.Contains(out, "Quarterly plan") || !strings.Contains(out, "Weekly digest") {
		t.Errorf("subjects missing:\n%s", out)
	}
	// The summary shows only for the cursored row.
	if !strings.Contains(out, "needs your input") {
		t.Errorf("cursor-row summary missing:\n%s", out)
	}
}

func TestActionTag(t *testing.T) {
	cases := []struct {
		name string
		e    Email
		want string
	}{
		{"applied reply", Email{Action: "reply", Applied: true}, "reply"},
		{"dry-run proposed", Email{Action: "archived", Applied: false}, "~"},
		{"reversed", Email{Action: "archived", Applied: true, Reversed: true}, "↶"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := actionTag(c.e); !strings.Contains(got, c.want) {
				t.Errorf("tag = %q, want substring %q", got, c.want)
			}
		})
	}
}

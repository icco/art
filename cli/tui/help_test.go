package tui

import (
	"strings"
	"testing"
)

// footerOnPage navigates a fresh root to id and returns its rendered view.
func footerOnPage(id pageID) string {
	root := newRootWithClient(Config{}, nil, false)
	root.width, root.height = 120, 40
	m, _ := root.navigate(id)
	return m.(rootModel).View().Content
}

func TestFooterShowsPageActions(t *testing.T) {
	cases := []struct {
		id   pageID
		want string
	}{
		{pageSettings, "edit"},
		{pageDigest, "archive ↔ inbox"},
		{pageDigest, "mark bad"},
	}
	for _, tc := range cases {
		got := footerOnPage(tc.id)
		if !strings.Contains(got, tc.want) {
			t.Errorf("%s footer missing %q:\n%s", tc.id, tc.want, got)
		}
	}
}

func TestFooterIsContextual(t *testing.T) {
	// The dashboard has no per-page actions, so action hints from other pages
	// must not leak into its footer.
	got := footerOnPage(pageDashboard)
	if strings.Contains(got, "delete") {
		t.Errorf("dashboard footer should not show 'delete':\n%s", got)
	}
}

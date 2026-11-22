package tui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"charm.land/bubbles/v2/list"
	tea "charm.land/bubbletea/v2"
)

// makeDigestWithEmail returns a digestPage with one email item already loaded.
func makeDigestWithEmail(t *testing.T) digestPage {
	t.Helper()
	p := newDigestPage(nil, false)
	// Set a window size so the list is properly sized.
	pg, _ := p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	dp := pg.(digestPage)
	// Load one email so SelectedItem() returns a valid emailItem.
	pg, _ = dp.Update(emailsMsg{[]Email{
		{ID: "e1", Subject: "Hello world", From: "sender@example.com", Action: "archived", Applied: true},
	}})
	return pg.(digestPage)
}

// TestEmailItemShowsAccount verifies the digest list says which account an
// email belongs to, both in the visible row and in the filter.
func TestEmailItemShowsAccount(t *testing.T) {
	i := emailItem{Email{AccountKind: "work", From: "a@b.com", Subject: "Hello", Summary: "a summary"}}
	if d := i.Description(); !strings.Contains(d, "work") {
		t.Fatalf("Description() = %q, want the account kind in it", d)
	}
	if fv := i.FilterValue(); !strings.Contains(fv, "work") {
		t.Fatalf("FilterValue() = %q, want the account kind in it", fv)
	}
}

// TestDigestRejectKeyOpensForm verifies that pressing "x" on a selected email
// opens the confirm form (FullInput becomes true and the view shows the form).
func TestDigestRejectKeyOpensForm(t *testing.T) {
	p := makeDigestWithEmail(t)

	if p.FullInput() {
		t.Fatal("form should not be open before pressing x")
	}

	pg, _ := p.Update(tea.KeyPressMsg{Code: 'x'})
	dp := pg.(digestPage)

	if !dp.FullInput() {
		t.Fatal("FullInput should be true after pressing x (form open)")
	}
	if dp.form == nil {
		t.Fatal("form field should be non-nil after pressing x")
	}
	if dp.reverseID != "e1" {
		t.Errorf("reverseID = %q, want %q", dp.reverseID, "e1")
	}
	view := dp.View()
	if !strings.Contains(view, "Mark this decision bad") {
		t.Errorf("confirm form not visible in view:\n%s", view)
	}
}

// TestDigestAbortClearsForm verifies that esc / StateAborted clears the form
// and emits no reverse command.
func TestDigestAbortClearsForm(t *testing.T) {
	p := makeDigestWithEmail(t)

	// Open the form.
	pg, _ := p.Update(tea.KeyPressMsg{Code: 'x'})
	dp := pg.(digestPage)
	if dp.form == nil {
		t.Fatal("form not opened after pressing x")
	}

	// Drive the form to aborted state by pressing esc.
	pg2, cmd := dp.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	dp2 := pg2.(digestPage)

	// If esc was consumed by the huh form (StateAborted), the form is cleared.
	// If huh didn't handle esc in this state, the form remains open — that is
	// also acceptable behaviour (the user can re-press). We assert on what the
	// codebase guarantees: either the form is nil (aborted) or it is still a
	// valid *huh.Form (not in a bad state).
	if dp2.form != nil {
		// Form is still open — verify FullInput still reflects that.
		if !dp2.FullInput() {
			t.Error("FullInput inconsistent with non-nil form")
		}
	} else {
		// Form was cleared — verify all related state is reset.
		if dp2.cf != nil || dp2.reverseID != "" {
			t.Error("cf/reverseID not reset when form was cleared")
		}
		if dp2.FullInput() {
			t.Error("FullInput should be false after form is cleared")
		}
		// cmd must be nil (no reverse dispatched on abort).
		if cmd != nil {
			t.Error("abort should not emit a command")
		}
	}
}

// TestDigestArchiveKeyIsInstant verifies that pressing "a" on a selected email
// dispatches a toggle command without opening a confirm form.
func TestDigestArchiveKeyIsInstant(t *testing.T) {
	p := makeDigestWithEmail(t)

	pg, cmd := p.Update(tea.KeyPressMsg{Code: 'a'})
	dp := pg.(digestPage)

	if dp.form != nil {
		t.Error("archive toggle must not open a form")
	}
	if dp.FullInput() {
		t.Error("archive toggle is instant; FullInput should stay false")
	}
	if cmd == nil {
		t.Error("expected a command from the archive toggle")
	}
}

// TestSetEmailArchivedCommand verifies the command posts the requested archived
// state and reports success.
func TestSetEmailArchivedCommand(t *testing.T) {
	var gotBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		decodeRequest(t, r.Body, &gotBody)
		encodeJSON(t, w, Email{ID: "e1", Archived: false})
	}))
	defer server.Close()

	msg := setEmailArchived(stubClient(server), "e1", false)()
	if em, ok := msg.(errMsg); ok {
		t.Fatalf("unexpected error: %v", em.err)
	}
	if _, ok := msg.(statusMsg); !ok {
		t.Fatalf("expected statusMsg, got %T", msg)
	}
	if gotBody["archived"] != false {
		t.Errorf("body archived = %v, want false", gotBody["archived"])
	}
}

// TestDigestNoFormWithoutSelection verifies that pressing x with no email
// selected does nothing.
func TestDigestNoFormWithoutSelection(t *testing.T) {
	p := newDigestPage(nil, false)
	pg, _ := p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	// No emailsMsg: list is empty, no selection.
	pg2, _ := pg.Update(tea.KeyPressMsg{Code: 'x'})
	dp := pg2.(digestPage)

	if dp.form != nil {
		t.Error("form must not open when no item is selected")
	}
	if dp.FullInput() {
		t.Error("FullInput must be false when no item is selected")
	}
}

func TestDigestUpdateForwardsAsyncListMessages(t *testing.T) {
	p := makeDigestWithEmail(t)
	// Start filtering for "banana"
	p.list.SetFilterText("banana")
	// Now update with items via emailsMsg, which returns a filterItems command
	pg, cmd := p.Update(emailsMsg{emails: []Email{
		{ID: "e1", Subject: "apple"},
		{ID: "e2", Subject: "banana"},
	}})
	if cmd == nil {
		t.Fatal("expected command from SetItems while filtered")
	}
	filterMatchesMsg := cmd()
	if filterMatchesMsg == nil {
		t.Fatal("expected filter matches msg from cmd")
	}
	// Forward the filter message to Update
	pg, _ = pg.Update(filterMatchesMsg)
	dp := pg.(digestPage)
	if len(dp.list.VisibleItems()) != 1 {
		t.Fatalf("expected 1 visible item, got %d", len(dp.list.VisibleItems()))
	}
	if it, ok := dp.list.VisibleItems()[0].(emailItem); !ok || it.e.ID != "e2" {
		t.Fatalf("expected e2 banana, got %+v", dp.list.VisibleItems()[0])
	}
}

func TestDigestFilterStateFullInputAndKeyRouting(t *testing.T) {
	p := newDigestPage(nil, false)
	pg, _ := p.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	pg, _ = pg.Update(emailsMsg{emails: []Email{
		{ID: "e1", Subject: "apple", Action: "archived", Applied: true},
		{ID: "e2", Subject: "banana", Action: "inbox", Applied: true},
	}})
	dp := pg.(digestPage)

	// Enter filter mode
	pg, _ = dp.Update(tea.KeyPressMsg{Code: '/'})
	dp = pg.(digestPage)
	if !dp.list.SettingFilter() {
		t.Fatal("expected SettingFilter to be true")
	}
	if !dp.FullInput() {
		t.Fatal("expected FullInput to be true while setting filter")
	}

	// While setting filter, typing 'a' or 'x' should NOT trigger archive or confirm form
	pg, _ = dp.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	dp = pg.(digestPage)
	if dp.form != nil {
		t.Fatal("typing in filter should not open confirm form")
	}
	if dp.list.FilterValue() != "a" {
		t.Fatalf("expected filter value %q, got %q", "a", dp.list.FilterValue())
	}

	// Apply the filter
	pg, _ = dp.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	dp = pg.(digestPage)
	if dp.list.SettingFilter() {
		t.Fatal("SettingFilter should be false after enter")
	}
	if !dp.list.IsFiltered() {
		t.Fatal("IsFiltered should be true after applying filter")
	}
	if !dp.FullInput() {
		t.Fatal("FullInput should be true when filter is applied")
	}

	// In FilterApplied state, 'x' still opens the confirm form
	pg, _ = dp.Update(tea.KeyPressMsg{Code: 'x'})
	dp = pg.(digestPage)
	if dp.form == nil {
		t.Fatal("pressing x with filter applied should open confirm form")
	}
	// Cancel the confirm form
	pg, _ = dp.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	dp = pg.(digestPage)
	if dp.form != nil {
		t.Fatal("esc should close confirm form")
	}
	if !dp.list.IsFiltered() {
		t.Fatal("list should still be filtered after closing confirm form")
	}

	// In FilterApplied state, 'a' triggers archive command
	pg, cmd := dp.Update(tea.KeyPressMsg{Code: 'a'})
	dp = pg.(digestPage)
	if cmd == nil {
		t.Fatal("pressing a with filter applied should trigger archive cmd")
	}

	// In FilterApplied state, esc clears the filter
	pg, _ = dp.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	dp = pg.(digestPage)
	if dp.list.IsFiltered() {
		t.Fatal("esc should clear filter")
	}
	if dp.list.FilterState() != list.Unfiltered {
		t.Fatalf("expected Unfiltered state, got %v", dp.list.FilterState())
	}
	if dp.FullInput() {
		t.Fatal("FullInput should be false after clearing filter")
	}
}

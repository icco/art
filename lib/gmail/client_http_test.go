package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// writeJSON encodes a fake Gmail response, failing the test on error.
func writeJSON(t *testing.T, w http.ResponseWriter, v any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		t.Errorf("encode fake response: %v", err)
	}
}

// fakeGmailServer routes the handful of Gmail endpoints the triager uses to
// canned responses, so the API-calling Client methods can be exercised without
// real credentials.
func fakeGmailServer(t *testing.T) *httptest.Server {
	t.Helper()
	write := func(w http.ResponseWriter, v any) { writeJSON(t, w, v) }
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/modify"):
			write(w, &gmail.Message{Id: "m1"})
		case strings.HasSuffix(p, "/labels") && r.Method == http.MethodGet:
			write(w, &gmail.ListLabelsResponse{Labels: []*gmail.Label{{Id: "L_TRIAGED", Name: LabelTriaged}}})
		case strings.HasSuffix(p, "/labels") && r.Method == http.MethodPost:
			var in gmail.Label
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Errorf("decode label request: %v", err)
			}
			write(w, &gmail.Label{Id: "NEW_" + in.Name, Name: in.Name})
		case strings.HasSuffix(p, "/messages"):
			write(w, &gmail.ListMessagesResponse{Messages: []*gmail.Message{{Id: "m1"}, {Id: "m2"}}})
		case strings.Contains(p, "/messages/"):
			write(w, &gmail.Message{
				Id: "m1", ThreadId: "t1", Snippet: "snip", InternalDate: 1700000000000,
				LabelIds: []string{InboxLabel, "UNREAD"},
				Payload: &gmail.MessagePart{
					MimeType: "text/plain",
					Headers: []*gmail.MessagePartHeader{
						{Name: "From", Value: "a@b.com"},
						{Name: "Subject", Value: "Hi"},
						{Name: "Message-ID", Value: "<mid-1>"},
					},
					Body: &gmail.MessagePartBody{Data: base64.URLEncoding.EncodeToString([]byte("body text"))},
				},
			})
		default:
			http.Error(w, "unexpected "+r.Method+" "+p, http.StatusNotFound)
		}
	}))
}

func testClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	svc, err := gmail.NewService(context.Background(),
		option.WithEndpoint(srv.URL+"/"),
		option.WithHTTPClient(srv.Client()))
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return &Client{svc: svc}
}

func TestEnsureLabelsCaseInsensitive(t *testing.T) {
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/labels") && r.Method == http.MethodGet:
			writeJSON(t, w, &gmail.ListLabelsResponse{Labels: []*gmail.Label{
				{Id: "L1", Name: "art/triaged"},
				{Id: "L2", Name: "ART/Archived"},
				{Id: "L3", Name: LabelReply},
			}})
		case strings.HasSuffix(r.URL.Path, "/labels") && r.Method == http.MethodPost:
			creates++
			http.Error(w, `{"error": {"code": 409}}`, http.StatusConflict)
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	labels, err := testClient(t, srv).EnsureLabels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if creates != 0 {
		t.Errorf("created %d labels that already existed", creates)
	}
	if labels[LabelTriaged] != "L1" || labels[LabelArchived] != "L2" || labels[LabelReply] != "L3" {
		t.Errorf("labels = %v", labels)
	}
}

func TestEnsureLabelsCreateConflict(t *testing.T) {
	lists := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/labels") && r.Method == http.MethodGet:
			lists++
			resp := &gmail.ListLabelsResponse{}
			if lists > 1 {
				resp.Labels = []*gmail.Label{
					{Id: "L1", Name: LabelTriaged},
					{Id: "L2", Name: LabelArchived},
					{Id: "L3", Name: LabelReply},
				}
			}
			writeJSON(t, w, resp)
		case strings.HasSuffix(r.URL.Path, "/labels") && r.Method == http.MethodPost:
			http.Error(w, `{"error": {"code": 409, "message": "Label name exists or conflicts"}}`, http.StatusConflict)
		default:
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
		}
	}))
	defer srv.Close()

	labels, err := testClient(t, srv).EnsureLabels(context.Background())
	if err != nil {
		t.Fatalf("409 should resolve via re-list: %v", err)
	}
	if labels[LabelTriaged] != "L1" {
		t.Errorf("labels = %v", labels)
	}
}

func htmlMessageServer(t *testing.T, html string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/messages/") {
			http.Error(w, "unexpected "+r.URL.Path, http.StatusNotFound)
			return
		}
		writeJSON(t, w, &gmail.Message{
			Id: "m1", InternalDate: 1700000000000,
			Payload: &gmail.MessagePart{
				MimeType: "text/html",
				Body:     &gmail.MessagePartBody{Data: base64.URLEncoding.EncodeToString([]byte(html))},
			},
		})
	}))
}

// HTML-only mail must not feed CSS/JS to the classifier, and entities decode.
func TestGetMessageHTMLBody(t *testing.T) {
	srv := htmlMessageServer(t, `<html><head><style>.x{color:red}</style></head><body><p>Hello&nbsp;world &amp; more</p><script>var evil=1</script></body></html>`)
	defer srv.Close()

	msg, err := testClient(t, srv).GetMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.Body != "Hello world & more" {
		t.Errorf("body = %q", msg.Body)
	}
}

func TestGetMessageBodyRuneSafeTruncation(t *testing.T) {
	srv := htmlMessageServer(t, "<p>"+strings.Repeat("€", 3000)+"</p>")
	defer srv.Close()

	msg, err := testClient(t, srv).GetMessage(context.Background(), "m1")
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Body) > 4000 {
		t.Errorf("body not truncated: %d bytes", len(msg.Body))
	}
	if !utf8.ValidString(msg.Body) {
		t.Error("truncation split a UTF-8 rune")
	}
}

func TestClientMethods(t *testing.T) {
	srv := fakeGmailServer(t)
	defer srv.Close()
	c := testClient(t, srv)
	ctx := context.Background()

	labels, err := c.EnsureLabels(ctx)
	if err != nil || len(labels) != len(ArtLabels) {
		t.Fatalf("EnsureLabels: err=%v labels=%v", err, labels)
	}
	if labels[LabelTriaged] != "L_TRIAGED" {
		t.Errorf("existing label id = %q, want L_TRIAGED", labels[LabelTriaged])
	}

	ids, err := c.FetchMessageIDs(ctx, "in:inbox", 10)
	if err != nil || len(ids) != 2 {
		t.Fatalf("FetchMessageIDs: err=%v ids=%v", err, ids)
	}

	msg, err := c.GetMessage(ctx, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if msg.From != "a@b.com" || msg.Subject != "Hi" || msg.Body != "body text" {
		t.Errorf("GetMessage parsed wrong: %+v", msg)
	}
	if msg.ReceivedAt.IsZero() {
		t.Error("ReceivedAt not set from InternalDate")
	}

	if err := c.ModifyLabels(ctx, "m1", []string{"L_TRIAGED"}, []string{InboxLabel}); err != nil {
		t.Errorf("ModifyLabels: %v", err)
	}
	if err := c.ModifyLabels(ctx, "m1", nil, nil); err != nil {
		t.Errorf("ModifyLabels no-op: %v", err)
	}
}

func TestThreadHasSentMessage(t *testing.T) {
	cases := []struct {
		name     string
		messages []*gmail.Message
		want     bool
	}{
		{"new mail", []*gmail.Message{{Id: "incoming", LabelIds: []string{InboxLabel}}}, false},
		{"received conversation", []*gmail.Message{
			{Id: "archived", LabelIds: []string{"UNREAD"}},
			{Id: "incoming", LabelIds: []string{InboxLabel}},
		}, false},
		{"reply to sent mail", []*gmail.Message{
			{Id: "incoming", LabelIds: []string{InboxLabel}},
			{Id: "sent-from-alias", LabelIds: []string{"SENT"}},
		}, true},
		{"sent copy in inbox", []*gmail.Message{{Id: "self", LabelIds: []string{InboxLabel, "SENT"}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/gmail/v1/users/me/threads/t1" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				if r.URL.Query().Get("format") != "minimal" || r.URL.Query().Get("fields") != "messages(id,labelIds)" {
					t.Errorf("must fetch only message IDs and labels: %s", r.URL.RawQuery)
				}
				writeJSON(t, w, &gmail.Thread{Id: "t1", Messages: tc.messages})
			}))
			defer srv.Close()
			got, err := testClient(t, srv).ThreadHasSentMessage(context.Background(), "t1")
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestThreadHasSentMessageFailure(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer srv.Close()
	c := testClient(t, srv)
	if _, err := c.ThreadHasSentMessage(context.Background(), ""); err == nil {
		t.Fatal("missing thread must fail closed")
	}
	if requests != 0 {
		t.Fatal("missing thread should not call Gmail")
	}
	if _, err := c.ThreadHasSentMessage(context.Background(), "t1"); err == nil {
		t.Fatal("API failure must propagate so triage cannot archive")
	}
}

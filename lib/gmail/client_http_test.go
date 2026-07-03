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

// fakeGmailServer routes the handful of Gmail endpoints the triager uses to
// canned responses, so the API-calling Client methods can be exercised without
// real credentials.
func fakeGmailServer(t *testing.T) *httptest.Server {
	t.Helper()
	write := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.HasSuffix(p, "/modify"):
			write(w, &gmail.Message{Id: "m1"})
		case strings.HasSuffix(p, "/labels") && r.Method == http.MethodGet:
			write(w, &gmail.ListLabelsResponse{Labels: []*gmail.Label{{Id: "L_TRIAGED", Name: LabelTriaged}}})
		case strings.HasSuffix(p, "/labels") && r.Method == http.MethodPost:
			var in gmail.Label
			_ = json.NewDecoder(r.Body).Decode(&in)
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

// Gmail label names are case-insensitively unique: an existing "art/triaged"
// must be reused, not re-created (which would 409 on every run forever).
func TestEnsureLabelsCaseInsensitive(t *testing.T) {
	creates := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/labels") && r.Method == http.MethodGet:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(&gmail.ListLabelsResponse{Labels: []*gmail.Label{
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

// A 409 on create (concurrent creation) must resolve by re-listing, not fail
// the whole run.
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
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
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
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&gmail.Message{
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

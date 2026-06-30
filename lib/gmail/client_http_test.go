package gmail

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	return &Client{Service: svc}
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
	if msg.From != "a@b.com" || msg.Subject != "Hi" || msg.Body != "body text" || msg.MessageIDHeader != "<mid-1>" {
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

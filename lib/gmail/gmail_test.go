package gmail

import (
	"encoding/base64"
	"testing"

	"google.golang.org/api/gmail/v1"
)

func TestDecodeBody(t *testing.T) {
	if got := decodeBody(base64.URLEncoding.EncodeToString([]byte("hello world"))); got != "hello world" {
		t.Errorf("padded: got %q", got)
	}
	if got := decodeBody(base64.RawURLEncoding.EncodeToString([]byte("no padding"))); got != "no padding" {
		t.Errorf("raw: got %q", got)
	}
	if got := decodeBody("!!! not base64 !!!"); got != "" {
		t.Errorf("garbage should decode to empty, got %q", got)
	}
}

func TestHTMLToText(t *testing.T) {
	if got := htmlToText("<p>hi <b>there</b></p>"); got != "hi there" {
		t.Errorf("got %q", got)
	}
	if got := htmlToText("  plain  "); got != "plain" {
		t.Errorf("trim: got %q", got)
	}
}

func TestExtractBody(t *testing.T) {
	enc := func(s string) string { return base64.URLEncoding.EncodeToString([]byte(s)) }

	multipart := &gmail.MessagePart{
		MimeType: "multipart/alternative",
		Parts: []*gmail.MessagePart{
			{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: enc("<p>html</p>")}},
			{MimeType: "text/plain; charset=UTF-8", Body: &gmail.MessagePartBody{Data: enc("plain body")}},
		},
	}
	if got := extractBody(multipart); got != "plain body" {
		t.Errorf("prefers text/plain, got %q", got)
	}

	htmlOnly := &gmail.MessagePart{MimeType: "text/html", Body: &gmail.MessagePartBody{Data: enc("<p>just html</p>")}}
	if got := extractBody(htmlOnly); got != "just html" {
		t.Errorf("html fallback, got %q", got)
	}

	if got := extractBody(nil); got != "" {
		t.Errorf("nil part, got %q", got)
	}
}

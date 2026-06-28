package gmail

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
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

func TestStripTags(t *testing.T) {
	if got := stripTags("<p>hi <b>there</b></p>"); got != "hi there" {
		t.Errorf("got %q", got)
	}
	if got := stripTags("  plain  "); got != "plain" {
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

func TestBuildReply(t *testing.T) {
	out := buildReply(DraftInput{To: "a@b.com", Subject: "Hello", Body: "hi there", InReplyTo: "<msg-1>"})
	for _, want := range []string{"To: a@b.com", "Subject: Re: Hello", "In-Reply-To: <msg-1>", "References: <msg-1>", "hi there"} {
		if !strings.Contains(out, want) {
			t.Errorf("reply missing %q in:\n%s", want, out)
		}
	}
	// An existing Re: prefix is not duplicated.
	if got := buildReply(DraftInput{Subject: "Re: Hello"}); strings.Count(got, "Re:") != 1 {
		t.Errorf("Re: prefix duplicated:\n%s", got)
	}
}

func TestIsNotFound(t *testing.T) {
	if !isNotFound(&googleapi.Error{Code: 404}) {
		t.Error("404 should be not-found")
	}
	if isNotFound(&googleapi.Error{Code: 500}) {
		t.Error("500 should not be not-found")
	}
	if isNotFound(errors.New("plain")) {
		t.Error("plain error should not be not-found")
	}
}

package email

import (
	"slices"
	"strings"
	"testing"

	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"google.golang.org/genai"
)

func TestUserPrompt(t *testing.T) {
	m := &gmail.Message{From: "a@b.com", To: "me@x.com", Subject: "Hi", Snippet: "snip", Body: "the body"}
	p := userPrompt(m)
	for _, want := range []string{"From: a@b.com", "Subject: Hi", "the body"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q in:\n%s", want, p)
		}
	}
	// Falls back to the snippet when there is no body.
	if p := userPrompt(&gmail.Message{Snippet: "only snippet"}); !strings.Contains(p, "only snippet") {
		t.Errorf("expected snippet fallback in:\n%s", p)
	}
}

// An out-of-range confidence (e.g. percent-style 85) previously overflowed the
// numeric(4,3) column, and the persist error aborted the whole account's run.
func TestParseClassification(t *testing.T) {
	good, err := parseClassification(`{"category":"keep","summary":"s","reason":"r","confidence":0.9}`)
	if err != nil || good.Confidence != 0.9 {
		t.Fatalf("valid classification rejected: %v", err)
	}
	for _, bad := range []string{
		`{"category":"keep","summary":"s","reason":"r","confidence":85}`,
		`{"category":"keep","summary":"s","reason":"r","confidence":-0.1}`,
		`{"category":"burn","summary":"s","reason":"r","confidence":0.5}`,
		`not json`,
	} {
		if _, err := parseClassification(bad); err == nil {
			t.Errorf("parseClassification(%q) should fail", bad)
		}
	}
}

func TestClassificationSchema(t *testing.T) {
	s := classificationSchema()
	if s.Type != genai.TypeObject {
		t.Fatalf("type = %v", s.Type)
	}
	cat := s.Properties["category"]
	if cat == nil {
		t.Fatal("missing category property")
	}
	for _, want := range []string{
		string(models.EmailArchive), string(models.EmailReply), string(models.EmailKeep),
	} {
		if !slices.Contains(cat.Enum, want) {
			t.Errorf("category enum missing %q", want)
		}
	}
	if !slices.Contains(s.Required, "category") {
		t.Error("category should be required")
	}
}

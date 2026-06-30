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

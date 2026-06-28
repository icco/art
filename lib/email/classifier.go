// Package email implements art's Gmail triage: classify new inbox mail with
// Gemini, apply reversible actions, record an audit trail, and learn from the
// corrections Nat makes.
package email

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icco/art/lib/config"
	"github.com/icco/art/lib/gmail"
	"github.com/icco/art/lib/models"
	"google.golang.org/genai"
)

//go:embed prompt.md
var systemInstruction string

// Classification is the structured output the model returns per message.
type Classification struct {
	Category   models.EmailCategory `json:"category"`
	Summary    string               `json:"summary"`
	DraftReply string               `json:"draft_reply"`
	Reason     string               `json:"reason"`
	Confidence float64              `json:"confidence"`
}

// Classifier calls Gemini with structured output to triage one message at a
// time. corrections holds a feedback block (from the reconcile pass) appended
// to the system instruction so the model learns from Nat's reversals.
type Classifier struct {
	client      *genai.Client
	model       string
	corrections string

	tokensIn  int
	tokensOut int
}

// NewClassifier builds a Gemini client on the Vertex backend, mirroring the
// planner's configuration.
func NewClassifier(ctx context.Context, cfg *config.Config, corrections string) (*Classifier, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  cfg.Vertex.ProjectID,
		Location: cfg.Vertex.Location,
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return nil, fmt.Errorf("genai client: %w", err)
	}
	return &Classifier{client: client, model: config.VertexModel, corrections: corrections}, nil
}

// TokensIn reports cumulative prompt tokens across all Classify calls.
func (c *Classifier) TokensIn() int { return c.tokensIn }

// TokensOut reports cumulative output tokens across all Classify calls.
func (c *Classifier) TokensOut() int { return c.tokensOut }

// Classify returns the model's triage decision for a single message.
func (c *Classifier) Classify(ctx context.Context, m *gmail.Message) (Classification, error) {
	resp, err := c.client.Models.GenerateContent(ctx, c.model, genai.Text(userPrompt(m)), &genai.GenerateContentConfig{
		SystemInstruction: &genai.Content{Parts: []*genai.Part{{Text: systemInstruction + c.corrections}}},
		ResponseMIMEType:  "application/json",
		ResponseSchema:    classificationSchema(),
	})
	if err != nil {
		return Classification{}, err
	}
	if resp.UsageMetadata != nil {
		c.tokensIn += int(resp.UsageMetadata.PromptTokenCount)
		c.tokensOut += int(resp.UsageMetadata.CandidatesTokenCount)
	}

	var out Classification
	if err := json.Unmarshal([]byte(resp.Text()), &out); err != nil {
		return Classification{}, fmt.Errorf("decode classification: %w", err)
	}
	if !out.Category.Valid() {
		return Classification{}, fmt.Errorf("model returned invalid category %q", out.Category)
	}
	// draft_reply is only meaningful for replies.
	if out.Category != models.EmailReply {
		out.DraftReply = ""
	}
	return out, nil
}

func userPrompt(m *gmail.Message) string {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\n", m.From)
	fmt.Fprintf(&b, "To: %s\n", m.To)
	fmt.Fprintf(&b, "Subject: %s\n", m.Subject)
	fmt.Fprintf(&b, "Received: %s\n", m.ReceivedAt.Format("2006-01-02 15:04 MST"))
	if m.Snippet != "" {
		fmt.Fprintf(&b, "Snippet: %s\n", m.Snippet)
	}
	b.WriteString("\nBody:\n")
	if strings.TrimSpace(m.Body) != "" {
		b.WriteString(m.Body)
	} else {
		b.WriteString(m.Snippet)
	}
	return b.String()
}

func classificationSchema() *genai.Schema {
	return &genai.Schema{
		Type: genai.TypeObject,
		Properties: map[string]*genai.Schema{
			"category": {
				Type: genai.TypeString,
				Enum: []string{
					string(models.EmailArchive),
					string(models.EmailReply),
					string(models.EmailRead),
					string(models.EmailThinking),
					string(models.EmailKeep),
				},
			},
			"summary":     {Type: genai.TypeString},
			"draft_reply": {Type: genai.TypeString},
			"reason":      {Type: genai.TypeString},
			"confidence":  {Type: genai.TypeNumber},
		},
		Required: []string{"category", "summary", "reason", "confidence"},
	}
}

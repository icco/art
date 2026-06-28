package gmail

import (
	"context"
	"encoding/base64"
	"strings"
	"time"

	"google.golang.org/api/gmail/v1"
)

// maxBodyChars bounds how much plaintext we feed the classifier per message.
const maxBodyChars = 4000

// Message is the extracted, classifier-ready view of a Gmail message. Bodies
// are held only in memory for the duration of a run; we never persist them.
type Message struct {
	ID         string
	ThreadID   string
	From       string
	To         string
	Subject    string
	Snippet    string
	Body       string
	ReceivedAt time.Time
	LabelIDs   []string
	// MessageIDHeader is the RFC822 Message-ID, used to thread draft replies.
	MessageIDHeader string
}

// FetchMessageIDs returns up to max message IDs matching the Gmail search
// query (e.g. "in:inbox -label:Art/Triaged newer_than:14d"), following
// pagination as needed.
func (c *Client) FetchMessageIDs(ctx context.Context, query string, limit int) ([]string, error) {
	var ids []string
	pageToken := ""
	for len(ids) < limit {
		call := c.Service.Users.Messages.List(User).Q(query).Context(ctx)
		remaining := min(int64(limit-len(ids)), 500)
		call = call.MaxResults(remaining)
		if pageToken != "" {
			call = call.PageToken(pageToken)
		}
		resp, err := call.Do()
		if err != nil {
			return nil, err
		}
		for _, m := range resp.Messages {
			ids = append(ids, m.Id)
		}
		if resp.NextPageToken == "" {
			break
		}
		pageToken = resp.NextPageToken
	}
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return ids, nil
}

// GetMessage fetches a single message in full and extracts the fields the
// triager needs. ReceivedAt comes from InternalDate (epoch ms), which is more
// reliable than parsing the Date header.
func (c *Client) GetMessage(ctx context.Context, id string) (*Message, error) {
	m, err := c.Service.Users.Messages.Get(User, id).Format("full").Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	out := &Message{
		ID:       m.Id,
		ThreadID: m.ThreadId,
		Snippet:  m.Snippet,
		LabelIDs: m.LabelIds,
	}
	if m.InternalDate > 0 {
		out.ReceivedAt = time.UnixMilli(m.InternalDate).UTC()
	}
	if m.Payload != nil {
		for _, h := range m.Payload.Headers {
			switch strings.ToLower(h.Name) {
			case "from":
				out.From = h.Value
			case "to":
				out.To = h.Value
			case "subject":
				out.Subject = h.Value
			case "message-id":
				out.MessageIDHeader = h.Value
			}
		}
		out.Body = extractBody(m.Payload)
	}
	if len(out.Body) > maxBodyChars {
		out.Body = out.Body[:maxBodyChars]
	}
	return out, nil
}

// extractBody walks the MIME tree preferring text/plain, falling back to a
// crudely de-tagged text/html part.
func extractBody(part *gmail.MessagePart) string {
	if plain := findPart(part, "text/plain"); plain != "" {
		return plain
	}
	if html := findPart(part, "text/html"); html != "" {
		return stripTags(html)
	}
	return ""
}

func findPart(part *gmail.MessagePart, mime string) string {
	if part == nil {
		return ""
	}
	if strings.HasPrefix(strings.ToLower(part.MimeType), mime) && part.Body != nil && part.Body.Data != "" {
		return decodeBody(part.Body.Data)
	}
	for _, p := range part.Parts {
		if got := findPart(p, mime); got != "" {
			return got
		}
	}
	return ""
}

// decodeBody decodes Gmail's base64url body data, tolerating missing padding.
func decodeBody(data string) string {
	if b, err := base64.URLEncoding.DecodeString(data); err == nil {
		return string(b)
	}
	if b, err := base64.RawURLEncoding.DecodeString(data); err == nil {
		return string(b)
	}
	return ""
}

// stripTags removes HTML tags for a rough plaintext approximation. The result
// only ever feeds the classifier, so fidelity is not important.
func stripTags(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return strings.TrimSpace(b.String())
}

package gmail

import (
	"context"
	"encoding/base64"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/net/html"
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
}

// FetchMessageIDs returns up to max message IDs matching the Gmail search
// query (e.g. "in:inbox -label:Art/Triaged newer_than:14d"), following
// pagination as needed.
func (c *Client) FetchMessageIDs(ctx context.Context, query string, limit int) ([]string, error) {
	var ids []string
	pageToken := ""
	for len(ids) < limit {
		call := c.svc.Users.Messages.List(User).Q(query).Context(ctx)
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
	m, err := c.svc.Users.Messages.Get(User, id).Format("full").Context(ctx).Do()
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
			}
		}
		out.Body = extractBody(m.Payload)
	}
	if len(out.Body) > maxBodyChars {
		cut := maxBodyChars
		for cut > 0 && !utf8.RuneStart(out.Body[cut]) {
			cut--
		}
		out.Body = out.Body[:cut]
	}
	return out, nil
}

// extractBody walks the MIME tree preferring text/plain over text/html.
func extractBody(part *gmail.MessagePart) string {
	if plain := findPart(part, "text/plain"); plain != "" {
		return plain
	}
	if raw := findPart(part, "text/html"); raw != "" {
		return htmlToText(raw)
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

// htmlToText extracts visible text: style/script dropped, entities decoded.
func htmlToText(s string) string {
	tok := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	skip := 0
	for {
		switch tok.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case html.StartTagToken:
			if name, _ := tok.TagName(); skipTag(string(name)) {
				skip++
			}
		case html.EndTagToken:
			if name, _ := tok.TagName(); skipTag(string(name)) && skip > 0 {
				skip--
			}
		case html.TextToken:
			if skip == 0 {
				b.Write(tok.Text())
				b.WriteByte(' ')
			}
		}
	}
}

func skipTag(name string) bool {
	switch name {
	case "script", "style", "head", "title", "template":
		return true
	}
	return false
}

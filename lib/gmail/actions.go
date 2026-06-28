package gmail

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
)

// ModifyLabels adds and removes label IDs on a message. Removing InboxLabel
// archives it (the message stays in All Mail).
func (c *Client) ModifyLabels(ctx context.Context, msgID string, add, remove []string) error {
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}
	_, err := c.Service.Users.Messages.Modify(User, msgID, &gmail.ModifyMessageRequest{
		AddLabelIds:    add,
		RemoveLabelIds: remove,
	}).Context(ctx).Do()
	return err
}

// DraftInput describes a reply draft to create in an existing thread.
type DraftInput struct {
	ThreadID  string
	To        string // the original sender
	Subject   string // the original subject (Re: is added if absent)
	Body      string
	InReplyTo string // the original Message-ID header, for threading
}

// CreateDraft creates a reply draft in the message's thread and returns its
// draft ID. The authenticated account is used as the From address.
func (c *Client) CreateDraft(ctx context.Context, in DraftInput) (string, error) {
	raw := base64.URLEncoding.EncodeToString([]byte(buildReply(in)))
	created, err := c.Service.Users.Drafts.Create(User, &gmail.Draft{
		Message: &gmail.Message{Raw: raw, ThreadId: in.ThreadID},
	}).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	return created.Id, nil
}

// GetDraft reports whether a draft still exists. Used by the reconcile pass to
// detect drafts Nat sent or deleted.
func (c *Client) GetDraft(ctx context.Context, draftID string) (bool, error) {
	_, err := c.Service.Users.Drafts.Get(User, draftID).Context(ctx).Do()
	if err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// HasInboxLabel reports whether a message is currently in the inbox. Used by
// the reconcile pass to detect mail Nat un-archived.
func (c *Client) HasInboxLabel(ctx context.Context, msgID string) (bool, error) {
	m, err := c.Service.Users.Messages.Get(User, msgID).Format("minimal").Context(ctx).Do()
	if err != nil {
		return false, err
	}
	return slices.Contains(m.LabelIds, InboxLabel), nil
}

func buildReply(in DraftInput) string {
	subject := in.Subject
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(subject)), "re:") {
		subject = "Re: " + subject
	}
	var b strings.Builder
	fmt.Fprintf(&b, "To: %s\r\n", in.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	if in.InReplyTo != "" {
		fmt.Fprintf(&b, "In-Reply-To: %s\r\n", in.InReplyTo)
		fmt.Fprintf(&b, "References: %s\r\n", in.InReplyTo)
	}
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(in.Body)
	return b.String()
}

func isNotFound(err error) bool {
	var gerr *googleapi.Error
	if errors.As(err, &gerr) {
		return gerr.Code == 404
	}
	return false
}

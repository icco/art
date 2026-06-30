package gmail

import (
	"context"

	"google.golang.org/api/gmail/v1"
)

// ModifyLabels adds and removes label IDs on a message. Removing InboxLabel
// archives it (the message stays in All Mail). Labeling and archiving are the
// only mutations Art is permitted to make to a mailbox: it never drafts, sends,
// or deletes mail.
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

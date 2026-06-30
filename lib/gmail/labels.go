package gmail

import (
	"context"

	"google.golang.org/api/gmail/v1"
)

// Art applies these labels so triage actions are attributable and bulk-reversible. Names are nested under "Art".
const (
	LabelTriaged  = "Art/Triaged"
	LabelArchived = "Art/Archived"
	LabelReply    = "Art/Reply"

	// InboxLabel is Gmail's system INBOX label; removing it archives a message.
	InboxLabel = "INBOX"
)

// ArtLabels is the full set of labels art manages, in creation order.
var ArtLabels = []string{LabelTriaged, LabelArchived, LabelReply}

// EnsureLabels makes sure every Art/* label exists and returns a name->id map.
// Creating a nested label ("Art/Triaged") auto-creates its parent.
func (c *Client) EnsureLabels(ctx context.Context) (map[string]string, error) {
	resp, err := c.Service.Users.Labels.List(User).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	existing := map[string]string{}
	for _, l := range resp.Labels {
		existing[l.Name] = l.Id
	}

	out := map[string]string{}
	for _, name := range ArtLabels {
		if id, ok := existing[name]; ok {
			out[name] = id
			continue
		}
		created, err := c.Service.Users.Labels.Create(User, &gmail.Label{
			Name:                  name,
			LabelListVisibility:   "labelShow",
			MessageListVisibility: "show",
		}).Context(ctx).Do()
		if err != nil {
			return nil, err
		}
		out[name] = created.Id
	}
	return out, nil
}

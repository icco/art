package gmail

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
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
// Gmail label names are case-insensitively unique.
func (c *Client) EnsureLabels(ctx context.Context) (map[string]string, error) {
	existing, err := c.labelsByLowerName(ctx)
	if err != nil {
		return nil, err
	}

	out := map[string]string{}
	for _, name := range ArtLabels {
		if id, ok := existing[strings.ToLower(name)]; ok {
			out[name] = id
			continue
		}
		created, err := c.svc.Users.Labels.Create(User, &gmail.Label{
			Name:                  name,
			LabelListVisibility:   "labelShow",
			MessageListVisibility: "show",
		}).Context(ctx).Do()
		if err != nil {
			// 409 means the label appeared since we listed; re-list and reuse.
			var gerr *googleapi.Error
			if errors.As(err, &gerr) && gerr.Code == http.StatusConflict {
				if existing, err = c.labelsByLowerName(ctx); err != nil {
					return nil, err
				}
				if id, ok := existing[strings.ToLower(name)]; ok {
					out[name] = id
					continue
				}
			}
			return nil, err
		}
		out[name] = created.Id
	}
	return out, nil
}

func (c *Client) labelsByLowerName(ctx context.Context) (map[string]string, error) {
	resp, err := c.svc.Users.Labels.List(User).Context(ctx).Do()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]string, len(resp.Labels))
	for _, l := range resp.Labels {
		byName[strings.ToLower(l.Name)] = l.Id
	}
	return byName, nil
}

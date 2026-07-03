// Package gmail wraps the Gmail v1 API for art's email triage. It mirrors the
// calendar package: one Client per linked account, built from the OAuth token.
package gmail

import (
	"context"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"google.golang.org/api/gmail/v1"
	"google.golang.org/api/option"
)

// User is the special userId Gmail accepts for the authenticated account.
const User = "me"

// Client is a Gmail client bound to a single linked account. svc stays
// unexported so the label/archive methods are the only mailbox surface.
type Client struct {
	Account models.Account
	svc     *gmail.Service
}

// NewClient constructs a Gmail Client for the given account kind, using the
// token stored by the OAuth Flow. It mirrors calendar.NewClient.
func NewClient(ctx context.Context, flow *oauth.Flow, kind models.AccountKind) (*Client, error) {
	ts, acct, err := flow.TokenSource(ctx, kind)
	if err != nil {
		return nil, err
	}
	svc, err := gmail.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return &Client{Account: acct, svc: svc}, nil
}

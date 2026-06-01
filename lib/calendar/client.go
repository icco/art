// Package calendar wraps the Google Calendar v3 API for art's syncs and writes.
package calendar

import (
	"context"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

// ArtManagedKey is the extended-property key art sets on events it owns;
// ArtManagedTrue is the corresponding value.
const (
	ArtManagedKey  = "art_managed"
	ArtManagedTrue = "true"
)

// Client is a Google Calendar client bound to a single linked account.
type Client struct {
	Account models.Account
	Service *calendar.Service
}

// NewClient constructs a calendar Client for the given account kind, using
// the token stored by the OAuth Flow.
func NewClient(ctx context.Context, flow *oauth.Flow, kind models.AccountKind) (*Client, error) {
	ts, acct, err := flow.TokenSource(ctx, kind)
	if err != nil {
		return nil, err
	}
	svc, err := calendar.NewService(ctx, option.WithTokenSource(ts))
	if err != nil {
		return nil, err
	}
	return &Client{Account: acct, Service: svc}, nil
}

package calendar

import (
	"context"

	"github.com/icco/art/lib/models"
	"github.com/icco/art/lib/oauth"
	"google.golang.org/api/calendar/v3"
	"google.golang.org/api/option"
)

const ArtManagedKey = "art_managed"

type Client struct {
	Account models.Account
	Service *calendar.Service
}

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

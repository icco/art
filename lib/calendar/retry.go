package calendar

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"google.golang.org/api/googleapi"
)

// maxAttempts bounds withRetry: 1 initial try + 3 retries.
const maxAttempts = 4

// withRetry runs fn, retrying transient Google API failures (429, 5xx, and
// rate-limit 403s) with exponential backoff and jitter starting at base.
// Permanent errors — including the 410 sync-token expiry that sync handles
// as control flow — surface immediately.
func withRetry(ctx context.Context, base time.Duration, fn func() error) error {
	var err error
	delay := base
	for attempt := 1; ; attempt++ {
		err = fn()
		if err == nil || attempt >= maxAttempts || !isTransient(err) {
			return err
		}
		jittered := delay + rand.N(delay) //nolint:gosec // jitter, not crypto
		select {
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		case <-time.After(jittered):
		}
		delay *= 2
	}
}

func isTransient(err error) bool {
	var gerr *googleapi.Error
	if !errors.As(err, &gerr) {
		return false
	}
	switch {
	case gerr.Code == 429, gerr.Code >= 500:
		return true
	case gerr.Code == 403:
		for _, item := range gerr.Errors {
			if item.Reason == "rateLimitExceeded" || item.Reason == "userRateLimitExceeded" {
				return true
			}
		}
	}
	return false
}

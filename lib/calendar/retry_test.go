package calendar

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/api/googleapi"
)

func TestWithRetryRetriesTransientErrors(t *testing.T) {
	for _, code := range []int{429, 500, 503} {
		attempts := 0
		err := withRetry(context.Background(), time.Millisecond, func() error {
			attempts++
			if attempts < 3 {
				return &googleapi.Error{Code: code}
			}
			return nil
		})
		if err != nil || attempts != 3 {
			t.Errorf("code %d: err=%v attempts=%d", code, err, attempts)
		}
	}
}

func TestWithRetryRateLimit403(t *testing.T) {
	attempts := 0
	err := withRetry(context.Background(), time.Millisecond, func() error {
		attempts++
		if attempts == 1 {
			return &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "rateLimitExceeded"}}}
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("rate-limit 403 should retry: err=%v attempts=%d", err, attempts)
	}
}

func TestWithRetryDoesNotRetryPermanentErrors(t *testing.T) {
	cases := map[string]error{
		"410 sync token expired": &googleapi.Error{Code: 410},
		"404 not found":          &googleapi.Error{Code: 404},
		"403 forbidden":          &googleapi.Error{Code: 403, Errors: []googleapi.ErrorItem{{Reason: "forbidden"}}},
		"plain error":            errors.New("nope"),
	}
	for name, want := range cases {
		attempts := 0
		err := withRetry(context.Background(), time.Millisecond, func() error {
			attempts++
			return want
		})
		if !errors.Is(err, want) || attempts != 1 {
			t.Errorf("%s: err=%v attempts=%d, want 1 attempt", name, err, attempts)
		}
	}
}

func TestWithRetryGivesUpEventually(t *testing.T) {
	attempts := 0
	err := withRetry(context.Background(), time.Millisecond, func() error {
		attempts++
		return &googleapi.Error{Code: 503}
	})
	if err == nil {
		t.Fatal("expected the final error to surface")
	}
	if attempts != maxAttempts {
		t.Fatalf("attempts=%d, want %d", attempts, maxAttempts)
	}
}

func TestWithRetryHonorsContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts := 0
	err := withRetry(ctx, time.Minute, func() error {
		attempts++
		return &googleapi.Error{Code: 503}
	})
	if err == nil || attempts != 1 {
		t.Fatalf("cancelled context should stop retries: err=%v attempts=%d", err, attempts)
	}
}

// Package logging wraps icco/gutil/logging with context Inject/From helpers
// so handlers and background jobs can pass the logger through context.Context.
package logging

import (
	"context"

	gutil "github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

type ctxKey struct{}

// New constructs the service logger via gutil. The level argument is
// preserved for compatibility but gutil configures debug-level production
// logging unconditionally; pass any string.
func New(_ string) (*zap.SugaredLogger, error) {
	return gutil.NewLogger("art")
}

// Sync flushes the logger at shutdown. Wraps gutil's Sync so callers don't
// have to import gutil directly for this one call.
func Sync(l *zap.SugaredLogger) { gutil.Sync(l) }

func Inject(ctx context.Context, l *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func From(ctx context.Context) *zap.SugaredLogger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.SugaredLogger); ok && l != nil {
		return l
	}
	return zap.NewNop().Sugar()
}

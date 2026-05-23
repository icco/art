// Package logging delegates to icco/gutil/logging for the zap SugaredLogger
// (production-config JSON, debug-level) and adds context Inject/From helpers.
package logging

import (
	"context"

	gutil "github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

type ctxKey struct{}

// New returns the gutil-backed SugaredLogger. All log settings — encoder,
// level, fields — come from gutil; nothing is configured locally.
func New() (*zap.SugaredLogger, error) { return gutil.NewLogger("art") }

// Sync flushes the logger at shutdown.
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

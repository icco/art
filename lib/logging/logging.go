// Package logging delegates to icco/gutil/logging for the zap SugaredLogger
// (production-config JSON, debug-level) and re-exports the context helpers
// so the gutil HTTP middleware and our handlers share one ctxKey.
package logging

import (
	"context"

	gutil "github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

// New returns the gutil-backed SugaredLogger. All log settings — encoder,
// level, fields — come from gutil; nothing is configured locally.
func New() (*zap.SugaredLogger, error) { return gutil.NewLogger("art") }

// Sync flushes the logger at shutdown.
func Sync(l *zap.SugaredLogger) { gutil.Sync(l) }

// Inject and From wrap gutil's context helpers so gutil/logging.Middleware
// (which calls NewContext) and our handlers see the same logger.
func Inject(ctx context.Context, l *zap.SugaredLogger) context.Context {
	return gutil.NewContext(ctx, l)
}

func From(ctx context.Context) *zap.SugaredLogger {
	return gutil.FromContext(ctx)
}

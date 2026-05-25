// Package logging re-exports gutil/logging so its HTTP middleware and our
// handlers share one ctxKey.
package logging

import (
	"context"

	gutil "github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

func New() (*zap.SugaredLogger, error) { return gutil.NewLogger("art") }

func Sync(l *zap.SugaredLogger) { gutil.Sync(l) }

func Inject(ctx context.Context, l *zap.SugaredLogger) context.Context {
	return gutil.NewContext(ctx, l)
}

func From(ctx context.Context) *zap.SugaredLogger {
	return gutil.FromContext(ctx)
}

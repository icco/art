// Package logging wraps icco/gutil/logging with context Inject/From.
package logging

import (
	"context"

	gutil "github.com/icco/gutil/logging"
	"go.uber.org/zap"
)

type ctxKey struct{}

func New(_ string) (*zap.SugaredLogger, error) { return gutil.NewLogger("art") }
func Sync(l *zap.SugaredLogger)                { gutil.Sync(l) }

func Inject(ctx context.Context, l *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func From(ctx context.Context) *zap.SugaredLogger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.SugaredLogger); ok && l != nil {
		return l
	}
	return zap.NewNop().Sugar()
}

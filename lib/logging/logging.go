package logging

import (
	"context"

	"go.uber.org/zap"
)

type ctxKey struct{}

// New returns a zap logger configured by level. Sugared logger is returned for
// convenience; the underlying *zap.Logger is on the .Desugar() of the result.
func New(level string) (*zap.SugaredLogger, error) {
	cfg := zap.NewProductionConfig()
	if err := cfg.Level.UnmarshalText([]byte(level)); err != nil {
		cfg.Level = zap.NewAtomicLevelAt(zap.InfoLevel)
	}
	l, err := cfg.Build()
	if err != nil {
		return nil, err
	}
	return l.Sugar(), nil
}

func Inject(ctx context.Context, l *zap.SugaredLogger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

func From(ctx context.Context) *zap.SugaredLogger {
	if l, ok := ctx.Value(ctxKey{}).(*zap.SugaredLogger); ok && l != nil {
		return l
	}
	return zap.NewNop().Sugar()
}

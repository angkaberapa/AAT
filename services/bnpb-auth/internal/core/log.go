package core

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// NewLogger returns a JSON logger writing one line per event to stdout. Every line
// carries `service`, and `correlation_id` taken from the context, so always log with
// the *Context variants (logger.InfoContext(ctx, ...)).
func NewLogger(service, level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(strings.ToUpper(level))); err != nil {
		lvl = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lvl,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if len(groups) == 0 && a.Key == slog.TimeKey {
				a = slog.Time("ts", a.Value.Time().UTC())
			}
			return a
		},
	})
	return slog.New(correlationHandler{h}).With("service", service)
}

type correlationHandler struct{ slog.Handler }

func (h correlationHandler) Handle(ctx context.Context, r slog.Record) error {
	r.AddAttrs(slog.String("correlation_id", CorrelationID(ctx)))
	return h.Handler.Handle(ctx, r)
}

func (h correlationHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return correlationHandler{h.Handler.WithAttrs(attrs)}
}

func (h correlationHandler) WithGroup(name string) slog.Handler {
	return correlationHandler{h.Handler.WithGroup(name)}
}

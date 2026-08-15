package logging

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"time"
)

type PrettyJSONHandler struct {
	w    io.Writer
	opts slog.HandlerOptions
}

func NewPrettyJSONHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &PrettyJSONHandler{w: w, opts: *opts}
}

func (h *PrettyJSONHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := h.opts.Level
	if minLevel == nil {
		return true
	}
	return level >= minLevel.Level()
}

func (h *PrettyJSONHandler) Handle(ctx context.Context, r slog.Record) error {
	out := map[string]any{
		"time":  r.Time.Format(time.RFC3339),
		"level": r.Level.String(),
		"msg":   r.Message,
	}

	r.Attrs(func(a slog.Attr) bool {
		out[a.Key] = a.Value.Any()
		return true
	})

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(h.w, "%s\n", b)
	return err
}

func (h *PrettyJSONHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *PrettyJSONHandler) WithGroup(name string) slog.Handler {
	return h
}

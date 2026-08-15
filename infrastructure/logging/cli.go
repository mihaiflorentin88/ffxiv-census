package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

type cliHandler struct {
	w    io.Writer
	opts slog.HandlerOptions
}

func NewCliHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &cliHandler{
		w:    w,
		opts: *opts,
	}
}

func (h *cliHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := h.opts.Level
	if minLevel == nil {
		return true
	}
	return level >= minLevel.Level()
}

func (h *cliHandler) Handle(ctx context.Context, r slog.Record) error {
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	ts := t.Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("%s | %s -> %s", ts, r.Level, r.Message)
	var attrs []string
	r.Attrs(func(a slog.Attr) bool {
		attrs = append(attrs, fmt.Sprintf("%s: %v", a.Key, a.Value))
		return true
	})
	if len(attrs) > 0 {
		line += " | " + strings.Join(attrs, " | ")
	}
	_, err := fmt.Fprintln(h.w, line)
	return err
}

func (h *cliHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *cliHandler) WithGroup(name string) slog.Handler {
	return h
}

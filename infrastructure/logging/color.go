package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"time"
)

const (
	colorReset  = "\033[0m"
	colorBlue   = "\033[34m"
	colorYellow = "\033[33m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
)

type colorHandler struct {
	w    io.Writer
	opts slog.HandlerOptions
}

func NewColorHandler(w io.Writer, opts *slog.HandlerOptions) slog.Handler {
	if opts == nil {
		opts = &slog.HandlerOptions{}
	}
	return &colorHandler{
		w:    w,
		opts: *opts,
	}
}

func (h *colorHandler) Enabled(_ context.Context, level slog.Level) bool {
	minLevel := h.opts.Level
	if minLevel == nil {
		return true
	}
	return level >= minLevel.Level()
}

func (h *colorHandler) Handle(ctx context.Context, r slog.Record) error {
	t := r.Time
	if t.IsZero() {
		t = time.Now()
	}
	ts := t.Format("2006-01-02 15:04:05")

	var levelColor string
	switch r.Level {
	case slog.LevelInfo:
		levelColor = "🔵 " + colorBlue
	case slog.LevelDebug:
		levelColor = "🟡 " + colorYellow
	case slog.LevelWarn:
		levelColor = "🟡 " + colorYellow
	case slog.LevelError:
		levelColor = "🔴 " + colorRed
	default:
		levelColor = "⚪ " + colorReset
	}

	simpleLevelColor := strings.Split(levelColor, " ")[1]

	var messages []string
	r.Attrs(func(a slog.Attr) bool {
		messages = append(messages, fmt.Sprintf("%s%s%s: %s%v%s", colorYellow, a.Key, colorReset, simpleLevelColor, a.Value, colorReset))
		return true
	})
	coloredLevel := levelColor + r.Level.String() + colorReset
	line := fmt.Sprintf("%s | %s", ts, coloredLevel)
	line += fmt.Sprintf(" -> %s%s%s", colorGreen, r.Message, colorReset)
	if len(messages) > 0 {
		line += " <- " + strings.Join(messages, " | ")
	}

	_, err := fmt.Fprintln(h.w, line)
	return err
}

func (h *colorHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return h
}

func (h *colorHandler) WithGroup(name string) slog.Handler {
	return h
}

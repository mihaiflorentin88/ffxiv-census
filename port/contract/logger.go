package contract

import (
	"context"
	"log/slog"
)

// Logger is a context-aware structured logger. Its methods match the
// corresponding methods of *log/slog.Logger (DebugContext/InfoContext/
// WarnContext/ErrorContext with args as alternating key/value pairs), so
// infrastructure/logging.Logger satisfies it with no adapter.
type Logger interface {
	DebugContext(ctx context.Context, msg string, args ...any)
	InfoContext(ctx context.Context, msg string, args ...any)
	WarnContext(ctx context.Context, msg string, args ...any)
	ErrorContext(ctx context.Context, msg string, args ...any)
	// Enabled reports whether the logger would emit at the given level.
	// *slog.Logger already conforms; a discard logger returns false.
	Enabled(ctx context.Context, level slog.Level) bool
}

package logging

import "log/slog"

type SlogWriter struct {
    Level slog.Level
    Event string
}

func (s SlogWriter) Write(p []byte) (n int, err error) {
    Log(s.Level, s.Event, string(p))
    return len(p), nil
}

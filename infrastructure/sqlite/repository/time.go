package repository

import "time"

// timeLayout matches the queue_jobs TEXT timestamp convention and SQLite's
// strftime('%Y-%m-%dT%H:%M:%fZ','now').
const timeLayout = "2006-01-02T15:04:05.000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

func parseTime(s string) (time.Time, error) { return time.Parse(timeLayout, s) }

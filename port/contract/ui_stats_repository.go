package contract

import (
	"context"
	"time"
)

const UIStatsSchemaVersion = 1

// StatsScope identifies the aggregation scope. Empty fields mean global.
// At most one field is normally populated.
type StatsScope struct {
	Region     string `json:"region,omitempty"`
	Datacenter string `json:"datacenter,omitempty"`
	World      string `json:"world,omitempty"`
}

type StatsSummary struct {
	Total    int64 `json:"total"`
	Active   int64 `json:"active"`
	MaxLevel int64 `json:"max_level"`
}

type ScopedGroupCount struct {
	Scope     StatsScope `json:"scope"`
	Dimension string     `json:"dimension"`
	Key       string     `json:"key"`
	Total     int64      `json:"total"`
	Active    int64      `json:"active"`
}

type ScopedExpansionCount struct {
	Scope     StatsScope `json:"scope"`
	Expansion string     `json:"expansion"`
	Count     int64      `json:"count"`
}

type ScopedDailyCount struct {
	Scope StatsScope `json:"scope"`
	Day   string     `json:"day"`
	Count int64      `json:"count"`
}

// UIStatsSnapshot is the complete, versioned read model used by aggregate UI
// and REST routes.
type UIStatsSnapshot struct {
	SchemaVersion    int                    `json:"schema_version"`
	GeneratedAt      time.Time              `json:"generated_at"`
	ActivitySince    time.Time              `json:"activity_since"`
	MaxLevel         uint32                 `json:"max_level"`
	SourceCharacters int64                  `json:"source_characters"`
	RefreshDuration  time.Duration          `json:"refresh_duration"`
	Groups           []ScopedGroupCount     `json:"groups"`
	Expansions       []ScopedExpansionCount `json:"expansions"`
	NewCharacters    []ScopedDailyCount     `json:"new_characters"`
	Summary          StatsSummary           `json:"summary"`
}

type UIStatsRefreshOptions struct {
	ActivitySince time.Time
	MaxLevel      uint32
	Timeout       time.Duration
}

type UIStatsRefreshResult struct {
	Snapshot     *UIStatsSnapshot
	Skipped      bool
	PayloadBytes int64
}

// UIStatsRepository persists and refreshes the bounded analytics read model.
type UIStatsRepository interface {
	LoadCurrent(ctx context.Context) (*UIStatsSnapshot, error)
	Refresh(ctx context.Context, opts UIStatsRefreshOptions) (*UIStatsRefreshResult, error)
}

// UIStatsObserver receives bounded-label cache and refresh measurements.
type UIStatsObserver interface {
	ObserveUIStatsCache(result string, snapshot *UIStatsSnapshot)
	ObserveUIStatsRefresh(result string, duration time.Duration, payloadBytes int64)
}

package response

import (
	"encoding/json"
	"time"
)

type QueueJobItem struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	PayloadHash string          `json:"payload_hash"`
	Status      string          `json:"status"`
	RunAt       time.Time       `json:"run_at"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	LastError   *string         `json:"last_error,omitempty"`
	ClaimedAt   *time.Time      `json:"claimed_at,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	FailedAt    *time.Time      `json:"failed_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

type PaginatedQueueJobs struct {
	Items  []QueueJobItem `json:"items"`
	Total  int64          `json:"total"`
	Limit  int            `json:"limit"`
	Offset int            `json:"offset"`
}

type QueueJobSummaryDTO struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	Attempts    int             `json:"attempts"`
	MaxAttempts int             `json:"max_attempts"`
	LastError   *string         `json:"last_error,omitempty"`
	ClaimedAt   *time.Time      `json:"claimed_at,omitempty"`
	RunAt       time.Time       `json:"run_at"`
	CreatedAt   time.Time       `json:"created_at"`
	FailedAt    *time.Time      `json:"failed_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

type QueueOverviewSummary struct {
	Total   int                         `json:"total"`
	Pending int                         `json:"pending"`
	Claimed int                         `json:"claimed"`
	Done    int                         `json:"done"`
	Failed  int                         `json:"failed"`
	ByEvent map[string]QueueEventCounts `json:"by_event"`
}

type QueueEventCounts struct {
	Pending int `json:"pending"`
	Claimed int `json:"claimed"`
	Done    int `json:"done"`
	Failed  int `json:"failed"`
	Total   int `json:"total"`
}

type QueueOverviewResponse struct {
	Summary QueueOverviewSummary    `json:"summary"`
	Events  []QueueEventTypeSummary `json:"events"`
}

type QueueEventTypeSummary struct {
	Type        string               `json:"type"`
	Description string               `json:"description"`
	Pending     int                  `json:"pending"`
	Claimed     int                  `json:"claimed"`
	Done        int                  `json:"done"`
	Failed      int                  `json:"failed"`
	Total       int                  `json:"total"`
	ActiveJobs  []QueueJobSummaryDTO `json:"active_jobs"`
	NextJobs    []QueueJobSummaryDTO `json:"next_jobs"`
	FailedJobs  []QueueJobSummaryDTO `json:"failed_jobs"`
}

type QueueRetryFailedResponse struct {
	Retried int    `json:"retried"`
	Message string `json:"message"`
}

type QueuePurgeResponse struct {
	Purged    int64  `json:"purged"`
	EventType string `json:"event_type,omitempty"`
	Status    string `json:"status"`
	OlderThan string `json:"older_than"`
}

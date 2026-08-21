package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Event types carried as queue job "type" strings.
const (
	EventNewProxy = "new-proxy"
)

// NewProxyPayload carries the data needed to register and test a new proxy.
type NewProxyPayload struct {
	Protocol      string   `json:"protocol"`
	IP            string   `json:"ip"`
	Port          int      `json:"port"`
	Country       *string  `json:"country,omitempty"`
	Anonymity     *string  `json:"anonymity,omitempty"`
	Source        string   `json:"source"`
	UptimePercent *float64 `json:"uptime_percent,omitempty"`
}

// NewProxyJob builds a new-proxy queue job.
func NewProxyJob(payload NewProxyPayload) contract.QueueJob {
	b, _ := json.Marshal(payload)
	return contract.QueueJob{Type: EventNewProxy, Payload: b}
}

// Handler processes a single queue job's payload and returns the jobs to publish
// next (downstream chaining). A non-nil error signals a transient failure; the
// worker maps it to Queue.Retry.
type Handler interface {
	Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error)
}

// loggerOrDiscard returns l, or a discard logger when l is nil, so handlers
// never require a non-nil logger.
func loggerOrDiscard(l contract.Logger) contract.Logger {
	if l == nil {
		return slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return l
}

// Registry maps event types to their handlers.
type Registry struct {
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: map[string]Handler{}}
}

func (r *Registry) Register(eventType string, h Handler) {
	r.handlers[eventType] = h
}

func (r *Registry) Get(eventType string) (Handler, bool) {
	h, ok := r.handlers[eventType]
	return h, ok
}

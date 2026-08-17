package handler

import (
	"context"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Handler processes a single queue job's payload and returns the jobs to publish
// next (downstream chaining). A non-nil error signals a transient failure; the
// worker maps it to Queue.Retry.
type Handler interface {
	Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error)
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

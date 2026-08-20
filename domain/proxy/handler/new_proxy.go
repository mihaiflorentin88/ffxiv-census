package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// NewProxy handles new-proxy events: inserts a discovered proxy and tests it.
type NewProxy struct {
	service *proxy.Service
	logger  contract.Logger
}

func NewNewProxy(svc *proxy.Service, logger contract.Logger) *NewProxy {
	return &NewProxy{service: svc, logger: logger}
}

func (h *NewProxy) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p NewProxyPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("new-proxy payload: %w", err)
	}
	if err := h.service.ProcessNewProxy(ctx, p.Protocol, p.IP, p.Port, p.Country, p.Anonymity, p.Source, p.UptimePercent); err != nil {
		return nil, err
	}
	return nil, nil // leaf event — no chaining
}

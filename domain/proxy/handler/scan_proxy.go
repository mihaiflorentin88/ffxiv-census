package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/domain/proxy"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ScanProxy handles scan-proxy events: tests an existing proxy and updates its status.
type ScanProxy struct {
	service *proxy.Service
	logger  contract.Logger
}

func NewScanProxy(svc *proxy.Service, logger contract.Logger) *ScanProxy {
	return &ScanProxy{service: svc, logger: logger}
}

func (h *ScanProxy) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p ScanProxyPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("scan-proxy payload: %w", err)
	}
	if err := h.service.ProcessScanProxy(ctx, p.ProxyID); err != nil {
		return nil, err
	}
	return nil, nil // leaf event — no chaining
}

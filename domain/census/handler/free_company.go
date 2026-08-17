package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FreeCompanyCensus fetches a free company and upserts its record. It is a leaf
// event (member-list re-census is deferred until FetchFreeCompanyMembers is
// exposed by the LodestoneClient contract).
type FreeCompanyCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
}

func NewFreeCompanyCensus(lodestone contract.LodestoneClient, svc *census.Service) *FreeCompanyCensus {
	return &FreeCompanyCensus{lodestone: lodestone, census: svc}
}

func (h *FreeCompanyCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p FreeCompanyCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("fc-census payload: %w", err)
	}
	fc, err := h.lodestone.FetchFreeCompany(ctx, p.FCID)
	if err != nil {
		return nil, fmt.Errorf("fc-census fetch %s: %w", p.FCID, err)
	}
	if err := h.census.UpsertFreeCompany(ctx, fc); err != nil {
		return nil, fmt.Errorf("fc-census upsert %s: %w", p.FCID, err)
	}
	return nil, nil
}

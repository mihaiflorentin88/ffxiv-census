package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// FreeCompanyCensus fetches a free company and upserts its record. It is a leaf
// event (member-list re-census is deferred until FetchFreeCompanyMembers is
// exposed by the LodestoneClient contract).
type FreeCompanyCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
	logger    contract.Logger
}

func NewFreeCompanyCensus(lodestone contract.LodestoneClient, svc *census.Service, logger contract.Logger) *FreeCompanyCensus {
	return &FreeCompanyCensus{lodestone: lodestone, census: svc, logger: loggerOrDiscard(logger)}
}

func (h *FreeCompanyCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p FreeCompanyCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("fc-census payload: %w", err)
	}
	h.logger.InfoContext(ctx, "handler.fc_census", slog.String("fc_id", p.FCID))
	fc, err := h.lodestone.FetchFreeCompany(ctx, p.FCID)
	if err != nil {
		h.logger.WarnContext(ctx, "handler.fc_census.fetch_error", slog.String("fc_id", p.FCID), slog.Any("error", err))
		return nil, fmt.Errorf("fc-census fetch %s: %w", p.FCID, err)
	}
	h.logger.InfoContext(ctx, "handler.fc_census.fetched", slog.String("fc_id", p.FCID), slog.String("name", fc.Name), slog.String("world", fc.World), slog.Int("members", int(fc.ActiveMemberCount)))
	if err := h.census.UpsertFreeCompany(ctx, fc); err != nil {
		h.logger.ErrorContext(ctx, "handler.fc_census.store_error", slog.String("fc_id", p.FCID), slog.String("name", fc.Name), slog.String("world", fc.World), slog.Any("error", err))
		return nil, fmt.Errorf("fc-census upsert %s: %w", p.FCID, err)
	}
	h.logger.InfoContext(ctx, "handler.fc_census.stored", slog.String("fc_id", p.FCID), slog.String("name", fc.Name), slog.String("world", fc.World))
	return nil, nil
}

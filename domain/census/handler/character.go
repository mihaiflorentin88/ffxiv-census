package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CharacterCensus re-censuses a known character: fetch → upsert (or mark deleted
// on 404), then chain an achievement-census job and (when in an FC) an fc-census.
type CharacterCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
	logger    contract.Logger
}

func NewCharacterCensus(lodestone contract.LodestoneClient, svc *census.Service, logger contract.Logger) *CharacterCensus {
	return &CharacterCensus{lodestone: lodestone, census: svc, logger: loggerOrDiscard(logger)}
}

func (h *CharacterCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p CharacterCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("character-census payload: %w", err)
	}
	h.logger.InfoContext(ctx, "handler.character_census", slog.Uint64("character_id", uint64(p.CharacterID)))
	char, err := h.lodestone.FetchCharacter(ctx, p.CharacterID)
	if errors.Is(err, contract.ErrCharacterNotFound) {
		if derr := h.census.MarkCharacterDeleted(ctx, p.CharacterID, time.Now().UTC()); derr != nil {
			h.logger.ErrorContext(ctx, "handler.character_census.store_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", derr))
			return nil, fmt.Errorf("character-census mark-deleted %d: %w", p.CharacterID, derr)
		}
		h.logger.InfoContext(ctx, "handler.character_census.deleted", slog.Uint64("character_id", uint64(p.CharacterID)))
		return nil, nil // deleted: no chained jobs
	}
	if err != nil {
		h.logger.WarnContext(ctx, "handler.character_census.fetch_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
		return nil, fmt.Errorf("character-census fetch %d: %w", p.CharacterID, err)
	}
	h.logger.InfoContext(ctx, "handler.character_census.fetched", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", char.Name), slog.String("world", char.World), slog.String("fc_id", char.FreeCompanyID))
	if err := h.census.UpsertCharacter(ctx, char); err != nil {
		h.logger.ErrorContext(ctx, "handler.character_census.store_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", char.Name), slog.String("world", char.World), slog.Any("error", err))
		return nil, fmt.Errorf("character-census upsert %d: %w", p.CharacterID, err)
	}
	h.logger.InfoContext(ctx, "handler.character_census.stored", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", char.Name), slog.String("world", char.World))
	next := []contract.QueueJob{AchievementCensusJob(char.ID)}
	if char.FreeCompanyID != "" {
		next = append(next, FreeCompanyCensusJob(char.FreeCompanyID))
	}
	h.logger.InfoContext(ctx, "handler.character_census.done", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Int("chained", len(next)))
	return next, nil
}

package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// CharacterCensus re-censuses a known character: fetch → upsert (or mark deleted
// on 404), then chain an achievement-census job and (when in an FC) an fc-census.
type CharacterCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
}

func NewCharacterCensus(lodestone contract.LodestoneClient, svc *census.Service) *CharacterCensus {
	return &CharacterCensus{lodestone: lodestone, census: svc}
}

func (h *CharacterCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p CharacterCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("character-census payload: %w", err)
	}
	char, err := h.lodestone.FetchCharacter(ctx, p.CharacterID)
	if errors.Is(err, contract.ErrCharacterNotFound) {
		if derr := h.census.MarkCharacterDeleted(ctx, p.CharacterID, time.Now().UTC()); derr != nil {
			return nil, derr
		}
		return nil, nil // deleted: no chained jobs
	}
	if err != nil {
		return nil, fmt.Errorf("character-census fetch %d: %w", p.CharacterID, err)
	}
	if err := h.census.UpsertCharacter(ctx, char); err != nil {
		return nil, fmt.Errorf("character-census upsert %d: %w", p.CharacterID, err)
	}
	next := []contract.QueueJob{AchievementCensusJob(p.CharacterID)}
	if char.FreeCompanyID != "" {
		next = append(next, FreeCompanyCensusJob(char.FreeCompanyID))
	}
	return next, nil
}

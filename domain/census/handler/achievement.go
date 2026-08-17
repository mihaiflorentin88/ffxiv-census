package handler

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementCensus fetches a character's achievements and runs the milestone
// filter + latest-achievement tracking. It is a leaf event (no chained jobs).
type AchievementCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
}

func NewAchievementCensus(lodestone contract.LodestoneClient, svc *census.Service) *AchievementCensus {
	return &AchievementCensus{lodestone: lodestone, census: svc}
}

func (h *AchievementCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p AchievementCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("achievement-census payload: %w", err)
	}
	list, all, err := h.lodestone.FetchAchievements(ctx, p.CharacterID)
	if err != nil {
		return nil, fmt.Errorf("achievement-census fetch %d: %w", p.CharacterID, err)
	}
	if _, err := h.census.ProcessAchievements(ctx, p.CharacterID, list, all); err != nil {
		return nil, fmt.Errorf("achievement-census process %d: %w", p.CharacterID, err)
	}
	return nil, nil
}

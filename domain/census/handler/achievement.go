package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/xivapi/godestone/v2"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementCensus fetches a character's achievements and runs the milestone
// filter + latest-achievement tracking. It is a leaf event (no chained jobs).
type AchievementCensus struct {
	lodestone contract.LodestoneClient
	census    *census.Service
	logger    contract.Logger
}

func NewAchievementCensus(lodestone contract.LodestoneClient, svc *census.Service, logger contract.Logger) *AchievementCensus {
	return &AchievementCensus{lodestone: lodestone, census: svc, logger: loggerOrDiscard(logger)}
}

func (h *AchievementCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p AchievementCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("achievement-census payload: %w", err)
	}
	h.logger.InfoContext(ctx, "handler.achievement_census", slog.Uint64("character_id", uint64(p.CharacterID)))
	list, all, err := h.lodestone.FetchAchievements(ctx, p.CharacterID)
	if err != nil {
		h.logger.WarnContext(ctx, "handler.achievement_census.fetch_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
		return nil, fmt.Errorf("achievement-census fetch %d: %w", p.CharacterID, err)
	}
	latest := latestAchievement(list)
	if latest != nil {
		h.logger.InfoContext(ctx, "handler.achievement_census.fetched", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Int("earned", len(list)), slog.Uint64("latest_id", uint64(latest.ID)), slog.String("latest_name", latest.NamedEntity.Name))
	} else {
		h.logger.InfoContext(ctx, "handler.achievement_census.fetched", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Int("earned", len(list)))
	}
	milestones, err := h.census.ProcessAchievements(ctx, p.CharacterID, list, all)
	if err != nil {
		h.logger.ErrorContext(ctx, "handler.achievement_census.process_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
		return nil, fmt.Errorf("achievement-census process %d: %w", p.CharacterID, err)
	}
	h.logger.InfoContext(ctx, "handler.achievement_census.done", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Int("milestones", len(milestones)), slog.Bool("private", all != nil && all.Private))
	return nil, nil
}

// latestAchievement returns the most recently earned achievement in list,
// mirroring the latest-achievement computation in census.ProcessAchievements.
func latestAchievement(list []*godestone.AchievementInfo) *godestone.AchievementInfo {
	var latest *godestone.AchievementInfo
	for _, a := range list {
		if a == nil || a.NamedEntity == nil {
			continue
		}
		if latest == nil || a.Date.After(latest.Date) {
			latest = a
		}
	}
	return latest
}

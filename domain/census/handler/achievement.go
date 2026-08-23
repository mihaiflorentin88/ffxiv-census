package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// AchievementCensus fetches a character's achievements and runs the milestone
// filter + latest-achievement tracking. It is a leaf event (no chained jobs).
type AchievementCensus struct {
	lodestone   contract.LodestoneClient
	census      *census.Service
	logger      contract.Logger
	rateLimiter contract.ProviderRateLimiter
}

func NewAchievementCensus(lodestone contract.LodestoneClient, svc *census.Service, logger contract.Logger, rateLimiter ...contract.ProviderRateLimiter) *AchievementCensus {
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	return &AchievementCensus{lodestone: lodestone, census: svc, logger: loggerOrDiscard(logger), rateLimiter: rl}
}

func (h *AchievementCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p AchievementCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("achievement-census payload: %w", err)
	}
	h.logger.DebugContext(ctx, "handler.achievement_census", slog.Uint64("character_id", uint64(p.CharacterID)))

	// Wait for Lodestone if rate-limited (blocks until cooldown expires).
	if h.rateLimiter != nil && !h.rateLimiter.IsAvailable(contract.ProviderLodestone) {
		h.logger.InfoContext(ctx, "handler.achievement_census.waiting_for_lodestone", slog.Uint64("character_id", uint64(p.CharacterID)))
		if err := h.rateLimiter.WaitUntilAvailable(ctx, contract.ProviderLodestone); err != nil {
			return nil, fmt.Errorf("achievement-census %d: waiting for lodestone: %w", p.CharacterID, err)
		}
	}

	// Query DB for already-known milestones to optimize scraping.
	knownMilestones, err := h.census.ListCharacterMilestones(ctx, p.CharacterID)
	if err != nil {
		// Log warning but continue — DB check is optimization, not requirement.
		h.logger.WarnContext(ctx, "handler.achievement_census.milestone_query_failed", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
	}

	// Get milestone IDs from config.
	milestoneIDSet, err := h.census.MilestoneIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("achievement-census %d: get milestone IDs: %w", p.CharacterID, err)
	}

	// Check if we can skip scraping entirely: all milestones known + fresh data.
	if len(milestoneIDSet) > 0 {
		found := make(map[uint32]bool, len(milestoneIDSet))
		for _, m := range knownMilestones {
			if milestoneIDSet[m.AchievementID] {
				found[m.AchievementID] = true
			}
		}

		allMilestonesKnown := len(found) == len(milestoneIDSet)
		freshEnough := false
		if allMilestonesKnown {
			char, getErr := h.census.GetCharacter(ctx, p.CharacterID)
			if getErr == nil && char.LatestAchievementAt != nil {
				staleness := time.Since(*char.LatestAchievementAt)
				freshEnough = staleness < time.Duration(h.census.AchievementStalenessDays())*24*time.Hour
			}
		}

		if allMilestonesKnown && freshEnough {
			h.logger.DebugContext(ctx, "handler.achievement_census.skipped",
				slog.Uint64("character_id", uint64(p.CharacterID)),
				slog.String("reason", "all_milestones_known_and_fresh"))
			return nil, nil
		}
	}

	// Build ordered milestone IDs slice for sequential checking.
	milestoneIDs := make([]uint32, 0, len(milestoneIDSet))
	for id := range milestoneIDSet {
		milestoneIDs = append(milestoneIDs, id)
	}

	start := time.Now()
	summary, err := h.lodestone.FetchAchievements(ctx, p.CharacterID, milestoneIDs)
	if err != nil {
		h.logger.WarnContext(ctx, "handler.achievement_census.fetch_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
		return nil, fmt.Errorf("achievement-census fetch %d: %w", p.CharacterID, err)
	}

	if summary != nil && summary.Private {
		h.logger.DebugContext(ctx, "handler.achievement_census.private", slog.Uint64("character_id", uint64(p.CharacterID)))
	}

	milestones, err := h.census.ProcessMilestoneResults(ctx, p.CharacterID, summary)
	if err != nil {
		h.logger.ErrorContext(ctx, "handler.achievement_census.process_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
		return nil, fmt.Errorf("achievement-census process %d: %w", p.CharacterID, err)
	}

	duration := time.Since(start)
	private := summary != nil && summary.Private
	requests := 1 // privacy check
	if summary != nil {
		requests += len(summary.Milestones)
		if !summary.Private && len(summary.Milestones) < len(milestoneIDs) {
			requests++ // the missing milestone request
		}
	}

	h.logger.DebugContext(ctx, "handler.achievement_census.complete",
		slog.Uint64("character_id", uint64(p.CharacterID)),
		slog.Int("milestones", len(milestones)),
		slog.Int("requests", requests),
		slog.Bool("private", private),
		slog.Duration("duration", duration))
	return nil, nil
}

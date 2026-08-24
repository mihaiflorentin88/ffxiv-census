package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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

	milestoneIDs := missingMilestoneIDs(milestoneIDSet, knownMilestones)
	if milestoneIDs == nil {
		h.logger.InfoContext(ctx, "handler.achievement_census.skipped", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("reason", "all_milestones_known"))
		return nil, nil
	}

	start := time.Now()
	summary, err := h.lodestone.FetchAchievements(ctx, p.CharacterID, milestoneIDs)
	if err != nil {
		h.logger.WarnContext(ctx, "handler.achievement_census.fetch_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
		return nil, fmt.Errorf("achievement-census fetch %d: %w", p.CharacterID, err)
	}
	if summary != nil {
		summary.LatestAchievement = latestKnownMilestone(summary.LatestAchievement, knownMilestones)
	}

	if summary != nil && summary.Private {
		h.logger.InfoContext(ctx, "handler.achievement_census.private",
			slog.Uint64("character_id", uint64(p.CharacterID)))
	}
	if summary != nil {
		h.logger.InfoContext(ctx, "handler.achievement_census.summary",
			slog.Uint64("character_id", uint64(p.CharacterID)),
			slog.Bool("private", summary.Private),
			slog.Int("milestones_found", len(summary.Milestones)),
			slog.Bool("has_latest", summary.LatestAchievement != nil))
	}

	milestones, err := h.census.ProcessMilestoneResults(ctx, p.CharacterID, summary)
	if err != nil {
		h.logger.ErrorContext(ctx, "handler.achievement_census.process_error", slog.Uint64("character_id", uint64(p.CharacterID)), slog.Any("error", err))
		return nil, fmt.Errorf("achievement-census process %d: %w", p.CharacterID, err)
	}

	duration := time.Since(start)
	private := summary != nil && summary.Private
	h.logger.DebugContext(ctx, "handler.achievement_census.complete",
		slog.Uint64("character_id", uint64(p.CharacterID)),
		slog.Int("milestones", len(milestones)),
		slog.Bool("private", private),
		slog.Duration("duration", duration))
	return nil, nil
}

// missingMilestoneIDs returns only absent tracked milestones in canonical order.
// Persisted checkpoints need not be requested again: the sequential client stops
// at the first public unearned checkpoint it receives.
func missingMilestoneIDs(ids map[uint32]bool, known []contract.CharacterMilestone) []uint32 {
	ordered := make([]uint32, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i] < ordered[j] })
	persisted := make(map[uint32]bool, len(known))
	for _, milestone := range known {
		if ids[milestone.AchievementID] {
			persisted[milestone.AchievementID] = true
		}
	}
	missing := make([]uint32, 0, len(ordered))
	for _, id := range ordered {
		if !persisted[id] {
			missing = append(missing, id)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	return missing
}

func latestKnownMilestone(latest *contract.AchievementResult, known []contract.CharacterMilestone) *contract.AchievementResult {
	for _, milestone := range known {
		if latest == nil || milestone.AchievedAt.After(latest.EarnedAt) {
			latest = &contract.AchievementResult{AchievementID: milestone.AchievementID, Earned: true, EarnedAt: milestone.AchievedAt}
		}
	}
	return latest
}

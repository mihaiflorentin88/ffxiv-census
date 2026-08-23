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

// CharacterCensus re-censuses a known character using Lodestone as primary and Tomestone as fallback.
// On success, chains an achievement-census job.
// On confirmed 404 across both providers, marks the character deleted.
type CharacterCensus struct {
	lodestone   contract.LodestoneClient
	tomestone   contract.TomestoneClient
	census      *census.Service
	logger      contract.Logger
	rateLimiter contract.ProviderRateLimiter
}

func NewCharacterCensus(
	lodestone contract.LodestoneClient,
	tomestone contract.TomestoneClient,
	svc *census.Service,
	logger contract.Logger,
	rateLimiter ...contract.ProviderRateLimiter,
) *CharacterCensus {
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	return &CharacterCensus{
		lodestone:   lodestone,
		tomestone:   tomestone,
		census:      svc,
		logger:      loggerOrDiscard(logger),
		rateLimiter: rl,
	}
}

func (h *CharacterCensus) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p CharacterCensusPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("character-census payload: %w", err)
	}
	h.logger.DebugContext(ctx, "Processing character census", slog.Uint64("character_id", uint64(p.CharacterID)))

	lodestoneAvail := h.lodestone != nil && (h.rateLimiter == nil || h.rateLimiter.IsAvailable(contract.ProviderLodestone))
	tomestoneAvail := h.tomestone != nil && h.tomestone.IsConfigured() && (h.rateLimiter == nil || h.rateLimiter.IsAvailable(contract.ProviderTomestone))

	if !lodestoneAvail && !tomestoneAvail {
		return nil, fmt.Errorf("character-census %d: all providers unavailable or rate-limited", p.CharacterID)
	}

	if lodestoneAvail {
		start := time.Now()
		char, err := h.lodestone.FetchCharacter(ctx, p.CharacterID)
		if err == nil {
			h.logger.DebugContext(ctx, "Fetched character from Lodestone", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", char.Name), slog.String("world", char.World), slog.String("datacenter", char.Datacenter), slog.Duration("duration", time.Since(start)))
			if err := h.census.UpsertCharacter(ctx, char); err != nil {
				h.logger.ErrorContext(ctx, "Failed to store character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", char.Name), slog.String("world", char.World), slog.String("source", "lodestone"), slog.Any("error", err))
				return nil, fmt.Errorf("character-census upsert %d: %w", p.CharacterID, err)
			}
			h.logger.DebugContext(ctx, "Stored character in database", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", char.Name), slog.String("world", char.World))
			next := BuildDependentCharacterJobs(char.ID)
			h.logger.DebugContext(ctx, "Character census complete", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", char.Name), slog.String("world", char.World), slog.Int("chained_jobs", len(next)))
			return next, nil
		}

		if errors.Is(err, contract.ErrCharacterNotFound) {
			if tomestoneAvail {
				tomestoneStart := time.Now()
				tChar, terr := h.tomestone.FetchCharacterProfile(ctx, p.CharacterID, false)
				if terr == nil {
					h.logger.DebugContext(ctx, "Fetched character from Tomestone", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("datacenter", tChar.Datacenter), slog.Duration("duration", time.Since(tomestoneStart)))
					if uerr := h.census.UpsertTomestoneCharacter(ctx, tChar); uerr != nil {
						h.logger.ErrorContext(ctx, "Failed to store character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("source", "tomestone"), slog.Any("error", uerr))
						return nil, fmt.Errorf("character-census upsert %d: %w", p.CharacterID, uerr)
					}
					h.logger.DebugContext(ctx, "Stored character in database", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server))
					next := BuildDependentCharacterJobs(tChar.ID)
					h.logger.DebugContext(ctx, "Character census complete", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.Int("chained_jobs", len(next)))
					return next, nil
				}
				if errors.Is(terr, contract.ErrCharacterNotFound) {
					if derr := h.census.MarkCharacterDeleted(ctx, p.CharacterID, time.Now().UTC()); derr != nil {
						h.logger.ErrorContext(ctx, "Failed to store character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("source", "lodestone+tomestone"), slog.Any("error", derr))
						return nil, fmt.Errorf("character-census mark-deleted %d: %w", p.CharacterID, derr)
					}
					h.logger.DebugContext(ctx, "Character marked as deleted", slog.Uint64("character_id", uint64(p.CharacterID)))
					return nil, nil
				}
				h.logger.WarnContext(ctx, "Failed to fetch character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("source", "tomestone"), slog.Any("error", terr))
				return nil, fmt.Errorf("character-census tomestone fetch %d: %w", p.CharacterID, terr)
			}

			// Tomestone unavailable: mark deleted based on Lodestone 404
			if derr := h.census.MarkCharacterDeleted(ctx, p.CharacterID, time.Now().UTC()); derr != nil {
				h.logger.ErrorContext(ctx, "Failed to store character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("source", "lodestone"), slog.Any("error", derr))
				return nil, fmt.Errorf("character-census mark-deleted %d: %w", p.CharacterID, derr)
			}
			h.logger.DebugContext(ctx, "Character marked as deleted", slog.Uint64("character_id", uint64(p.CharacterID)))
			return nil, nil
		}

		// Lodestone returned transient/rate-limit error: try Tomestone fallback if available
		if tomestoneAvail {
			tomestoneStart := time.Now()
			tChar, terr := h.tomestone.FetchCharacterProfile(ctx, p.CharacterID, false)
			if terr == nil {
				h.logger.DebugContext(ctx, "Fetched character from Tomestone", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("datacenter", tChar.Datacenter), slog.Duration("duration", time.Since(tomestoneStart)))
				if uerr := h.census.UpsertTomestoneCharacter(ctx, tChar); uerr != nil {
					h.logger.ErrorContext(ctx, "Failed to store character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("source", "tomestone"), slog.Any("error", uerr))
					return nil, fmt.Errorf("character-census upsert %d: %w", p.CharacterID, uerr)
				}
				h.logger.DebugContext(ctx, "Stored character in database", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server))
				next := BuildDependentCharacterJobs(tChar.ID)
				h.logger.DebugContext(ctx, "Character census complete", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.Int("chained_jobs", len(next)))
				return next, nil
			}
			if errors.Is(terr, contract.ErrCharacterNotFound) {
				h.logger.WarnContext(ctx, "Character not found on Tomestone, retrying with Lodestone", slog.Uint64("character_id", uint64(p.CharacterID)))
				return nil, fmt.Errorf("character-census %d: not found on tomestone and lodestone error (%v), retrying on lodestone", p.CharacterID, err)
			}
			h.logger.WarnContext(ctx, "Failed to fetch character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("source", "tomestone"), slog.Any("error", terr))
			return nil, fmt.Errorf("character-census tomestone fetch %d: %w", p.CharacterID, terr)
		}

		h.logger.WarnContext(ctx, "Failed to fetch character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("source", "lodestone"), slog.Any("error", err))
		return nil, fmt.Errorf("character-census fetch %d: %w", p.CharacterID, err)
	}

	// Lodestone unavailable/paused, but Tomestone is available
	tomestoneStart := time.Now()
	tChar, err := h.tomestone.FetchCharacterProfile(ctx, p.CharacterID, false)
	if err == nil {
		h.logger.DebugContext(ctx, "Fetched character from Tomestone", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("datacenter", tChar.Datacenter), slog.Duration("duration", time.Since(tomestoneStart)))
		if uerr := h.census.UpsertTomestoneCharacter(ctx, tChar); uerr != nil {
			h.logger.ErrorContext(ctx, "Failed to store character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("source", "tomestone"), slog.Any("error", uerr))
			return nil, fmt.Errorf("character-census upsert %d: %w", p.CharacterID, uerr)
		}
		h.logger.DebugContext(ctx, "Stored character in database", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server))
		next := BuildDependentCharacterJobs(tChar.ID)
		h.logger.DebugContext(ctx, "Character census complete", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.Int("chained_jobs", len(next)))
		return next, nil
	}

	if errors.Is(err, contract.ErrCharacterNotFound) {
		h.logger.WarnContext(ctx, "Character not found on Tomestone, retrying with Lodestone", slog.Uint64("character_id", uint64(p.CharacterID)))
		return nil, fmt.Errorf("character-census %d: not found on tomestone and lodestone currently paused/unavailable, retrying on lodestone", p.CharacterID)
	}

	h.logger.WarnContext(ctx, "Failed to fetch character", slog.Uint64("character_id", uint64(p.CharacterID)), slog.String("source", "tomestone"), slog.Any("error", err))
	return nil, fmt.Errorf("character-census tomestone fetch %d: %w", p.CharacterID, err)
}

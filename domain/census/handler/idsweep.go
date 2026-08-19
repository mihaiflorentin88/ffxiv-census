package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

type IDSweepPayload struct {
	From   uint32 `json:"from"`
	To     uint32 `json:"to"`
	Source string `json:"source,omitempty"`
}

// IDSweep probes a range of Lodestone/Tomestone character IDs, ingesting any that exist
// and chaining dependent jobs (achievement-census, and fc-census when affiliated with an FC).
type IDSweep struct {
	lodestone   contract.LodestoneClient
	tomestone   contract.TomestoneClient
	census      *census.Service
	logger      contract.Logger
	rateLimiter contract.ProviderRateLimiter
}

func NewIDSweep(
	lodestone contract.LodestoneClient,
	tomestone contract.TomestoneClient,
	svc *census.Service,
	logger contract.Logger,
	rateLimiter ...contract.ProviderRateLimiter,
) *IDSweep {
	var rl contract.ProviderRateLimiter
	if len(rateLimiter) > 0 {
		rl = rateLimiter[0]
	}
	return &IDSweep{
		lodestone:   lodestone,
		tomestone:   tomestone,
		census:      svc,
		logger:      loggerOrDiscard(logger),
		rateLimiter: rl,
	}
}

func (h *IDSweep) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p IDSweepPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("id-sweep payload: %w", err)
	}
	if p.From > p.To {
		return nil, fmt.Errorf("id-sweep range invalid: from %d > to %d", p.From, p.To)
	}
	h.logger.InfoContext(ctx, "handler.id_sweep.start", slog.Uint64("from", uint64(p.From)), slog.Uint64("to", uint64(p.To)), slog.Uint64("count", uint64(p.To-p.From+1)))

	if p.Source == "tomestone" && (h.tomestone == nil || !h.tomestone.IsConfigured()) {
		return nil, errors.New("tomestone client unconfigured on this worker")
	}
	if p.Source == "lodestone" && h.lodestone == nil {
		return nil, errors.New("lodestone client unconfigured on this worker")
	}
	if (h.tomestone == nil || !h.tomestone.IsConfigured()) && h.lodestone == nil {
		return nil, errors.New("no lodestone or tomestone client available for id-sweep")
	}

	var next []contract.QueueJob
	// Break-based loop (not `id <= p.To`) so id++ never wraps past MaxUint32.
	for id := p.From; ; id++ {
		if p.Source == "tomestone" {
			tChar, err := h.tomestone.FetchCharacterProfile(ctx, id, false)
			if err == nil {
				if uerr := h.census.UpsertTomestoneCharacter(ctx, tChar); uerr != nil {
					h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.Any("error", uerr))
					return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
				}
				h.logger.InfoContext(ctx, "handler.id_sweep.discovered", slog.Uint64("character_id", uint64(id)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("source", "tomestone"))
				fcID := ""
				if tChar.FreeCompanyID != nil {
					fcID = *tChar.FreeCompanyID
				}
				jobs := BuildDependentCharacterJobs(tChar.ID, fcID)
				next = append(next, jobs...)
			} else if !errors.Is(err, contract.ErrCharacterNotFound) {
				h.logger.WarnContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.String("source", "tomestone"), slog.Any("error", err))
				return nil, fmt.Errorf("id-sweep tomestone fetch %d: %w", id, err)
			} else {
				h.logger.InfoContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("source", "tomestone"), slog.String("status", "not_found"))
			}
		} else if p.Source == "lodestone" {
			lChar, err := h.lodestone.FetchCharacter(ctx, id)
			if err == nil {
				if uerr := h.census.UpsertCharacter(ctx, lChar); uerr != nil {
					h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.Any("error", uerr))
					return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
				}
				h.logger.InfoContext(ctx, "handler.id_sweep.discovered", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.String("source", "lodestone"))
				jobs := BuildDependentCharacterJobs(lChar.ID, lChar.FreeCompanyID)
				next = append(next, jobs...)
			} else if !errors.Is(err, contract.ErrCharacterNotFound) {
				h.logger.WarnContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.Any("error", err))
				return nil, fmt.Errorf("id-sweep lodestone fetch %d: %w", id, err)
			} else {
				h.logger.InfoContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.String("status", "not_found"))
			}
		} else {
			// Auto / default: Tomestone primary, Lodestone fallback
			tomestoneAvail := h.tomestone != nil && h.tomestone.IsConfigured() && (h.rateLimiter == nil || h.rateLimiter.IsAvailable(contract.ProviderTomestone))
			lodestoneAvail := h.lodestone != nil && (h.rateLimiter == nil || h.rateLimiter.IsAvailable(contract.ProviderLodestone))

			if !tomestoneAvail && !lodestoneAvail {
				return nil, errors.New("id-sweep: all providers unavailable or rate-limited")
			}

			if tomestoneAvail {
				tChar, err := h.tomestone.FetchCharacterProfile(ctx, id, false)
				if err == nil {
					if uerr := h.census.UpsertTomestoneCharacter(ctx, tChar); uerr != nil {
						h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.Any("error", uerr))
						return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
					}
					h.logger.InfoContext(ctx, "handler.id_sweep.discovered", slog.Uint64("character_id", uint64(id)), slog.String("name", tChar.Name), slog.String("world", tChar.Server), slog.String("source", "tomestone"))
					fcID := ""
					if tChar.FreeCompanyID != nil {
						fcID = *tChar.FreeCompanyID
					}
					jobs := BuildDependentCharacterJobs(tChar.ID, fcID)
					next = append(next, jobs...)
				} else if errors.Is(err, contract.ErrCharacterNotFound) {
					// Tomestone 404: character not indexed, try Lodestone
					if lodestoneAvail {
						lChar, lerr := h.lodestone.FetchCharacter(ctx, id)
						if lerr == nil {
							if uerr := h.census.UpsertCharacter(ctx, lChar); uerr != nil {
								h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.Any("error", uerr))
								return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
							}
							h.logger.InfoContext(ctx, "handler.id_sweep.discovered", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.String("source", "lodestone"))
							jobs := BuildDependentCharacterJobs(lChar.ID, lChar.FreeCompanyID)
							next = append(next, jobs...)
						} else if errors.Is(lerr, contract.ErrCharacterNotFound) {
							h.logger.InfoContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("source", "tomestone+lodestone"), slog.String("status", "not_found"))
						} else {
							h.logger.WarnContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.Any("error", lerr))
							return nil, fmt.Errorf("id-sweep lodestone fetch %d: %w", id, lerr)
						}
					} else {
						// Lodestone unavailable: return error to retry later on Lodestone
						h.logger.WarnContext(ctx, "handler.id_sweep.tomestone_miss_retrying_lodestone", slog.Uint64("character_id", uint64(id)))
						return nil, fmt.Errorf("id-sweep %d: not found on tomestone and lodestone currently paused/unavailable, retrying on lodestone", id)
					}
				} else {
					// Tomestone transient error: fall back to Lodestone
					if lodestoneAvail {
						lChar, lerr := h.lodestone.FetchCharacter(ctx, id)
						if lerr == nil {
							if uerr := h.census.UpsertCharacter(ctx, lChar); uerr != nil {
								h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.Any("error", uerr))
								return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
							}
							h.logger.InfoContext(ctx, "handler.id_sweep.discovered", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.String("source", "lodestone"))
							jobs := BuildDependentCharacterJobs(lChar.ID, lChar.FreeCompanyID)
							next = append(next, jobs...)
						} else if errors.Is(lerr, contract.ErrCharacterNotFound) {
							// Lodestone is authoritative for existence: if it says 404, character doesn't exist.
							h.logger.InfoContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("source", "tomestone+lodestone"), slog.String("status", "not_found"))
						} else {
							h.logger.WarnContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.Any("error", lerr))
							return nil, fmt.Errorf("id-sweep lodestone fetch %d: %w", id, lerr)
						}
					} else {
						h.logger.WarnContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.String("source", "tomestone"), slog.Any("error", err))
						return nil, fmt.Errorf("id-sweep tomestone fetch %d: %w", id, err)
					}
				}
			} else {
				// Tomestone unavailable/paused, Lodestone available: probe Lodestone directly
				lChar, err := h.lodestone.FetchCharacter(ctx, id)
				if err == nil {
					if uerr := h.census.UpsertCharacter(ctx, lChar); uerr != nil {
						h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.Any("error", uerr))
						return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
					}
					h.logger.InfoContext(ctx, "handler.id_sweep.discovered", slog.Uint64("character_id", uint64(id)), slog.String("name", lChar.Name), slog.String("world", lChar.World), slog.String("source", "lodestone"))
					jobs := BuildDependentCharacterJobs(lChar.ID, lChar.FreeCompanyID)
					next = append(next, jobs...)
				} else if errors.Is(err, contract.ErrCharacterNotFound) {
					h.logger.InfoContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.String("status", "not_found"))
				} else {
					h.logger.WarnContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.Any("error", err))
					return nil, fmt.Errorf("id-sweep lodestone fetch %d: %w", id, err)
				}
			}
		}

		if id == p.To {
			break
		}
	}
	h.logger.InfoContext(ctx, "handler.id_sweep.done", slog.Uint64("from", uint64(p.From)), slog.Uint64("to", uint64(p.To)), slog.Int("discovered", len(next)))
	return next, nil
}

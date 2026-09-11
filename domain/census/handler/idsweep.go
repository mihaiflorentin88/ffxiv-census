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

// IDSweep probes a range of Lodestone character IDs, ingesting any that exist
// and chaining dependent jobs (achievement-census). The Lodestone is the sole
// data source and the sole authority for existence.
type IDSweep struct {
	lodestone contract.LodestoneClient
	census    *census.Service
	logger    contract.Logger
}

func NewIDSweep(
	lodestone contract.LodestoneClient,
	svc *census.Service,
	logger contract.Logger,
) *IDSweep {
	return &IDSweep{
		lodestone: lodestone,
		census:    svc,
		logger:    loggerOrDiscard(logger),
	}
}

func (h *IDSweep) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p IDSweepPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, errors.Join(contract.ErrPoisonPayload, fmt.Errorf("id-sweep payload: %w", err))
	}
	if p.From > p.To {
		return nil, errors.Join(contract.ErrPoisonPayload, fmt.Errorf("id-sweep range invalid: from %d > to %d", p.From, p.To))
	}
	// The Lodestone is the only data source; the source field on legacy
	// payloads (e.g. "tomestone") is informational and behaves as auto.
	h.logger.InfoContext(ctx, "handler.id_sweep.start", slog.Uint64("from", uint64(p.From)), slog.Uint64("to", uint64(p.To)), slog.Uint64("count", uint64(p.To-p.From+1)))

	if h.lodestone == nil {
		return nil, errors.New("lodestone client unconfigured on this worker")
	}

	var next []contract.QueueJob
	// Break-based loop (not `id <= p.To`) so id++ never wraps past MaxUint32.
	for id := p.From; ; id++ {
		lChar, err := h.lodestone.FetchCharacter(ctx, id)
		if err == nil {
			jobs, serr := h.storeLodestone(ctx, id, lChar)
			if serr != nil {
				return nil, serr
			}
			next = append(next, jobs...)
		} else if errors.Is(err, contract.ErrCharacterNotFound) {
			if h.logger.Enabled(ctx, slog.LevelDebug) {
				h.logger.DebugContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.String("status", "not_found"))
			}
		} else {
			// Transient Lodestone error (challenge, timeout, 429, dial):
			// fail the delivery so the queue's retry ladder re-probes it
			// on Lodestone. Never skip an id on anything but a genuine 404.
			h.logger.ErrorContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.Any("error", err))
			return nil, fmt.Errorf("id-sweep lodestone fetch %d: %w", id, err)
		}

		if id == p.To {
			break
		}
	}
	h.logger.InfoContext(ctx, "handler.id_sweep.done", slog.Uint64("from", uint64(p.From)), slog.Uint64("to", uint64(p.To)), slog.Int("discovered", len(next)))
	return next, nil
}

// storeLodestone persists a discovered Lodestone character and returns its
// dependent jobs. Hidden profiles (census.ErrProfileHidden) are skipped: the
// character exists but has no censusable demographics, so nothing is stored
// and no jobs are chained.
func (h *IDSweep) storeLodestone(ctx context.Context, id uint32, char *contract.CharacterProfile) ([]contract.QueueJob, error) {
	if err := h.census.UpsertCharacter(ctx, char); err != nil {
		if errors.Is(err, census.ErrProfileHidden) {
			h.logger.InfoContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("source", "lodestone"), slog.String("status", "profile_hidden"))
			return nil, nil
		}
		h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", char.Name), slog.String("world", char.World), slog.Any("error", err))
		return nil, fmt.Errorf("id-sweep upsert %d: %w", id, err)
	}
	h.logger.InfoContext(ctx, "handler.id_sweep.discovered", slog.Uint64("character_id", uint64(id)), slog.String("name", char.Name), slog.String("world", char.World), slog.String("source", "lodestone"))
	return BuildDependentCharacterJobs(char.ID), nil
}

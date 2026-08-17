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

// IDSweepPayload is the payload of an id-sweep job: an inclusive ID range to probe.
type IDSweepPayload struct {
	From uint32 `json:"from"`
	To   uint32 `json:"to"`
}

// IDSweep probes a range of Lodestone character IDs, ingesting any that exist
// and chaining an achievement-census job for each discovery.
type IDSweep struct {
	lodestone contract.LodestoneClient
	census    *census.Service
	logger    contract.Logger
}

func NewIDSweep(lodestone contract.LodestoneClient, svc *census.Service, logger contract.Logger) *IDSweep {
	return &IDSweep{lodestone: lodestone, census: svc, logger: loggerOrDiscard(logger)}
}

func (h *IDSweep) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p IDSweepPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("id-sweep payload: %w", err)
	}
	if p.From > p.To {
		return nil, fmt.Errorf("id-sweep range invalid: from %d > to %d", p.From, p.To)
	}
	h.logger.InfoContext(ctx, "handler.id_sweep", slog.Uint64("from", uint64(p.From)), slog.Uint64("to", uint64(p.To)))

	var next []contract.QueueJob
	// Break-based loop (not `id <= p.To`) so id++ never wraps past MaxUint32.
	for id := p.From; ; id++ {
		char, err := h.lodestone.FetchCharacter(ctx, id)
		if err == nil {
			if uerr := h.census.UpsertCharacter(ctx, char); uerr != nil {
				h.logger.ErrorContext(ctx, "handler.id_sweep.store_error", slog.Uint64("character_id", uint64(id)), slog.String("name", char.Name), slog.String("world", char.World), slog.Any("error", uerr))
				return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
			}
			h.logger.DebugContext(ctx, "handler.id_sweep.stored", slog.Uint64("character_id", uint64(id)), slog.String("name", char.Name), slog.String("world", char.World))
			next = append(next, AchievementCensusJob(id))
		} else if !errors.Is(err, contract.ErrCharacterNotFound) {
			h.logger.WarnContext(ctx, "handler.id_sweep.fetch_error", slog.Uint64("character_id", uint64(id)), slog.Any("error", err))
			return nil, fmt.Errorf("id-sweep fetch %d: %w", id, err)
		} else {
			h.logger.DebugContext(ctx, "handler.id_sweep.probe", slog.Uint64("character_id", uint64(id)), slog.String("result", "not_found"))
		}
		if id == p.To {
			break
		}
	}
	h.logger.InfoContext(ctx, "handler.id_sweep.done", slog.Uint64("from", uint64(p.From)), slog.Uint64("to", uint64(p.To)), slog.Int("discovered", len(next)))
	return next, nil
}

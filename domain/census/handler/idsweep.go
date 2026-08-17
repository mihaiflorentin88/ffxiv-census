package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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
}

func NewIDSweep(lodestone contract.LodestoneClient, svc *census.Service) *IDSweep {
	return &IDSweep{lodestone: lodestone, census: svc}
}

func (h *IDSweep) Handle(ctx context.Context, payload []byte) ([]contract.QueueJob, error) {
	var p IDSweepPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return nil, fmt.Errorf("id-sweep payload: %w", err)
	}
	if p.From > p.To {
		return nil, fmt.Errorf("id-sweep range invalid: from %d > to %d", p.From, p.To)
	}

	var next []contract.QueueJob
	// Break-based loop (not `id <= p.To`) so id++ never wraps past MaxUint32.
	for id := p.From; ; id++ {
		char, err := h.lodestone.FetchCharacter(ctx, id)
		if err == nil {
			if uerr := h.census.UpsertCharacter(ctx, char); uerr != nil {
				return nil, fmt.Errorf("id-sweep upsert %d: %w", id, uerr)
			}
			next = append(next, AchievementCensusJob(id))
		} else if !errors.Is(err, contract.ErrCharacterNotFound) {
			return nil, fmt.Errorf("id-sweep fetch %d: %w", id, err)
		}
		if id == p.To {
			break
		}
	}
	return next, nil
}

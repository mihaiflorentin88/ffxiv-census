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

	var next []contract.QueueJob
	for id := p.From; id <= p.To; id++ {
		char, err := h.lodestone.FetchCharacter(ctx, id)
		if errors.Is(err, contract.ErrCharacterNotFound) {
			continue // doesn't exist
		}
		if err != nil {
			return nil, fmt.Errorf("id-sweep fetch %d: %w", id, err)
		}
		if err := h.census.UpsertCharacter(ctx, char); err != nil {
			return nil, fmt.Errorf("id-sweep upsert %d: %w", id, err)
		}
		next = append(next, AchievementCensusJob(id))
	}
	return next, nil
}

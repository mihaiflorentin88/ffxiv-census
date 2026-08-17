package handler

import (
	"encoding/json"

	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// Event types carried as queue job "type" strings.
const (
	EventIDSweep           = "id-sweep"
	EventCharacterCensus   = "character-census"
	EventAchievementCensus = "achievement-census"
	EventFreeCompanyCensus = "fc-census"
)

// AchievementCensusPayload identifies a character to run an achievement census on.
type AchievementCensusPayload struct {
	CharacterID uint32 `json:"character_id"`
}

// AchievementCensusJob builds an achievement-census queue job for a character.
func AchievementCensusJob(characterID uint32) contract.QueueJob {
	b, _ := json.Marshal(AchievementCensusPayload{CharacterID: characterID})
	return contract.QueueJob{Type: EventAchievementCensus, Payload: b}
}

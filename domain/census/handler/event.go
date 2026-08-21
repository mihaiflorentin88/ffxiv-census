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

// CharacterCensusPayload identifies a character to re-census.
type CharacterCensusPayload struct {
	CharacterID uint32 `json:"character_id"`
}

// CharacterCensusJob builds a character-census queue job for a character.
func CharacterCensusJob(characterID uint32) contract.QueueJob {
	b, _ := json.Marshal(CharacterCensusPayload{CharacterID: characterID})
	return contract.QueueJob{Type: EventCharacterCensus, Payload: b}
}

// BuildDependentCharacterJobs creates downstream jobs for an ingested character:
// an achievement-census job.
func BuildDependentCharacterJobs(characterID uint32) []contract.QueueJob {
	return []contract.QueueJob{AchievementCensusJob(characterID)}
}

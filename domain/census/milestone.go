package census

import "github.com/mihaiflorentin88/ffxiv-census/port/contract"

func expansionPtr(s string) *string { return &s }

// MilestoneSet is the canonical registry of achievements the census tracks.
// Achievement IDs are game achievement IDs (verified against ffxivcollect /
// XIVAPI achievement data), matching godestone's AchievementInfo.ID. Additive
// only: append new milestones here; they are synced to the DB idempotently via
// AchievementRepository.SyncMilestones (INSERT OR IGNORE).
var MilestoneSet = []contract.MilestoneAchievement{
	// Chocobo (verified: XIVAPI sheet 590).
	{AchievementID: 590, Kind: contract.MilestoneKindChocobo, Detail: "My Little Chocobo"},

	// Expansion MSQ completions (verified against ffxivcollect achievement data).
	// Each is the achievement for completing the expansion's final main-scenario
	// quest (A Realm Reborn: 1129 "My Left Arm" / "Before the Dawn"; Heavensward: 1139 "Looking Up"; etc.).
	{AchievementID: 1129, Kind: contract.MilestoneKindExpansion, Expansion: expansionPtr("A Realm Reborn"), Detail: "My Left Arm"},
	{AchievementID: 1139, Kind: contract.MilestoneKindExpansion, Expansion: expansionPtr("Heavensward"), Detail: "Looking Up"},
	{AchievementID: 1794, Kind: contract.MilestoneKindExpansion, Expansion: expansionPtr("Stormblood"), Detail: "The Measure of His Reach"},
	{AchievementID: 2298, Kind: contract.MilestoneKindExpansion, Expansion: expansionPtr("Shadowbringers"), Detail: "Shadowbringers"},
	{AchievementID: 2958, Kind: contract.MilestoneKindExpansion, Expansion: expansionPtr("Endwalker"), Detail: "That Its Chorus Might Ring for All"},
	{AchievementID: 3496, Kind: contract.MilestoneKindExpansion, Expansion: expansionPtr("Dawntrail"), Detail: "In the Glow of a New Dawn"},
}

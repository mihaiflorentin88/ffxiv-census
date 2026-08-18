package census

import "github.com/mihaiflorentin88/ffxiv-census/port/contract"

func expansionPtr(s string) *string { return &s }

// ExpansionConfig defines metadata and milestone achievement for an expansion.
type ExpansionConfig struct {
	Name          string
	Version       string
	FinalQuest    string
	Icon          string
	LevelCap      uint32
	AchievementID uint32
}

// DefaultExpansions is the canonical default list of storyline expansions.
var DefaultExpansions = []ExpansionConfig{
	{Name: "A Realm Reborn", Version: "Patch 2.55", FinalQuest: "Before the Dawn", Icon: "🌱", LevelCap: 50, AchievementID: 1129},
	{Name: "Heavensward", Version: "Patch 3.0", FinalQuest: "Looking Up", Icon: "❄️", LevelCap: 60, AchievementID: 1139},
	{Name: "Stormblood", Version: "Patch 4.0", FinalQuest: "The Measure of His Reach", Icon: "⚔️", LevelCap: 70, AchievementID: 1794},
	{Name: "Shadowbringers", Version: "Patch 5.0", FinalQuest: "Shadowbringers", Icon: "🌑", LevelCap: 80, AchievementID: 2298},
	{Name: "Endwalker", Version: "Patch 6.0", FinalQuest: "That Its Chorus Might Ring for All", Icon: "🌕", LevelCap: 90, AchievementID: 2958},
	{Name: "Dawntrail", Version: "Patch 7.0", FinalQuest: "In the Glow of a New Dawn", Icon: "☀️", LevelCap: 100, AchievementID: 3496},
}

// BuildMilestones constructs milestone achievement records for a set of expansions,
// including the static Chocobo milestone (590).
func BuildMilestones(expansions []ExpansionConfig) []contract.MilestoneAchievement {
	milestones := []contract.MilestoneAchievement{
		{AchievementID: 590, Kind: contract.MilestoneKindChocobo, Detail: "My Little Chocobo"},
	}
	for _, exp := range expansions {
		if exp.AchievementID > 0 {
			detail := exp.FinalQuest
			if exp.Name == "A Realm Reborn" && detail == "Before the Dawn" {
				detail = "My Left Arm" // Achievement name in game
			}
			milestones = append(milestones, contract.MilestoneAchievement{
				AchievementID: exp.AchievementID,
				Kind:          contract.MilestoneKindExpansion,
				Expansion:     expansionPtr(exp.Name),
				Detail:        detail,
			})
		}
	}
	return milestones
}

// DefaultMilestones returns the canonical milestones generated from DefaultExpansions.
func DefaultMilestones() []contract.MilestoneAchievement {
	return BuildMilestones(DefaultExpansions)
}

// MilestoneSet is maintained for backward compatibility.
var MilestoneSet = DefaultMilestones()

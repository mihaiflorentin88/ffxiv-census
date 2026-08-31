package ui

import (
	"sort"
	"strings"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
)

// worldDatacenter maps standard FFXIV worlds to their logical datacenters.
var worldDatacenter = map[string]string{
	// NA - Aether
	"Adamantoise":  "Aether",
	"Cactuar":      "Aether",
	"Faerie":       "Aether",
	"Gilgamesh":    "Aether",
	"Jenova":       "Aether",
	"Midgardsormr": "Aether",
	"Sargatanas":   "Aether",
	"Siren":        "Aether",

	// NA - Primal
	"Behemoth":  "Primal",
	"Excalibur": "Primal",
	"Exodus":    "Primal",
	"Famfrit":   "Primal",
	"Hyperion":  "Primal",
	"Lamia":     "Primal",
	"Leviathan": "Primal",
	"Ultros":    "Primal",

	// NA - Crystal
	"Balmung":   "Crystal",
	"Brynhildr": "Crystal",
	"Coeurl":    "Crystal",
	"Diabolos":  "Crystal",
	"Goblin":    "Crystal",
	"Malboro":   "Crystal",
	"Mateus":    "Crystal",
	"Zalera":    "Crystal",

	// NA - Dynamis
	"Cuchulainn":    "Dynamis",
	"Golem":         "Dynamis",
	"Halicarnassus": "Dynamis",
	"Kraken":        "Dynamis",
	"Maduin":        "Dynamis",
	"Marilith":      "Dynamis",
	"Rafflesia":     "Dynamis",
	"Seraph":        "Dynamis",

	// EU - Chaos
	"Cerberus":    "Chaos",
	"Louisoix":    "Chaos",
	"Moogle":      "Chaos",
	"Omega":       "Chaos",
	"Phantom":     "Chaos",
	"Ragnarok":    "Chaos",
	"Sagittarius": "Chaos",
	"Spriggan":    "Chaos",

	// EU - Light
	"Alpha":     "Light",
	"Lich":      "Light",
	"Odin":      "Light",
	"Phoenix":   "Light",
	"Raiden":    "Light",
	"Shiva":     "Light",
	"Twintania": "Light",
	"Zodiark":   "Light",

	// JP - Elemental
	"Aegis":     "Elemental",
	"Atomos":    "Elemental",
	"Carbuncle": "Elemental",
	"Garuda":    "Elemental",
	"Gungnir":   "Elemental",
	"Kujata":    "Elemental",
	"Tonberry":  "Elemental",
	"Typhon":    "Elemental",

	// JP - Gaia
	"Alexander": "Gaia",
	"Bahamut":   "Gaia",
	"Durandal":  "Gaia",
	"Fenrir":    "Gaia",
	"Ifrit":     "Gaia",
	"Ridill":    "Gaia",
	"Tiamat":    "Gaia",
	"Ultima":    "Gaia",

	// JP - Mana
	"Anima":        "Mana",
	"Asura":        "Mana",
	"Chocobo":      "Mana",
	"Hades":        "Mana",
	"Ixion":        "Mana",
	"Masamune":     "Mana",
	"Pandaemonium": "Mana",
	"Titan":        "Mana",

	// JP - Meteor
	"Belias":     "Meteor",
	"Mandragora": "Meteor",
	"Ramuh":      "Meteor",
	"Shinryu":    "Meteor",
	"Unicorn":    "Meteor",
	"Valefor":    "Meteor",
	"Yojimbo":    "Meteor",
	"Zeromus":    "Meteor",

	// OCE - Materia
	"Bismarck": "Materia",
	"Ravana":   "Materia",
	"Sephirot": "Materia",
	"Sophia":   "Materia",
	"Zurvan":   "Materia",
}

// DCsForRegion returns sorted datacenter names belonging to the given region.
// Returns nil for empty or unknown region input.
func DCsForRegion(region string) []string {
	if region == "" {
		return nil
	}
	seen := make(map[string]bool)
	for _, dc := range worldDatacenter {
		if dc != "" && strings.EqualFold(census.RegionForDatacenter(dc), region) {
			seen[dc] = true
		}
	}
	if len(seen) == 0 {
		return nil
	}
	result := make([]string, 0, len(seen))
	for dc := range seen {
		result = append(result, dc)
	}
	sort.Strings(result)
	return result
}

// WorldsForDC returns sorted world names belonging to the given datacenter.
// Returns nil for empty or unknown datacenter input.
func WorldsForDC(dc string) []string {
	if dc == "" {
		return nil
	}
	var result []string
	for world, worldDC := range worldDatacenter {
		if strings.EqualFold(worldDC, dc) {
			result = append(result, world)
		}
	}
	if len(result) == 0 {
		return nil
	}
	sort.Strings(result)
	return result
}

// WorldToDC returns the logical datacenter for a world, or "Unknown" if not found.
func WorldToDC(world string) string {
	if dc, ok := worldDatacenter[world]; ok {
		return dc
	}
	// Case-insensitive lookup fallback
	for w, dc := range worldDatacenter {
		if strings.EqualFold(w, world) {
			return dc
		}
	}
	return "Unknown"
}

// isIndexableWorld reports whether a snapshot world key names a world that is
// part of the known world hierarchy and therefore gets an indexable page.
func isIndexableWorld(world string) bool {
	return world != "" && census.RegionForDatacenter(WorldToDC(world)) != ""
}

// WorldToRegion returns the region (NA, EU, JP, OCE) for a world, or "Unknown" if not found.
func WorldToRegion(world string) string {
	dc := WorldToDC(world)
	if dc == "Unknown" {
		return "Unknown"
	}
	reg := census.RegionForDatacenter(dc)
	if reg == "" {
		return "Unknown"
	}
	return reg
}

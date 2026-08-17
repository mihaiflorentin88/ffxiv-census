package ui

import (
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

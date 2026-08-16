package census

// datacenterRegion maps each FFXIV logical datacenter to its physical region.
// World counts roll up to datacenter, datacenter rolls up to region.
var datacenterRegion = map[string]string{
	"Aether":    "NA",
	"Primal":    "NA",
	"Crystal":   "NA",
	"Dynamis":   "NA",
	"Chaos":     "EU",
	"Light":     "EU",
	"Elemental": "JP",
	"Gaia":      "JP",
	"Mana":      "JP",
	"Meteor":    "JP",
	"Materia":   "OCE",
}

// RegionForDatacenter returns the region (NA/EU/JP/OCE) for a datacenter name,
// or "" when unknown.
func RegionForDatacenter(dc string) string {
	return datacenterRegion[dc]
}

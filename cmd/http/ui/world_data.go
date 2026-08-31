package ui

import (
	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
)

// The world hierarchy (world → datacenter → region) lives in the domain.
// These wrappers preserve the UI-facing helpers and their display fallbacks.

// DCsForRegion returns sorted datacenter names belonging to the given region.
// Returns nil for empty or unknown region input.
func DCsForRegion(region string) []string {
	return census.DatacentersForRegion(region)
}

// WorldsForDC returns sorted world names belonging to the given datacenter.
// Returns nil for empty or unknown datacenter input.
func WorldsForDC(dc string) []string {
	return census.WorldsForDatacenter(dc)
}

// WorldToDC returns the logical datacenter for a world, or "Unknown" if not found.
func WorldToDC(world string) string {
	if dc := census.WorldDatacenter(world); dc != "" {
		return dc
	}
	return "Unknown"
}

// isIndexableWorld reports whether a snapshot world key names a world that is
// part of the known world hierarchy and therefore gets an indexable page.
func isIndexableWorld(world string) bool {
	return world != "" && census.RegionForDatacenter(WorldToDC(world)) != ""
}

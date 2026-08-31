package ui

import (
	"net/http"
	"sort"
	"strings"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// WorldsViewData holds world demographics, filter state, and lists for /ui/worlds.
type WorldsViewData struct {
	TotalCharacters    int64
	ActiveCharacters   int64
	NewCharacters30d   int64
	NewCharactersTrend string
	SelectedRegion     string
	SelectedDC         string
	Regions            []string
	Datacenters        []string
	Worlds             []WorldDetailRow
}

// WorldDetailRow represents one row in the global worlds table.
type WorldDetailRow struct {
	World            string
	Datacenter       string
	Region           string
	Total            int64
	Active           int64
	ActiveRatio      string
	ActivePctVal     float64
	NewCharacters30d int64
}

// Worlds handles GET /ui/worlds.
func (c *UIController) Worlds(w http.ResponseWriter, r *http.Request) {
	snapshot, state, ok := c.currentStats(w, r)
	if !ok {
		return
	}
	selectedRegion := strings.TrimSpace(r.URL.Query().Get("region"))
	selectedDC := strings.TrimSpace(r.URL.Query().Get("dc"))

	var allWorlds []WorldDetailRow
	var totalChars, activeChars int64

	regionSet := make(map[string]bool)
	dcSet := make(map[string]bool)

	for _, row := range census.SnapshotGroups(snapshot, "world", contract.StatsScope{}) {
		wName := row.Key
		if !isIndexableWorld(wName) {
			continue // skip unassigned worlds and worlds outside the known hierarchy
		}
		wDC := WorldToDC(wName)
		wReg := census.RegionForDatacenter(wDC)

		regionSet[wReg] = true
		dcSet[wDC] = true

		var actPctVal float64
		if row.Total > 0 {
			actPctVal = (float64(row.Active) / float64(row.Total)) * 100
		}

		allWorlds = append(allWorlds, WorldDetailRow{
			World:            wName,
			Datacenter:       wDC,
			Region:           wReg,
			Total:            row.Total,
			Active:           row.Active,
			ActiveRatio:      formatPercent(row.Active, row.Total),
			ActivePctVal:     actPctVal,
			NewCharacters30d: census.NewCharactersWindow(snapshot, []string{wName}).Current,
		})
	}

	// Filter
	var filteredWorlds []WorldDetailRow
	for _, wRow := range allWorlds {
		if selectedRegion != "" && !strings.EqualFold(wRow.Region, selectedRegion) {
			continue
		}
		if selectedDC != "" && !strings.EqualFold(wRow.Datacenter, selectedDC) {
			continue
		}
		filteredWorlds = append(filteredWorlds, wRow)
		totalChars += wRow.Total
		activeChars += wRow.Active
	}

	sort.Slice(filteredWorlds, func(i, j int) bool {
		return filteredWorlds[i].Total > filteredWorlds[j].Total
	})

	var regionList []string
	for reg := range regionSet {
		regionList = append(regionList, reg)
	}
	sort.Strings(regionList)

	var dcList []string
	if selectedRegion != "" {
		dcList = DCsForRegion(selectedRegion)
	} else {
		for dc := range dcSet {
			dcList = append(dcList, dc)
		}
		sort.Strings(dcList)
	}

	var trendWorlds []string
	switch {
	case selectedDC != "":
		trendWorlds = WorldsForDC(selectedDC)
	case selectedRegion != "":
		for world := range worldDatacenter {
			if strings.EqualFold(census.RegionForDatacenter(WorldToDC(world)), selectedRegion) {
				trendWorlds = append(trendWorlds, world)
			}
		}
		sort.Strings(trendWorlds)
	}
	newWindow := census.NewCharactersWindow(snapshot, trendWorlds)

	c.render(w, "templates/worlds.html", statsPageData("Worlds Census", "worlds", "/ui/worlds", "Final Fantasy XIV world rankings: total and active character counts for every world and datacenter, with region filters and 30-day activity ratios.", state, WorldsViewData{
		TotalCharacters:    totalChars,
		ActiveCharacters:   activeChars,
		NewCharacters30d:   newWindow.Current,
		NewCharactersTrend: formatTrend(newWindow.Current, newWindow.Previous),
		SelectedRegion:     selectedRegion,
		SelectedDC:         selectedDC,
		Regions:            regionList,
		Datacenters:        dcList,
		Worlds:             filteredWorlds,
	}))
}

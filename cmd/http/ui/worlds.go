package ui

import (
	"net/http"
	"sort"
	"strings"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
)

// WorldsViewData holds world demographics, filter state, and lists for /ui/worlds.
type WorldsViewData struct {
	TotalCharacters  int64
	ActiveCharacters int64
	SelectedRegion   string
	SelectedDC       string
	Regions          []string
	Datacenters      []string
	Worlds           []WorldDetailRow
}

// WorldDetailRow represents one row in the global worlds table.
type WorldDetailRow struct {
	World        string
	Datacenter   string
	Region       string
	Total        int64
	Active       int64
	ActiveRatio  string
	ActivePctVal float64
}

// Worlds handles GET /ui/worlds.
func (c *UIController) Worlds(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	selectedRegion := strings.TrimSpace(r.URL.Query().Get("region"))
	selectedDC := strings.TrimSpace(r.URL.Query().Get("dc"))

	var allWorlds []WorldDetailRow
	var totalChars, activeChars int64

	regionSet := make(map[string]bool)
	dcSet := make(map[string]bool)

	if c.svc != nil {
		breakdown, err := c.svc.Breakdown(ctx, "world")
		if err != nil {
			logging.Error("ui.worlds.breakdown", err.Error())
		} else {
			for _, row := range breakdown {
				wName := row.Key
				wDC := WorldToDC(wName)
				wReg := census.RegionForDatacenter(wDC)
				if wReg == "" {
					wReg = "Unknown"
				}

				regionSet[wReg] = true
				dcSet[wDC] = true

				var actPctVal float64
				if row.Total > 0 {
					actPctVal = (float64(row.Active) / float64(row.Total)) * 100
				}

				allWorlds = append(allWorlds, WorldDetailRow{
					World:        wName,
					Datacenter:   wDC,
					Region:       wReg,
					Total:        row.Total,
					Active:       row.Active,
					ActiveRatio:  formatPercent(row.Active, row.Total),
					ActivePctVal: actPctVal,
				})
			}
		}
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

	c.render(w, "templates/worlds.html", PageData{
		Title:     "Worlds Census",
		ActiveNav: "worlds",
		Data: WorldsViewData{
			TotalCharacters:  totalChars,
			ActiveCharacters: activeChars,
			SelectedRegion:   selectedRegion,
			SelectedDC:       selectedDC,
			Regions:          regionList,
			Datacenters:      dcList,
			Worlds:           filteredWorlds,
		},
	})
}

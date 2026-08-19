package ui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// RaceRow represents demographic stats for one playable race.
type RaceRow struct {
	Race            string
	Total           int64
	Active          int64
	ActiveRatio     string
	ShareOfTotal    string
	SharePercentVal float64
}

// RacesViewData holds race demographics, filter state, and chart data for /ui/races.
type RacesViewData struct {
	TotalCharacters  int64
	ActiveCharacters int64
	SelectedRegion   string
	SelectedDC       string
	SelectedWorld    string
	Regions          []string
	Datacenters      []string
	Worlds           []string
	Races            []RaceRow
	ChartLabels      []string
	ChartData        []int64
}

// Races handles GET /ui/races.
func (c *UIController) Races(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	selectedRegion := strings.TrimSpace(r.URL.Query().Get("region"))
	selectedDC := strings.TrimSpace(r.URL.Query().Get("dc"))
	selectedWorld := strings.TrimSpace(r.URL.Query().Get("world"))

	filter := contract.CharacterFilter{
		Region:     selectedRegion,
		Datacenter: selectedDC,
		World:      selectedWorld,
	}

	var totalChars, activeChars int64
	var raceRows []RaceRow
	var chartLabels []string
	var chartData []int64

	if c.svc != nil {
		if cnt, err := c.svc.Breakdown(ctx, "race", filter); err == nil {
			for _, row := range cnt {
				totalChars += row.Total
				activeChars += row.Active
			}
			for _, row := range cnt {
				rName := row.Key
				if rName == "" {
					rName = "Unknown"
				}

				var sharePctVal float64
				if totalChars > 0 {
					sharePctVal = (float64(row.Total) / float64(totalChars)) * 100
				}

				raceRows = append(raceRows, RaceRow{
					Race:            rName,
					Total:           row.Total,
					Active:          row.Active,
					ActiveRatio:     formatPercent(row.Active, row.Total),
					ShareOfTotal:    formatPercent(row.Total, totalChars),
					SharePercentVal: sharePctVal,
				})

				chartLabels = append(chartLabels, rName)
				chartData = append(chartData, row.Total)
			}
		} else {
			logging.Error("ui.races.breakdown", err.Error())
		}
	}

	sort.Slice(raceRows, func(i, j int) bool {
		return raceRows[i].Total > raceRows[j].Total
	})

	// Build cascading filter lists: Region narrows DCs, DC narrows Worlds.
	var dcList []string
	if selectedRegion != "" {
		dcList = DCsForRegion(selectedRegion)
	} else {
		dcSet := make(map[string]bool)
		for _, dc := range worldDatacenter {
			if dc != "" {
				dcSet[dc] = true
			}
		}
		for dc := range dcSet {
			dcList = append(dcList, dc)
		}
		sort.Strings(dcList)
	}

	var worldList []string
	if selectedDC != "" {
		worldList = WorldsForDC(selectedDC)
	} else if selectedRegion != "" {
		for _, dc := range DCsForRegion(selectedRegion) {
			worldList = append(worldList, WorldsForDC(dc)...)
		}
		sort.Strings(worldList)
	} else {
		worldSet := make(map[string]bool)
		for w := range worldDatacenter {
			worldSet[w] = true
		}
		for w := range worldSet {
			worldList = append(worldList, w)
		}
		sort.Strings(worldList)
	}

	title := "Race Distribution"
	if selectedWorld != "" {
		title = fmt.Sprintf("Race Distribution - %s", selectedWorld)
	} else if selectedDC != "" {
		title = fmt.Sprintf("Race Distribution - %s DC", selectedDC)
	} else if selectedRegion != "" {
		title = fmt.Sprintf("Race Distribution - %s", selectedRegion)
	}

	regionList := []string{"NA", "EU", "JP", "OCE"}

	c.render(w, "templates/races.html", PageData{
		Title:     title,
		ActiveNav: "races",
		Data: RacesViewData{
			TotalCharacters:  totalChars,
			ActiveCharacters: activeChars,
			SelectedRegion:   selectedRegion,
			SelectedDC:       selectedDC,
			SelectedWorld:    selectedWorld,
			Regions:          regionList,
			Datacenters:      dcList,
			Worlds:           worldList,
			Races:            raceRows,
			ChartLabels:      chartLabels,
			ChartData:        chartData,
		},
	})
}

package ui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

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
	TribeLabels      []string
	TribeData        []int64
	GenderLabels     []string
	GenderData       []int64
	RaceGenderLabels []string
	RaceGenderData   []int64
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
	var tribeLabels []string
	var tribeData []int64
	var genderLabels []string
	var genderData []int64
	var raceGenderLabels []string
	var raceGenderData []int64

	if c.svc != nil {
		var wg sync.WaitGroup
		var mu sync.Mutex

		// Goroutine 1: Race breakdown
		wg.Add(1)
		go func() {
			defer wg.Done()
			cnt, err := c.svc.Breakdown(ctx, "race", filter)
			if err != nil {
				logging.Error("ui.races.breakdown", err.Error())
				return
			}
			var rows []RaceRow
			var tChars, aChars int64
			for _, row := range cnt {
				tChars += row.Total
				aChars += row.Active
			}
			for _, row := range cnt {
				rName := row.Key
				if rName == "" {
					rName = "Unknown"
				}
				var sharePctVal float64
				if tChars > 0 {
					sharePctVal = (float64(row.Total) / float64(tChars)) * 100
				}
				rows = append(rows, RaceRow{
					Race:            rName,
					Total:           row.Total,
					Active:          row.Active,
					ActiveRatio:     formatPercent(row.Active, row.Total),
					ShareOfTotal:    formatPercent(row.Total, tChars),
					SharePercentVal: sharePctVal,
				})
			}
			sort.Slice(rows, func(i, j int) bool {
				return rows[i].Total > rows[j].Total
			})
			var labels []string
			var data []int64
			for _, rc := range rows {
				labels = append(labels, rc.Race)
				data = append(data, rc.Total)
			}
			mu.Lock()
			totalChars = tChars
			activeChars = aChars
			raceRows = rows
			chartLabels = labels
			chartData = data
			mu.Unlock()
		}()

		// Goroutine 2: Demographics (tribe + gender + race×gender, single query)
		wg.Add(1)
		go func() {
			defer wg.Done()
			demo, err := c.svc.DemographicBreakdown(ctx, filter)
			if err != nil {
				logging.Error("ui.races.demographics", err.Error())
				return
			}
			var tLabels []string
			var tData []int64
			for _, t := range demo.Tribes {
				if t.Key == "" {
					continue
				}
				tLabels = append(tLabels, t.Key)
				tData = append(tData, t.Total)
			}
			var gLabels []string
			var gData []int64
			for _, g := range demo.Genders {
				if g.Key == "" {
					continue
				}
				gLabels = append(gLabels, g.Key)
				gData = append(gData, g.Total)
			}
			var rgLabels []string
			var rgData []int64
			for _, rg := range demo.RaceGenders {
				if rg.Key == "" {
					continue
				}
				rgLabels = append(rgLabels, rg.Key)
				rgData = append(rgData, rg.Total)
			}
			mu.Lock()
			tribeLabels, tribeData = tLabels, tData
			genderLabels, genderData = gLabels, gData
			raceGenderLabels, raceGenderData = rgLabels, rgData
			mu.Unlock()
		}()

		wg.Wait()
	}

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

	title := "Race & Clan Demographics"
	if selectedWorld != "" {
		title = fmt.Sprintf("Race & Clan Demographics - %s", selectedWorld)
	} else if selectedDC != "" {
		title = fmt.Sprintf("Race & Clan Demographics - %s DC", selectedDC)
	} else if selectedRegion != "" {
		title = fmt.Sprintf("Race & Clan Demographics - %s", selectedRegion)
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
			TribeLabels:      tribeLabels,
			TribeData:        tribeData,
			GenderLabels:     genderLabels,
			GenderData:       genderData,
			RaceGenderLabels: raceGenderLabels,
			RaceGenderData:   raceGenderData,
		},
	})
}

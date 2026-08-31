package ui

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
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
	TotalCharacters    int64
	ActiveCharacters   int64
	NewCharacters30d   int64
	NewCharactersTrend string
	SelectedRegion     string
	SelectedDC         string
	SelectedWorld      string
	Regions            []string
	Datacenters        []string
	Worlds             []string
	Races              []RaceRow
	ChartLabels        []string
	ChartData          []int64
	TribeLabels        []string
	TribeData          []int64
	GenderLabels       []string
	GenderData         []int64
	RaceGenderLabels   []string
	RaceGenderData     []int64
}

// Races handles GET /ui/races.
func (c *UIController) Races(w http.ResponseWriter, r *http.Request) {
	snapshot, state, ok := c.currentStats(w, r)
	if !ok {
		return
	}
	selectedRegion := strings.TrimSpace(r.URL.Query().Get("region"))
	selectedDC := strings.TrimSpace(r.URL.Query().Get("dc"))
	selectedWorld := strings.TrimSpace(r.URL.Query().Get("world"))

	scope := contract.StatsScope{}
	if selectedWorld != "" {
		scope.World = selectedWorld
	} else if selectedDC != "" {
		scope.Datacenter = selectedDC
	} else if selectedRegion != "" {
		scope.Region = selectedRegion
	}

	newWindow := census.NewCharactersWindow(snapshot, scope)

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

	races := census.SnapshotGroups(snapshot, "race", scope)
	for _, row := range races {
		totalChars += row.Total
		activeChars += row.Active
	}
	for _, row := range races {
		name := row.Key
		if name == "" {
			name = "Unknown"
		}
		share := 0.0
		if totalChars > 0 {
			share = float64(row.Total) / float64(totalChars) * 100
		}
		raceRows = append(raceRows, RaceRow{Race: name, Total: row.Total, Active: row.Active, ActiveRatio: formatPercent(row.Active, row.Total), ShareOfTotal: formatPercent(row.Total, totalChars), SharePercentVal: share})
		chartLabels = append(chartLabels, name)
		chartData = append(chartData, row.Total)
	}
	for _, row := range census.SnapshotGroups(snapshot, "tribe", scope) {
		if row.Key != "" {
			tribeLabels = append(tribeLabels, row.Key)
			tribeData = append(tribeData, row.Total)
		}
	}
	for _, row := range census.SnapshotGroups(snapshot, "gender", scope) {
		genderLabels = append(genderLabels, row.Key)
		genderData = append(genderData, row.Total)
	}
	for _, row := range census.SnapshotGroups(snapshot, "race_gender", scope) {
		if row.Key != "" {
			raceGenderLabels = append(raceGenderLabels, row.Key)
			raceGenderData = append(raceGenderData, row.Total)
		}
	}

	// Build cascading filter lists: Region narrows DCs, DC narrows Worlds.
	var dcList []string
	if selectedRegion != "" {
		dcList = DCsForRegion(selectedRegion)
	} else {
		dcList = census.Datacenters()
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
		worldList = census.Worlds()
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

	c.render(w, "templates/races.html", statsPageData(title, "races", "/ui/races", "Race and clan demographics for Final Fantasy XIV: population shares, active ratios, and tribe and gender breakdowns filterable by region, datacenter, and world.", state, RacesViewData{
		TotalCharacters:    totalChars,
		ActiveCharacters:   activeChars,
		NewCharacters30d:   newWindow.Current,
		NewCharactersTrend: formatTrend(newWindow.Current, newWindow.Previous),
		SelectedRegion:     selectedRegion,
		SelectedDC:         selectedDC,
		SelectedWorld:      selectedWorld,
		Regions:            regionList,
		Datacenters:        dcList,
		Worlds:             worldList,
		Races:              raceRows,
		ChartLabels:        chartLabels,
		ChartData:          chartData,
		TribeLabels:        tribeLabels,
		TribeData:          tribeData,
		GenderLabels:       genderLabels,
		GenderData:         genderData,
		RaceGenderLabels:   raceGenderLabels,
		RaceGenderData:     raceGenderData,
	}))
}

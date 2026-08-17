package ui

import (
	"net/http"
	"sort"

	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
)

// RacesViewData holds race demographics and chart data for /ui/races.
type RacesViewData struct {
	TotalCharacters  int64
	ActiveCharacters int64
	Races            []RaceRow
	ChartLabels      []string
	ChartData        []int64
}

// RaceRow represents demographic stats for one playable race.
type RaceRow struct {
	Race            string
	Total           int64
	Active          int64
	ActiveRatio     string
	ShareOfTotal    string
	SharePercentVal float64
}

// Races handles GET /ui/races.
func (c *UIController) Races(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var totalChars, activeChars int64
	var raceRows []RaceRow
	var chartLabels []string
	var chartData []int64

	if c.svc != nil {
		tot, act, err := c.svc.Summary(ctx)
		if err == nil {
			totalChars = tot
			activeChars = act
		}

		breakdown, err := c.svc.Breakdown(ctx, "race")
		if err != nil {
			logging.Error("ui.races.breakdown", err.Error())
		} else {
			for _, row := range breakdown {
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
		}
	}

	sort.Slice(raceRows, func(i, j int) bool {
		return raceRows[i].Total > raceRows[j].Total
	})

	c.render(w, "templates/races.html", PageData{
		Title:     "Race Distribution",
		ActiveNav: "races",
		Data: RacesViewData{
			TotalCharacters:  totalChars,
			ActiveCharacters: activeChars,
			Races:            raceRows,
			ChartLabels:      chartLabels,
			ChartData:        chartData,
		},
	})
}

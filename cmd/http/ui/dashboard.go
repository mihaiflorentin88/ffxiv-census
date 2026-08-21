package ui

import (
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
)

// DashboardViewData holds the aggregate metrics and time-series for the dashboard.
type DashboardViewData struct {
	TotalCharacters      int64
	ActiveCharacters     int64
	ActivePercent        string
	MaxLevelCharacters   int64
	MaxLevel             uint32
	ChartLabels          []string
	ChartData            []int64
	Regions              []RegionSummary
	RaceLabels           []string
	RaceData             []int64
	ExpansionCompletions []ExpansionCard
}

// ExpansionCard holds completion stats for one expansion.
type ExpansionCard struct {
	Icon    string
	Name    string
	Count   int64
	Percent string
}

// RegionSummary holds aggregated stats for a region in the dashboard.
type RegionSummary struct {
	Region      string
	Total       int64
	Active      int64
	ActiveRatio string
}

// WorldDrilldownData holds the filtered world rows for the HTMX partial.
type WorldDrilldownData struct {
	Region string
	Worlds []WorldRow
}

// WorldRow holds stats for an individual world.
type WorldRow struct {
	Region      string
	Datacenter  string
	World       string
	Total       int64
	Active      int64
	ActiveRatio string
}

// Dashboard handles GET /ui/dashboard.
func (c *UIController) Dashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var total, active, maxLevelCount int64
	var maxLevel uint32 = 100
	var chartLabels []string
	var chartData []int64
	var regions []RegionSummary
	var raceLabels []string
	var raceData []int64
	var expansionCards []ExpansionCard

	if c.svc != nil {
		var wg sync.WaitGroup
		var mu sync.Mutex

		// Fetch summary + max level
		wg.Add(1)
		go func() {
			defer wg.Done()
			t, a, m, err := c.svc.Summary(ctx)
			if err != nil {
				logging.Error("ui.dashboard.summary", err.Error())
				return
			}
			mu.Lock()
			total, active, maxLevelCount = t, a, m
			if lvl := c.svc.MaxLevel(); lvl > 0 {
				maxLevel = lvl
			}
			mu.Unlock()
		}()

		// Fetch 30-day time series
		wg.Add(1)
		go func() {
			defer wg.Done()
			now := time.Now().UTC()
			since := now.AddDate(0, 0, -29)
			daily, err := c.svc.NewCharacters(ctx, since, now.AddDate(0, 0, 1))
			if err != nil {
				logging.Error("ui.dashboard.new_characters", err.Error())
				return
			}
			dayMap := make(map[string]int64)
			for _, d := range daily {
				dayMap[d.Day] = d.Count
			}
			var labels []string
			var data []int64
			for i := 29; i >= 0; i-- {
				dayStr := now.AddDate(0, 0, -i).Format("2006-01-02")
				labels = append(labels, dayStr[5:])
				data = append(data, dayMap[dayStr])
			}
			mu.Lock()
			chartLabels, chartData = labels, data
			mu.Unlock()
		}()

		// Fetch region breakdown
		wg.Add(1)
		go func() {
			defer wg.Done()
			byRegion, err := c.svc.Breakdown(ctx, "region")
			if err != nil {
				logging.Error("ui.dashboard.breakdown", err.Error())
				return
			}
			var reg []RegionSummary
			for _, rRow := range byRegion {
				regName := rRow.Key
				if regName == "" {
					regName = "Unknown"
				}
				reg = append(reg, RegionSummary{
					Region:      regName,
					Total:       rRow.Total,
					Active:      rRow.Active,
					ActiveRatio: formatPercent(rRow.Active, rRow.Total),
				})
			}
			mu.Lock()
			regions = reg
			mu.Unlock()
		}()

		// Fetch race distribution
		wg.Add(1)
		go func() {
			defer wg.Done()
			byRace, err := c.svc.Breakdown(ctx, "race")
			if err != nil {
				logging.Error("ui.dashboard.race_breakdown", err.Error())
				return
			}
			sort.Slice(byRace, func(i, j int) bool {
				return byRace[i].Total > byRace[j].Total
			})
			var labels []string
			var data []int64
			for _, rc := range byRace {
				if rc.Key == "" {
					continue
				}
				labels = append(labels, rc.Key)
				data = append(data, rc.Total)
			}
			mu.Lock()
			raceLabels, raceData = labels, data
			mu.Unlock()
		}()

		// Fetch expansion completions
		wg.Add(1)
		go func() {
			defer wg.Done()
			completions, err := c.svc.ExpansionCompletions(ctx)
			if err != nil {
				logging.Error("ui.dashboard.expansion_completions", err.Error())
				return
			}
			expansions := c.svc.Expansions()
			expMap := make(map[string]census.ExpansionConfig)
			for _, e := range expansions {
				expMap[e.Name] = e
			}
			var cards []ExpansionCard
			for _, ec := range completions {
				cfg, ok := expMap[ec.Expansion]
				icon := "⚔️"
				if ok {
					icon = cfg.Icon
				}
				cards = append(cards, ExpansionCard{
					Icon:  icon,
					Name:  ec.Expansion,
					Count: ec.Count,
				})
			}
			mu.Lock()
			expansionCards = cards
			mu.Unlock()
		}()

		wg.Wait()
	}

	// Fix expansion percents now that total is known
	for i := range expansionCards {
		expansionCards[i].Percent = formatPercent(expansionCards[i].Count, total)
	}

	viewData := DashboardViewData{
		TotalCharacters:      total,
		ActiveCharacters:     active,
		ActivePercent:        formatPercent(active, total),
		MaxLevelCharacters:   maxLevelCount,
		MaxLevel:             maxLevel,
		ChartLabels:          chartLabels,
		ChartData:            chartData,
		Regions:              regions,
		RaceLabels:           raceLabels,
		RaceData:             raceData,
		ExpansionCompletions: expansionCards,
	}

	c.render(w, "templates/dashboard.html", PageData{
		Title:     "Dashboard",
		ActiveNav: "dashboard",
		Data:      viewData,
	})
}

// WorldDrilldown handles GET /ui/partials/world-breakdown?region=NA.
func (c *UIController) WorldDrilldown(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	region := strings.TrimSpace(r.URL.Query().Get("region"))
	if region == "" {
		region = "NA"
	}

	var worlds []WorldRow
	if c.svc != nil {
		worldCounts, err := c.svc.Breakdown(ctx, "world")
		if err == nil {
			for _, wc := range worldCounts {
				wName := wc.Key
				wDC := WorldToDC(wName)
				wReg := census.RegionForDatacenter(wDC)
				if wReg == "" {
					wReg = "Unknown"
				}

				if strings.EqualFold(wReg, region) || (region == "Unknown" && wReg == "Unknown") {
					worlds = append(worlds, WorldRow{
						Region:      wReg,
						Datacenter:  wDC,
						World:       wName,
						Total:       wc.Total,
						Active:      wc.Active,
						ActiveRatio: formatPercent(wc.Active, wc.Total),
					})
				}
			}
		}
	}

	sort.Slice(worlds, func(i, j int) bool {
		if worlds[i].Datacenter != worlds[j].Datacenter {
			return worlds[i].Datacenter < worlds[j].Datacenter
		}
		return worlds[i].Total > worlds[j].Total
	})

	c.renderPartial(w, "templates/partials/world_drilldown.html", WorldDrilldownData{
		Region: region,
		Worlds: worlds,
	})
}

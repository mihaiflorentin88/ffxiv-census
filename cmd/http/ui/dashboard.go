package ui

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// DashboardViewData holds the aggregate metrics and time-series for the dashboard.
type DashboardViewData struct {
	TotalCharacters    int64
	ActiveCharacters   int64
	ActivePercent      string
	MaxLevelCharacters int64
	MaxLevel           uint32
	QueuePending       int
	QueueClaimed       int
	QueueDone          int
	QueueFailed        int
	ChartLabels        []string
	ChartData          []int64
	Regions            []RegionSummary
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
	var err error

	if c.svc != nil {
		total, active, maxLevelCount, err = c.svc.Summary(ctx)
		if err != nil {
			logging.Error("ui.dashboard.summary", err.Error())
		}
		if lvl := c.svc.MaxLevel(); lvl > 0 {
			maxLevel = lvl
		}
	}

	// 30-day time series
	var chartLabels []string
	var chartData []int64
	if c.svc != nil {
		now := time.Now().UTC()
		since := now.AddDate(0, 0, -29)
		daily, err := c.svc.NewCharacters(ctx, since, now.AddDate(0, 0, 1))
		if err != nil {
			logging.Error("ui.dashboard.new_characters", err.Error())
		}

		dayMap := make(map[string]int64)
		for _, d := range daily {
			dayMap[d.Day] = d.Count
		}

		for i := 29; i >= 0; i-- {
			dayStr := now.AddDate(0, 0, -i).Format("2006-01-02")
			chartLabels = append(chartLabels, dayStr[5:]) // e.g. "08-16"
			chartData = append(chartData, dayMap[dayStr])
		}
	}

	// Queue depth
	var qPending, qClaimed, qDone, qFailed int
	if c.q != nil {
		depth, err := c.q.Depth(ctx)
		if err == nil {
			qPending = depth[contract.QueueJobPending]
			qClaimed = depth[contract.QueueJobClaimed]
			qDone = depth[contract.QueueJobDone]
			qFailed = depth[contract.QueueJobFailed]
		}
	}

	// Region Breakdown
	var regions []RegionSummary
	if c.svc != nil {
		byRegion, err := c.svc.Breakdown(ctx, "region")
		if err == nil {
			for _, rRow := range byRegion {
				regName := rRow.Key
				if regName == "" {
					regName = "Unknown"
				}
				regions = append(regions, RegionSummary{
					Region:      regName,
					Total:       rRow.Total,
					Active:      rRow.Active,
					ActiveRatio: formatPercent(rRow.Active, rRow.Total),
				})
			}
		}
	}

	viewData := DashboardViewData{
		TotalCharacters:    total,
		ActiveCharacters:   active,
		ActivePercent:      formatPercent(active, total),
		MaxLevelCharacters: maxLevelCount,
		MaxLevel:           maxLevel,
		QueuePending:       qPending,
		QueueClaimed:       qClaimed,
		QueueDone:          qDone,
		QueueFailed:        qFailed,
		ChartLabels:        chartLabels,
		ChartData:          chartData,
		Regions:            regions,
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

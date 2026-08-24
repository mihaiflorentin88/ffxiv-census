package ui

import (
	"fmt"
	"net/http"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/port/contract"
)

// ExpansionsViewData holds MSQ completion funnel metrics for /ui/expansions.
type ExpansionsViewData struct {
	TotalCharacters int64
	Expansions      []ExpansionProgression
}

// ExpansionProgression holds the stats and drop-off rate for an expansion milestone.
type ExpansionProgression struct {
	Name           string
	Version        string
	FinalQuest     string
	Icon           string
	Completions    int64
	PercentOfTotal string
	PercentValue   float64
	RetentionRate  string
	DropOffRate    string
}

// Expansions handles GET /ui/expansions.
func (c *UIController) Expansions(w http.ResponseWriter, r *http.Request) {
	snapshot, state, ok := c.currentStats(w, r)
	if !ok {
		return
	}
	totalChars := snapshot.Summary.Total
	completions := census.SnapshotExpansions(snapshot, contract.StatsScope{})
	countMap := make(map[string]int64, len(completions))
	for _, completion := range completions {
		countMap[completion.Expansion] = completion.Count
	}

	var expansionList []census.ExpansionConfig
	if c.svc != nil {
		expansionList = c.svc.Expansions()
	}
	if len(expansionList) == 0 {
		expansionList = census.DefaultExpansions
	}

	var list []ExpansionProgression
	var prevCount int64 = -1

	for _, info := range expansionList {
		count := countMap[info.Name]
		var pctVal float64
		if totalChars > 0 {
			pctVal = (float64(count) / float64(totalChars)) * 100
		}

		retention := "-"
		dropOff := "-"
		if prevCount > 0 {
			retPct := (float64(count) / float64(prevCount)) * 100
			dropPct := 100.0 - retPct
			if dropPct < 0 {
				dropPct = 0
			}
			retention = fmt.Sprintf("%.1f%%", retPct)
			dropOff = fmt.Sprintf("%.1f%%", dropPct)
		} else if prevCount == 0 {
			retention = "0.0%"
			dropOff = "100.0%"
		}

		list = append(list, ExpansionProgression{
			Name:           info.Name,
			Version:        info.Version,
			FinalQuest:     info.FinalQuest,
			Icon:           info.Icon,
			Completions:    count,
			PercentOfTotal: formatPercent(count, totalChars),
			PercentValue:   pctVal,
			RetentionRate:  retention,
			DropOffRate:    dropOff,
		})

		prevCount = count
	}

	c.render(w, "templates/expansions.html", statsPageData("Expansion Progression", "expansions", state, ExpansionsViewData{
		TotalCharacters: totalChars,
		Expansions:      list,
	}))
}

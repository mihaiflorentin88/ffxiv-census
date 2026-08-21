package ui

import (
	"fmt"
	"net/http"
	"sync"

	"github.com/mihaiflorentin88/ffxiv-census/domain/census"
	"github.com/mihaiflorentin88/ffxiv-census/infrastructure/logging"
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
	ctx := r.Context()
	var totalChars int64
	var countMap map[string]int64

	if c.svc != nil {
		var wg sync.WaitGroup
		var completions []contract.ExpansionCount
		var summaryErr, compErr error

		wg.Add(2)
		go func() {
			defer wg.Done()
			tot, _, _, err := c.svc.Summary(ctx)
			if err != nil {
				summaryErr = err
				return
			}
			totalChars = tot
		}()
		go func() {
			defer wg.Done()
			completions, compErr = c.svc.ExpansionCompletions(ctx)
		}()
		wg.Wait()

		if summaryErr != nil {
			// totalChars stays zero; partial page renders.
			logging.Error("ui.expansions.summary", summaryErr.Error())
		}
		if compErr != nil {
			logging.Error("ui.expansions.completions", compErr.Error())
		} else {
			countMap = make(map[string]int64, len(completions))
			for _, ec := range completions {
				countMap[ec.Expansion] = ec.Count
			}
		}
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

	c.render(w, "templates/expansions.html", PageData{
		Title:     "Expansion Progression",
		ActiveNav: "expansions",
		Data: ExpansionsViewData{
			TotalCharacters: totalChars,
			Expansions:      list,
		},
	})
}

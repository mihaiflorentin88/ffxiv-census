# Plan: Dashboard Expansion Sort, Query Optimization & New Demographic Pie Charts

## Context

Three changes needed:

1. **Dashboard expansion sort:** The dashboard renders expansion MSQ completions in SQL alphabetical order instead of config release order. The expansions page already does this correctly by iterating `svc.Expansions()` and looking up counts from a map.

2. **Query optimization:** The dashboard fires 5 goroutines making ~7 DB calls (Summary alone makes 3: Count, CountActive, Count with MinLevel). Consolidate into fewer, larger queries using `COUNT(*) FILTER` and `UNION ALL`.

3. **New pie charts on `/ui/races`:** Add tribe distribution, gender distribution, and race×gender combination distribution doughnut charts to the Race & Clan Demographics page — following the existing race distribution chart pattern on that page.

## Approach

### Step 0: Save Plan

Copy this plan to `docs/superpowers/plans/2026-08-23-ui-dashboard-expansion-sort-query-optimization-new-charts.md`.

---

### Step 1: Fix Dashboard Expansion Sort Order

**File:** `cmd/http/ui/dashboard.go` — expansion completions goroutine

The goroutine currently iterates `completions` (DB order: alphabetical). Change to iterate `svc.Expansions()` (config order) and look up counts from a map.

**Replace the expansion goroutine body (the `go func() { ... }()` block that calls `c.svc.ExpansionCompletions`) with:**

```go
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
    countMap := make(map[string]int64, len(completions))
    for _, ec := range completions {
        countMap[ec.Expansion] = ec.Count
    }
    var cards []ExpansionCard
    for _, exp := range expansions {
        cards = append(cards, ExpansionCard{
            Icon:  exp.Icon,
            Name:  exp.Name,
            Count: countMap[exp.Name],
        })
    }
    mu.Lock()
    expansionCards = cards
    mu.Unlock()
}()
```

---

### Step 2: Add SummaryCounts to Character Repository

**File:** `port/contract/character_repository.go` — add to interface after `CountActive`:

```go
// SummaryCounts returns total, active (latest_achievement_at >= since), and
// max-level (character_jobs.level >= maxLevel) counts in a single query.
SummaryCounts(ctx context.Context, since time.Time, maxLevel uint32) (total, active, maxLevelCount int64, err error)
```

**File:** `infrastructure/postgres/repository/character.go` — add after `CountActive` method:

```go
func (r *CharacterRepository) SummaryCounts(ctx context.Context, since time.Time, maxLevel uint32) (total, active, maxLevelCount int64, err error) {
	row, err := r.driver.FetchOne(ctx,
		`SELECT
			COUNT(*) AS total,
			COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active,
			COUNT(*) FILTER (WHERE id IN (SELECT character_id FROM character_jobs WHERE level >= $2)) AS max_level
		FROM characters
		WHERE deleted_at IS NULL`, since, maxLevel)
	if err != nil {
		return 0, 0, 0, err
	}
	if err := row.Scan(&total, &active, &maxLevelCount); err != nil {
		return 0, 0, 0, err
	}
	return total, active, maxLevelCount, nil
}
```

**File:** `mock/repository/character.go` — add field `SummaryCountsErr error` to struct, then add method:

```go
func (f *CharacterRepository) SummaryCounts(ctx context.Context, since time.Time, maxLevel uint32) (total, active, maxLevelCount int64, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.SummaryCountsErr != nil {
		return 0, 0, 0, f.SummaryCountsErr
	}
	for _, rec := range f.characters {
		if rec.DeletedAt != nil {
			continue
		}
		total++
		if rec.LatestAchievementAt != nil && !rec.LatestAchievementAt.Before(since) {
			active++
		}
		for _, j := range f.jobs[rec.ID] {
			if uint32(j.Level) >= maxLevel {
				maxLevelCount++
				break
			}
		}
	}
	return total, active, maxLevelCount, nil
}
```

---

### Step 3: Add MultiBreakdown to Character Repository

**File:** `port/contract/character_repository.go` — add after `Breakdown`:

```go
// MultiBreakdown returns group-by counts for multiple columns in a single
// query using UNION ALL. Returns a map[column][]GroupCount. Supported
// columns: race, world, datacenter, region.
MultiBreakdown(ctx context.Context, columns []string, since time.Time, filter CharacterFilter) (map[string][]GroupCount, error)
```

**File:** `infrastructure/postgres/repository/character.go` — add after `Breakdown`:

```go
func (r *CharacterRepository) MultiBreakdown(ctx context.Context, columns []string, since time.Time, f contract.CharacterFilter) (map[string][]contract.GroupCount, error) {
	if len(columns) == 0 {
		return map[string][]contract.GroupCount{}, nil
	}
	for _, col := range columns {
		if !breakdownColumns[col] {
			return nil, fmt.Errorf("invalid breakdown column %q", col)
		}
	}

	filterWhere, filterArgs := characterFilterWhereWithStart(f, 2)
	args := []any{since}
	args = append(args, filterArgs...)

	var unions []string
	for _, col := range columns {
		unions = append(unions, fmt.Sprintf(
			`SELECT '%s' AS dimension, %s AS key, COUNT(*) AS total,
			        COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active
			   FROM characters WHERE deleted_at IS NULL %s
			  GROUP BY %s`, col, col, filterWhere, col))
	}
	query := strings.Join(unions, " UNION ALL ")

	rows, err := r.driver.FetchMany(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make(map[string][]contract.GroupCount)
	for rows.Next() {
		var dimension string
		var g contract.GroupCount
		var key sql.NullString
		if err := rows.Scan(&dimension, &key, &g.Total, &g.Active); err != nil {
			return nil, err
		}
		if key.Valid {
			g.Key = key.String
		}
		out[dimension] = append(out[dimension], g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
```

**File:** `mock/repository/character.go` — add field `MultiBreakdownErr error`, then add method:

```go
func (f *CharacterRepository) MultiBreakdown(ctx context.Context, columns []string, since time.Time, filter contract.CharacterFilter) (map[string][]contract.GroupCount, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.MultiBreakdownErr != nil {
		return nil, f.MultiBreakdownErr
	}
	out := make(map[string][]contract.GroupCount, len(columns))
	for _, col := range columns {
		counts := map[string]*contract.GroupCount{}
		for _, rec := range f.characters {
			if rec.DeletedAt != nil || !matchesFilter(rec, f.jobs[rec.ID], filter) {
				continue
			}
			var key string
			switch col {
			case "race":
				key = rec.Race
			case "world":
				key = rec.World
			case "datacenter":
				key = rec.Datacenter
			case "region":
				key = rec.Region
			}
			g := counts[key]
			if g == nil {
				g = &contract.GroupCount{Key: key}
				counts[key] = g
			}
			g.Total++
			if rec.LatestAchievementAt != nil && !rec.LatestAchievementAt.Before(since) {
				g.Active++
			}
		}
		var list []contract.GroupCount
		for _, g := range counts {
			list = append(list, *g)
		}
		sort.Slice(list, func(i, j int) bool {
			if list[i].Total != list[j].Total {
				return list[i].Total > list[j].Total
			}
			return list[i].Key < list[j].Key
		})
		out[col] = list
	}
	return out, nil
}
```

---

### Step 4: Add DemographicBreakdown to Character Repository

**File:** `port/contract/character_repository.go` — add after `MultiBreakdown`:

```go
// DemographicCounts holds tribe, gender, and race×gender breakdowns from a
// single query.
type DemographicCounts struct {
	Tribes      []GroupCount
	Genders     []GroupCount
	RaceGenders []GroupCount // Key format: "Race|Gender"
}

// DemographicBreakdown returns tribe, gender, and race×gender character
// counts in a single query. RaceGenders keys use "Race|Gender" format.
DemographicBreakdown(ctx context.Context, since time.Time, filter CharacterFilter) (*DemographicCounts, error)
```

**File:** `infrastructure/postgres/repository/character.go` — add after `MultiBreakdown`. Uses separate `characterFilterWhereWithStart` calls with unique parameter indices per UNION branch:

```go
func (r *CharacterRepository) DemographicBreakdown(ctx context.Context, since time.Time, f contract.CharacterFilter) (*contract.DemographicCounts, error) {
	// Build filter clauses with unique parameter indices for each UNION branch.
	// Branch 1 (tribe): $1 = since, $2..N = filter params
	filterWhere1, filterArgs1 := characterFilterWhereWithStart(f, 2)
	// Branch 2 (gender): params continue after branch 1
	offset2 := 2 + len(filterArgs1)
	filterWhere2, filterArgs2 := characterFilterWhereWithStart(f, offset2)
	// Branch 3 (race_gender): params continue after branch 2
	offset3 := offset2 + len(filterArgs2)
	filterWhere3, filterArgs3 := characterFilterWhereWithStart(f, offset3)

	args := []any{since}
	args = append(args, filterArgs1...)
	args = append(args, filterArgs2...)
	args = append(args, filterArgs3...)

	query := fmt.Sprintf(`
		SELECT 'tribe' AS dimension, tribe AS key, COUNT(*) AS total,
		       COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active
		  FROM characters WHERE deleted_at IS NULL AND tribe != '' %s
		  GROUP BY tribe
		UNION ALL
		SELECT 'gender' AS dimension,
		       CASE gender WHEN 1 THEN 'Male' WHEN 2 THEN 'Female' ELSE 'Unknown' END AS key,
		       COUNT(*) AS total,
		       COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active
		  FROM characters WHERE deleted_at IS NULL %s
		  GROUP BY gender
		UNION ALL
		SELECT 'race_gender' AS dimension,
		       race || '|' || CASE gender WHEN 1 THEN 'Male' WHEN 2 THEN 'Female' ELSE 'Unknown' END AS key,
		       COUNT(*) AS total,
		       COUNT(*) FILTER (WHERE latest_achievement_at >= $1) AS active
		  FROM characters WHERE deleted_at IS NULL AND race != '' %s
		  GROUP BY race, gender`, filterWhere1, filterWhere2, filterWhere3)

	rows, err := r.driver.FetchMany(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	result := &contract.DemographicCounts{}
	for rows.Next() {
		var dimension string
		var g contract.GroupCount
		var key sql.NullString
		if err := rows.Scan(&dimension, &key, &g.Total, &g.Active); err != nil {
			return nil, err
		}
		if key.Valid {
			g.Key = key.String
		}
		switch dimension {
		case "tribe":
			result.Tribes = append(result.Tribes, g)
		case "gender":
			result.Genders = append(result.Genders, g)
		case "race_gender":
			result.RaceGenders = append(result.RaceGenders, g)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return result, nil
}
```

**File:** `mock/repository/character.go` — add field `DemographicBreakdownErr error`, then add method:

```go
func (f *CharacterRepository) DemographicBreakdown(ctx context.Context, since time.Time, filter contract.CharacterFilter) (*contract.DemographicCounts, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.DemographicBreakdownErr != nil {
		return nil, f.DemographicBreakdownErr
	}

	result := &contract.DemographicCounts{}
	tribeCounts := map[string]*contract.GroupCount{}
	genderCounts := map[string]*contract.GroupCount{}
	rgCounts := map[string]*contract.GroupCount{}

	for _, rec := range f.characters {
		if rec.DeletedAt != nil || !matchesFilter(rec, f.jobs[rec.ID], filter) {
			continue
		}
		active := rec.LatestAchievementAt != nil && !rec.LatestAchievementAt.Before(since)

		if rec.Tribe != "" {
			g := tribeCounts[rec.Tribe]
			if g == nil {
				g = &contract.GroupCount{Key: rec.Tribe}
				tribeCounts[rec.Tribe] = g
			}
			g.Total++
			if active {
				g.Active++
			}
		}

		var genderStr string
		switch rec.Gender {
		case 1:
			genderStr = "Male"
		case 2:
			genderStr = "Female"
		default:
			genderStr = "Unknown"
		}
		gg := genderCounts[genderStr]
		if gg == nil {
			gg = &contract.GroupCount{Key: genderStr}
			genderCounts[genderStr] = gg
		}
		gg.Total++
		if active {
			gg.Active++
		}

		if rec.Race != "" {
			rgKey := rec.Race + "|" + genderStr
			rg := rgCounts[rgKey]
			if rg == nil {
				rg = &contract.GroupCount{Key: rgKey}
				rgCounts[rgKey] = rg
			}
			rg.Total++
			if active {
				rg.Active++
			}
		}
	}

	for _, g := range tribeCounts {
		result.Tribes = append(result.Tribes, *g)
	}
	sort.Slice(result.Tribes, func(i, j int) bool { return result.Tribes[i].Total > result.Tribes[j].Total })

	for _, g := range genderCounts {
		result.Genders = append(result.Genders, *g)
	}
	sort.Slice(result.Genders, func(i, j int) bool { return result.Genders[i].Total > result.Genders[j].Total })

	for _, g := range rgCounts {
		result.RaceGenders = append(result.RaceGenders, *g)
	}
	sort.Slice(result.RaceGenders, func(i, j int) bool { return result.RaceGenders[i].Total > result.RaceGenders[j].Total })

	return result, nil
}
```

---

### Step 5: Add Service Layer Methods

**File:** `domain/census/service.go` — add after `ExpansionCompletions` method:

```go
// SummaryCounts returns total, active, and max-level character counts in a
// single repository query. More efficient than Summary() which uses 3 queries.
func (s *Service) SummaryCounts(ctx context.Context) (total, active, maxLevelCount int64, err error) {
	return s.characters.SummaryCounts(ctx, s.activitySince(), s.MaxLevel())
}

// MultiBreakdown returns group-by counts for multiple columns in a single
// query. More efficient than calling Breakdown() multiple times.
func (s *Service) MultiBreakdown(ctx context.Context, columns []string, filter ...contract.CharacterFilter) (map[string][]contract.GroupCount, error) {
	f := contract.CharacterFilter{}
	if len(filter) > 0 {
		f = filter[0]
	}
	return s.characters.MultiBreakdown(ctx, columns, s.activitySince(), f)
}

// DemographicBreakdown returns tribe, gender, and race×gender counts in a
// single query. Used by the races page for demographic pie charts.
func (s *Service) DemographicBreakdown(ctx context.Context, filter ...contract.CharacterFilter) (*contract.DemographicCounts, error) {
	f := contract.CharacterFilter{}
	if len(filter) > 0 {
		f = filter[0]
	}
	return s.characters.DemographicBreakdown(ctx, s.activitySince(), f)
}
```

---

### Step 6: Refactor Dashboard Handler to Use Optimized Queries

**File:** `cmd/http/ui/dashboard.go`

**6a. Replace the entire `Dashboard` handler body with:**

```go
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

		// Goroutine 1: Summary counts (single query replacing 3)
		wg.Add(1)
		go func() {
			defer wg.Done()
			t, a, m, err := c.svc.SummaryCounts(ctx)
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

		// Goroutine 2: Region + Race breakdown (single query replacing 2)
		wg.Add(1)
		go func() {
			defer wg.Done()
			breakdowns, err := c.svc.MultiBreakdown(ctx, []string{"region", "race"})
			if err != nil {
				logging.Error("ui.dashboard.multi_breakdown", err.Error())
				return
			}
			var reg []RegionSummary
			for _, rRow := range breakdowns["region"] {
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
			byRace := breakdowns["race"]
			sort.Slice(byRace, func(i, j int) bool {
				return byRace[i].Total > byRace[j].Total
			})
			var rLabels []string
			var rData []int64
			for _, rc := range byRace {
				if rc.Key == "" {
					continue
				}
				rLabels = append(rLabels, rc.Key)
				rData = append(rData, rc.Total)
			}
			mu.Lock()
			regions = reg
			raceLabels, raceData = rLabels, rData
			mu.Unlock()
		}()

		// Goroutine 3: Expansion completions (config-order iteration)
		wg.Add(1)
		go func() {
			defer wg.Done()
			completions, err := c.svc.ExpansionCompletions(ctx)
			if err != nil {
				logging.Error("ui.dashboard.expansion_completions", err.Error())
				return
			}
			expansions := c.svc.Expansions()
			countMap := make(map[string]int64, len(completions))
			for _, ec := range completions {
				countMap[ec.Expansion] = ec.Count
			}
			var cards []ExpansionCard
			for _, exp := range expansions {
				cards = append(cards, ExpansionCard{
					Icon:  exp.Icon,
					Name:  exp.Name,
					Count: countMap[exp.Name],
				})
			}
			mu.Lock()
			expansionCards = cards
			mu.Unlock()
		}()

		// Goroutine 4: 30-day time series (single query)
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
```

**6b. Remove unused imports:** The `"time"` import is still needed (time series goroutine). No import changes required.

---

### Step 7: Add New Pie Charts to Races Page

**File:** `cmd/http/ui/races.go`

**7a. Add new fields to `RacesViewData` struct:**

```go
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
```

**7b. Add `"sync"` to imports.**

**7c. Replace the `Races` handler body with concurrent data fetching:**

```go
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
```

---

### Step 8: Add New Pie Chart Panels to Races Template

**File:** `cmd/http/ui/templates/races.html`

**8a. Add HTML panels** after the "Detailed Race Table Panel" `</div>` (end of the race table panel), before the `<script>` tag:

```html
<!-- Tribe, Gender & Race×Gender Distribution -->
<div style="display: grid; grid-template-columns: repeat(auto-fit, minmax(360px, 1fr)); gap: 1.5rem; margin-bottom: 2rem; margin-top: 2rem;">
    <div class="panel" style="margin-bottom: 0;">
        <div class="panel-header">
            <h2 class="panel-title"><span>🧝</span><span>Tribe Distribution</span></h2>
        </div>
        <div class="panel-body">
            {{if .Data.TribeLabels}}
            <div class="chart-container" style="height: 340px;"><canvas id="tribePieChart"></canvas></div>
            {{else}}
            <p class="text-dim">No tribe data available yet.</p>
            {{end}}
        </div>
    </div>
    <div class="panel" style="margin-bottom: 0;">
        <div class="panel-header">
            <h2 class="panel-title"><span>⚧</span><span>Gender Distribution</span></h2>
        </div>
        <div class="panel-body">
            {{if .Data.GenderLabels}}
            <div class="chart-container" style="height: 340px;"><canvas id="genderPieChart"></canvas></div>
            {{else}}
            <p class="text-dim">No gender data available yet.</p>
            {{end}}
        </div>
    </div>
</div>
<div style="margin-bottom: 2rem;">
    <div class="panel" style="margin-bottom: 0;">
        <div class="panel-header">
            <h2 class="panel-title"><span>👥</span><span>Race × Gender Distribution</span></h2>
        </div>
        <div class="panel-body">
            {{if .Data.RaceGenderLabels}}
            <div class="chart-container" style="height: 400px;"><canvas id="raceGenderPieChart"></canvas></div>
            {{else}}
            <p class="text-dim">No race/gender data available yet.</p>
            {{end}}
        </div>
    </div>
</div>
```

**8b. Add Chart.js initializations** inside the existing `document.addEventListener("DOMContentLoaded", function() { ... })` block, after the existing `raceChart` initialization:

```javascript
    var tribeCtx = document.getElementById("tribePieChart");
    if (tribeCtx) {
        var tribeLabels = {{jsonSafe .Data.TribeLabels}};
        var tribeData = {{jsonSafe .Data.TribeData}};
        var tribeColors = ['#d4af37','#38bdf8','#f472b6','#34d399','#a78bfa','#fb923c','#e879f9','#22d3ee','#fbbf24','#60a5fa','#f87171','#4ade80','#818cf8','#f59e0b','#10b981','#ec4899'];
        new Chart(tribeCtx, {
            type: 'doughnut',
            data: { labels: tribeLabels, datasets: [{ data: tribeData, backgroundColor: tribeColors.slice(0, tribeLabels.length), borderColor: '#141923', borderWidth: 2 }] },
            options: {
                responsive: true, maintainAspectRatio: false, cutout: '65%',
                plugins: {
                    legend: { position: 'bottom', labels: { color: '#94a3b8', padding: 12, font: { size: 11 } } },
                    tooltip: { backgroundColor: '#141923', borderColor: '#263248', borderWidth: 1, titleColor: '#d4af37', bodyColor: '#f1f5f9',
                        callbacks: { label: function(ctx) { var total = ctx.dataset.data.reduce(function(a,b){return a+b;},0); var pct = ((ctx.parsed/total)*100).toFixed(1); return ctx.label+': '+ctx.parsed.toLocaleString()+' ('+pct+'%)'; } }
                    }
                }
            }
        });
    }
    var genderCtx = document.getElementById("genderPieChart");
    if (genderCtx) {
        var genderLabels = {{jsonSafe .Data.GenderLabels}};
        var genderData = {{jsonSafe .Data.GenderData}};
        var genderColors = ['#38bdf8','#f472b6','#a78bfa'];
        new Chart(genderCtx, {
            type: 'doughnut',
            data: { labels: genderLabels, datasets: [{ data: genderData, backgroundColor: genderColors.slice(0, genderLabels.length), borderColor: '#141923', borderWidth: 2 }] },
            options: {
                responsive: true, maintainAspectRatio: false, cutout: '65%',
                plugins: {
                    legend: { position: 'bottom', labels: { color: '#94a3b8', padding: 12, font: { size: 11 } } },
                    tooltip: { backgroundColor: '#141923', borderColor: '#263248', borderWidth: 1, titleColor: '#d4af37', bodyColor: '#f1f5f9',
                        callbacks: { label: function(ctx) { var total = ctx.dataset.data.reduce(function(a,b){return a+b;},0); var pct = ((ctx.parsed/total)*100).toFixed(1); return ctx.label+': '+ctx.parsed.toLocaleString()+' ('+pct+'%)'; } }
                    }
                }
            }
        });
    }
    var rgCtx = document.getElementById("raceGenderPieChart");
    if (rgCtx) {
        var rgLabels = {{jsonSafe .Data.RaceGenderLabels}};
        var rgData = {{jsonSafe .Data.RaceGenderData}};
        var rgColors = ['#d4af37','#38bdf8','#f472b6','#34d399','#a78bfa','#fb923c','#e879f9','#22d3ee','#fbbf24','#60a5fa','#f87171','#4ade80','#818cf8','#f59e0b','#10b981','#ec4899'];
        new Chart(rgCtx, {
            type: 'doughnut',
            data: { labels: rgLabels, datasets: [{ data: rgData, backgroundColor: rgColors.slice(0, rgLabels.length), borderColor: '#141923', borderWidth: 2 }] },
            options: {
                responsive: true, maintainAspectRatio: false, cutout: '65%',
                plugins: {
                    legend: { position: 'bottom', labels: { color: '#94a3b8', padding: 12, font: { size: 11 } } },
                    tooltip: { backgroundColor: '#141923', borderColor: '#263248', borderWidth: 1, titleColor: '#d4af37', bodyColor: '#f1f5f9',
                        callbacks: { label: function(ctx) { var total = ctx.dataset.data.reduce(function(a,b){return a+b;},0); var pct = ((ctx.parsed/total)*100).toFixed(1); return ctx.label+': '+ctx.parsed.toLocaleString()+' ('+pct+'%)'; } }
                    }
                }
            }
        });
    }
```

---

### Step 9: Write Tests

**File:** `cmd/http/ui/dashboard_test.go` — add after existing tests:

```go
func TestDashboardHandler_ExpansionSortOrder(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID: 5001, Name: "TestChar", World: "Balmung", Datacenter: "Crystal", Region: "NA",
		Race: "Hyur", FirstSeenAt: recent, LatestAchievementAt: &recent,
	}, nil)

	_ = rig.ach.SyncMilestones(context.Background(), census.DefaultMilestones())
	_ = rig.ach.UpsertCharacterMilestones(context.Background(), 5001, []contract.CharacterMilestone{
		{CharacterID: 5001, AchievementID: 1129, AchievedAt: recent},
		{CharacterID: 5001, AchievementID: 3496, AchievedAt: recent},
	})

	req := httptest.NewRequest(http.MethodGet, "/ui/dashboard", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Dashboard(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	arIdx := strings.Index(body, "A Realm Reborn")
	dtIdx := strings.Index(body, "Dawntrail")
	if arIdx < 0 {
		t.Fatal("expected 'A Realm Reborn' in body")
	}
	if dtIdx < 0 {
		t.Fatal("expected 'Dawntrail' in body")
	}
	if arIdx > dtIdx {
		t.Errorf("expansion sort order wrong: A Realm Reborn (idx %d) should appear before Dawntrail (idx %d)", arIdx, dtIdx)
	}
}
```

**File:** `cmd/http/ui/races_test.go` — add after existing tests:

```go
func TestRacesHandler_DemographicPieCharts(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID: 7001, Name: "SunSeeker", World: "Balmung", Datacenter: "Crystal", Region: "NA",
		Race: "Miqo'te", Tribe: "Seekers of the Sun", Gender: 2,
		FirstSeenAt: recent, LatestAchievementAt: &recent,
	}, nil)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID: 7002, Name: "Highlander", World: "Louisoix", Datacenter: "Chaos", Region: "EU",
		Race: "Hyur", Tribe: "Highlander", Gender: 1,
		FirstSeenAt: recent, LatestAchievementAt: &recent,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ui/races", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Races(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()

	if !strings.Contains(body, "tribePieChart") {
		t.Error("expected tribePieChart canvas in body")
	}
	if !strings.Contains(body, "Seekers of the Sun") {
		t.Error("expected tribe 'Seekers of the Sun' in body")
	}

	if !strings.Contains(body, "genderPieChart") {
		t.Error("expected genderPieChart canvas in body")
	}

	if !strings.Contains(body, "raceGenderPieChart") {
		t.Error("expected raceGenderPieChart canvas in body")
	}
}

func TestRacesHandler_DemographicChartsFiltered(t *testing.T) {
	rig := newTestRig(t)
	now := time.Now().UTC()
	recent := now.Add(-1 * time.Hour)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID: 8001, Name: "NAChar", World: "Balmung", Datacenter: "Crystal", Region: "NA",
		Race: "Miqo'te", Tribe: "Seekers of the Sun", Gender: 2,
		FirstSeenAt: recent, LatestAchievementAt: &recent,
	}, nil)

	_ = rig.chars.Upsert(context.Background(), contract.CharacterRecord{
		ID: 8002, Name: "EUChar", World: "Louisoix", Datacenter: "Chaos", Region: "EU",
		Race: "Hyur", Tribe: "Highlander", Gender: 1,
		FirstSeenAt: recent, LatestAchievementAt: &recent,
	}, nil)

	req := httptest.NewRequest(http.MethodGet, "/ui/races?region=NA", nil)
	rec := httptest.NewRecorder()
	rig.ctrl.Races(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "Seekers of the Sun") {
		t.Error("expected filtered body to contain NA tribe")
	}
	if strings.Contains(body, "Highlander") {
		t.Error("expected filtered body to exclude EU tribe")
	}
}
```

---

### Step 10: Update Documentation

**File:** `docs/ui.md` — update the `/ui/races` route description in the Route Inventory table:

```
| `/ui/races` | `GET` | Playable race demographics with cascading region/DC/world filters, global percentage shares, active ratios, Chart.js doughnut chart, and three additional demographic doughnut charts: tribe distribution, gender distribution, and race×gender combination distribution. All demographic data fetched via single `DemographicBreakdown` query. |
```

Update the `/ui/dashboard` route description:

```
| `/ui/dashboard` | `GET` | Executive overview: responsive stat-card grid (total population, 30-day active ratio, max-level count), race distribution doughnut chart, expansion MSQ completion card (sorted by config release order), 30-day time-series line chart, and region summary with world drill-down. Queries optimized: 4 concurrent goroutines, 4 DB queries (SummaryCounts, MultiBreakdown, ExpansionCompletions, NewCharactersPerDay). |
```

---

## Critical Files & Anchors

| File | Symbol/Region | Why |
|------|--------------|-----|
| `cmd/http/ui/dashboard.go:55-200` | `Dashboard()` handler | Expansion sort fix + query optimization refactor |
| `cmd/http/ui/races.go:1-158` | `Races()` handler | Add concurrent demographic fetching, new view data fields |
| `cmd/http/ui/races.go:14-30` | `RacesViewData` struct | Add TribeLabels/Data, GenderLabels/Data, RaceGenderLabels/Data |
| `port/contract/character_repository.go:80-95` | Interface | Add `SummaryCounts`, `MultiBreakdown`, `DemographicBreakdown` |
| `port/contract/character_repository.go:56-70` | Types | Add `DemographicCounts` struct |
| `infrastructure/postgres/repository/character.go:454-500` | `Breakdown()` | Pattern for new SQL; insert new methods after this |
| `mock/repository/character.go:30-45` | Struct fields | Add error fields for new methods |
| `domain/census/service.go:650-655` | `ExpansionCompletions()` | Insert new service methods after this |
| `cmd/http/ui/templates/races.html:95-123` | Race table + script | Insert new chart panels before `<script>`, new Chart.js in script |

## Verification

1. **Unit tests:** `go test -v ./cmd/http/ui/...` — all existing tests pass, new tests pass
2. **Expansion sort:** `TestDashboardHandler_ExpansionSortOrder` — "A Realm Reborn" appears before "Dawntrail" in dashboard HTML
3. **Demographic charts:** `TestRacesHandler_DemographicPieCharts` — canvas elements present for tribe, gender, race×gender on races page
4. **Filtered demographics:** `TestRacesHandler_DemographicChartsFiltered` — filters propagate to demographic data
5. **Lint:** `make lint` passes
6. **Full suite:** `make test` passes

## Assumptions & Contingencies

- **Gender encoding:** `characters.gender` is `uint8` (1=Male, 2=Female). SQL `CASE` maps these. Other values → "Unknown".
- **Tribe field:** `characters.tribe` is a string. Empty strings excluded from tribe breakdown.
- **Race×Gender key format:** `"Race|Gender"` pipe-delimited. Template displays the key directly (e.g. "Hyur Male").
- **Parameter indexing in DemographicBreakdown:** Each UNION ALL branch gets unique parameter indices via `characterFilterWhereWithStart` with incremented start values. Critical — reusing indices causes PostgreSQL to reject the query.
- **Backward compatibility:** Existing `Summary()`, `Breakdown()`, `ExpansionCompletions()` methods unchanged. New methods are additions only.
- **`breakdownColumns` whitelist:** Not modified. `DemographicBreakdown` is a standalone method that doesn't go through `Breakdown()`.

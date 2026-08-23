# Fix Lodestone Character Job Sync — ClassJobID Always Zero

## Context

Character jobs and levels **are** being synced on every character census (both initial discovery via `id-sweep` and re-census via `character-census`). The full pipeline works: fetch → convert → `Upsert` (delete-all character_jobs + re-insert each job). The Tomestone path is correct — it receives real `ClassJobID` values from the REST API.

**No extra request needed.** `FetchCharacter` scrapes a single URL (`https://na.finalfantasyxiv.com/lodestone/character/{id}/`) and `parseCharacterProfile` extracts everything from that one HTML response — name, world, race, bio, active job, AND class/job levels (via `parseClassJobs` at line 549). Job data is already available in the existing character-census fetch; no separate consumer or job-sync event required.

**The bug:** The Lodestone HTML scraper `parseClassJobs` in `infrastructure/lodestone/client.go:643-697` never sets `ClassJobID` on the `ClassJobRecord`. It defaults to `0` (Go zero value). Since the `character_jobs` primary key is `(character_id, class_job_id)`, every Lodestone-scraped job collides on `class_job_id = 0` — the UPSERT's `ON CONFLICT (character_id, class_job_id) DO UPDATE` overwrites the same row repeatedly, leaving only the **last** job parsed from the HTML. All other jobs are lost.

This means:
- Characters synced via Lodestone (primary source) have exactly **1 job** in the DB (the last one parsed), with `class_job_id = 0`.
- Characters synced via Tomestone (fallback) have all jobs with correct IDs.
- The UI `buildJobGroups` matches by **name** (not ID), so if the one surviving Lodestone job happens to be in the hardcoded list, it shows — but all others show level 0.

The Lodestone HTML does **not** contain numeric job IDs — only job names (e.g. "Paladin", "Warrior") and levels. The fix is a static name→ID lookup table applied during parsing.

## Approach

### Step 0: Save this plan to docs/superpowers/plans/

**First action before any code changes.** Write this plan to `docs/superpowers/plans/2026-08-23-lodestone-job-id-sync.md`. This ensures the plan is versioned with the codebase and follows the project convention.

### Step 1: Add a static job name → ID lookup table

**File:** `infrastructure/lodestone/client.go`

Add a package-level `var` mapping every FFXIV class/job name (as it appears on the Lodestone HTML) to its official `ClassJobID`. Place it after the existing `var` block (after `entityMap` / `multiSpaceRe` declarations, before function definitions).

```go
// lodestoneJobIDs maps English job/class names (as rendered on The Lodestone
// character profile page) to their official FFXIV ClassJobID values.
// These IDs are stable across patches — Square Enix never reassigns existing IDs.
// New jobs (e.g. from expansions) need entries added here.
var lodestoneJobIDs = map[string]uint8{
	// Tanks
	"Paladin":     19,
	"Warrior":     21,
	"Dark Knight": 32,
	"Gunbreaker":  37,
	// Healers
	"White Mage":  24,
	"Scholar":     28,
	"Astrologian": 33,
	"Sage":        40,
	// Melee DPS
	"Monk":    20,
	"Dragoon": 22,
	"Ninja":   30,
	"Samurai": 34,
	"Reaper":  39,
	"Viper":   41,
	// Physical Ranged DPS
	"Bard":      23,
	"Machinist": 31,
	"Dancer":    38,
	// Magic Ranged DPS
	"Black Mage":  25,
	"Summoner":    27,
	"Red Mage":    35,
	"Pictomancer": 42,
	"Blue Mage":   36,
	// Disciples of the Hand (Crafters)
	"Carpenter":     8,
	"Blacksmith":    9,
	"Armorer":       10,
	"Goldsmith":     11,
	"Leatherworker": 12,
	"Weaver":        13,
	"Alchemist":     14,
	"Culinarian":    15,
	// Disciples of the Land (Gatherers)
	"Miner":    16,
	"Botanist": 17,
	"Fisher":   18,
	// Base Classes (still appear on Lodestone for some characters)
	"Gladiator": 1,
	"Pugilist":  2,
	"Marauder":  3,
	"Lancer":    4,
	"Archer":    5,
	"Conjurer":  6,
	"Thaumaturge": 7,
	"Arcanist":  26,
	"Rogue":     29,
}
```

### Step 2: Look up ClassJobID in parseClassJobs

**File:** `infrastructure/lodestone/client.go`, function `parseClassJobs` (line ~643)

Replace the current job append block (lines ~688-694) that looks like:

```go
if jobName != "" {
    jobs = append(jobs, contract.ClassJobRecord{
        CharacterID: charID,
        Name:        jobName,
        Level:       uint8(level),
    })
}
```

With:

```go
if jobName != "" {
    classJobID, ok := lodestoneJobIDs[jobName]
    if !ok {
        // Unknown job name — skip to avoid inserting with class_job_id=0
        // which would collide in the (character_id, class_job_id) primary key.
        searchFrom = entryIdx + entryEnd + 1
        continue
    }
    jobs = append(jobs, contract.ClassJobRecord{
        CharacterID: charID,
        ClassJobID:  classJobID,
        Name:        jobName,
        Level:       uint8(level),
    })
}
```

**Why skip unknown names instead of logging:** The `parseClassJobs` function has no logger parameter. Adding one would change its signature and all callers. Skipping is safe — unknown names are either new expansion jobs (need a map update) or HTML parsing artifacts. Both cases are better than inserting a row with `class_job_id=0` that corrupts the data.

### Step 3: Add comprehensive tests for parseClassJobs

**File:** `infrastructure/lodestone/client_test.go`

Add the following tests. These use inline HTML fixtures matching the real Lodestone DOM structure (same pattern as the existing `TestParseCharacterProfile_RealLodestoneHTML`).

```go
func TestParseClassJobs_SetsClassJobID(t *testing.T) {
	// Minimal HTML mimicking Lodestone's character__level__list structure.
	// Each entry has a name and level element.
	html := `
<div class="character__level__list">
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Paladin</p>
		<p class="character__level__list__level">Lv.90</p>
	</div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Warrior</p>
		<p class="character__level__list__level">Lv.80</p>
	</div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">White Mage</p>
		<p class="character__level__list__level">Lv.100</p>
	</div>
</div>`

	jobs := parseClassJobs(html, 12345)
	if len(jobs) != 3 {
		t.Fatalf("parseClassJobs returned %d jobs, want 3", len(jobs))
	}

	tests := []struct {
		name       string
		classJobID uint8
		level      uint8
	}{
		{"Paladin", 19, 90},
		{"Warrior", 21, 80},
		{"White Mage", 24, 100},
	}

	for i, tt := range tests {
		if jobs[i].CharacterID != 12345 {
			t.Errorf("job[%d].CharacterID = %d, want 12345", i, jobs[i].CharacterID)
		}
		if jobs[i].ClassJobID != tt.classJobID {
			t.Errorf("job[%d].ClassJobID = %d, want %d (%s)", i, jobs[i].ClassJobID, tt.classJobID, tt.name)
		}
		if jobs[i].Name != tt.name {
			t.Errorf("job[%d].Name = %q, want %q", i, jobs[i].Name, tt.name)
		}
		if jobs[i].Level != tt.level {
			t.Errorf("job[%d].Level = %d, want %d", i, jobs[i].Level, tt.level)
		}
	}
}

func TestParseClassJobs_SkipsUnknownJobName(t *testing.T) {
	html := `
<div class="character__level__list">
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Paladin</p>
		<p class="character__level__list__level">Lv.90</p>
	</div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">FutureJobNotInMap</p>
		<p class="character__level__list__level">Lv.50</p>
	</div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Warrior</p>
		<p class="character__level__list__level">Lv.80</p>
	</div>
</div>`

	jobs := parseClassJobs(html, 99999)
	if len(jobs) != 2 {
		t.Fatalf("parseClassJobs returned %d jobs, want 2 (unknown name should be skipped)", len(jobs))
	}
	if jobs[0].Name != "Paladin" || jobs[0].ClassJobID != 19 {
		t.Errorf("job[0] = {Name:%q, ClassJobID:%d}, want {Paladin, 19}", jobs[0].Name, jobs[0].ClassJobID)
	}
	if jobs[1].Name != "Warrior" || jobs[1].ClassJobID != 21 {
		t.Errorf("job[1] = {Name:%q, ClassJobID:%d}, want {Warrior, 21}", jobs[1].Name, jobs[1].ClassJobID)
	}
}

func TestParseClassJobs_NoClassJobIDZero(t *testing.T) {
	// Verify that NO job ever gets ClassJobID=0 after the fix.
	// This is the regression test for the original bug.
	html := `
<div class="character__level__list">
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Paladin</p>
		<p class="character__level__list__level">Lv.90</p>
	</div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Dark Knight</p>
		<p class="character__level__list__level">Lv.80</p>
	</div>
</div>`

	jobs := parseClassJobs(html, 12345)
	for _, j := range jobs {
		if j.ClassJobID == 0 {
			t.Errorf("job %q has ClassJobID=0 — this was the original bug", j.Name)
		}
	}
}

func TestParseClassJobs_AllKnownJobs(t *testing.T) {
	// Build HTML with ALL known jobs to verify every entry in the lookup table.
	var entries string
	for name := range lodestoneJobIDs {
		entries += `<div class="character__level__list__entry">
			<p class="character__level__list__name">` + name + `</p>
			<p class="character__level__list__level">Lv.50</p>
		</div>`
	}
	html := `<div class="character__level__list">` + entries + `</div>`

	jobs := parseClassJobs(html, 12345)
	if len(jobs) != len(lodestoneJobIDs) {
		t.Fatalf("parseClassJobs returned %d jobs, want %d (all known jobs)", len(jobs), len(lodestoneJobIDs))
	}

	for _, j := range jobs {
		expectedID, ok := lodestoneJobIDs[j.Name]
		if !ok {
			t.Errorf("unexpected job name %q returned", j.Name)
			continue
		}
		if j.ClassJobID != expectedID {
			t.Errorf("job %q ClassJobID = %d, want %d", j.Name, j.ClassJobID, expectedID)
		}
	}
}

func TestParseClassJobs_SpanTags(t *testing.T) {
	// Lodestone sometimes uses <span> instead of <p> for name/level.
	html := `
<div class="character__level__list">
	<div class="character__level__list__entry">
		<span class="character__level__list__name">Monk</span>
		<span class="character__level__list__level">Lv.70</span>
	</div>
</div>`

	jobs := parseClassJobs(html, 12345)
	if len(jobs) != 1 {
		t.Fatalf("parseClassJobs returned %d jobs, want 1", len(jobs))
	}
	if jobs[0].ClassJobID != 20 {
		t.Errorf("ClassJobID = %d, want 20 (Monk)", jobs[0].ClassJobID)
	}
	if jobs[0].Level != 70 {
		t.Errorf("Level = %d, want 70", jobs[0].Level)
	}
}

func TestParseClassJobs_EmptyList(t *testing.T) {
	jobs := parseClassJobs(`<div class="character__level__list"></div>`, 12345)
	if len(jobs) != 0 {
		t.Errorf("parseClassJobs returned %d jobs for empty list, want 0", len(jobs))
	}
}

func TestParseClassJobs_NoListSection(t *testing.T) {
	jobs := parseClassJobs(`<div>no level list here</div>`, 12345)
	if len(jobs) != 0 {
		t.Errorf("parseClassJobs returned %d jobs for missing section, want 0", len(jobs))
	}
}
```

### Step 4: Add integration test verifying no HTML contamination in job names

**File:** `infrastructure/lodestone/client_test.go`

This test verifies that `parseCharacterProfile` (which calls `parseClassJobs`) produces clean text in job names — no HTML tags, no entities. This addresses the user's concern about HTML leaking into the database.

```go
func TestParseCharacterProfile_JobsNoHTMLContamination(t *testing.T) {
	// HTML with tags inside job name/level elements — mimics Lodestone
	// wrapping job names in <a> or <i> tags.
	html := `
<p class="frame__chara__name"><a href="/lodestone/character/12345">Test Char</a></p>
<p class="frame__chara__world">Ultros [Primal]</p>
<div class="character__profile__state"><p>Adventurer</p></div>
<p class="character-block__name">Hyur<br />Midlander / ♂</p>
<p class="character-block__name">-</p>
<p class="character-block__name">-</p>
<p class="character-block__name">-</p>
<div class="character__level__list">
	<div class="character__level__list__entry">
		<p class="character__level__list__name"><a href="/lodestone/character/12345/class_job">Paladin</a></p>
		<p class="character__level__list__level"><span>Lv.</span>90</p>
	</div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name"><i class="xiv-lds xiv-lds-tank"></i> Warrior</p>
		<p class="character__level__list__level">Lv.80</p>
	</div>
</div>`

	profile, err := parseCharacterProfile(html, 12345)
	if err != nil {
		t.Fatalf("parseCharacterProfile: %v", err)
	}

	if len(profile.ClassJobs) != 2 {
		t.Fatalf("ClassJobs count = %d, want 2", len(profile.ClassJobs))
	}

	for _, j := range profile.ClassJobs {
		// No HTML tags in job names
		if strings.Contains(j.Name, "<") || strings.Contains(j.Name, ">") {
			t.Errorf("job name %q contains HTML tags — contamination risk", j.Name)
		}
		// No HTML entities in job names
		if strings.Contains(j.Name, "&") {
			t.Errorf("job name %q contains HTML entities — contamination risk", j.Name)
		}
		// ClassJobID must not be zero
		if j.ClassJobID == 0 {
			t.Errorf("job %q has ClassJobID=0", j.Name)
		}
	}

	if profile.ClassJobs[0].Name != "Paladin" {
		t.Errorf("job[0].Name = %q, want %q", profile.ClassJobs[0].Name, "Paladin")
	}
	if profile.ClassJobs[1].Name != "Warrior" {
		t.Errorf("job[1].Name = %q, want %q", profile.ClassJobs[1].Name, "Warrior")
	}
}
```

### Step 5: Multi-character smoke test with varied job sets

**File:** `infrastructure/lodestone/client_test.go`

Test with different character profiles that have different job subsets — a max-level character (all jobs), a casual character (few jobs), and a new character (no jobs).

```go
func TestParseClassJobs_VariousCharacterProfiles(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		wantJobs int
		wantIDs  []uint8 // expected ClassJobIDs
	}{
		{
			name: "max_level_character_all_jobs",
			html: `<div class="character__level__list">
				<div class="character__level__list__entry"><p class="character__level__list__name">Paladin</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Warrior</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Dark Knight</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Gunbreaker</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">White Mage</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Scholar</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Astrologian</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Sage</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Monk</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Dragoon</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Ninja</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Samurai</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Reaper</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Viper</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Bard</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Machinist</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Dancer</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Black Mage</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Summoner</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Red Mage</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Pictomancer</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Blue Mage</p><p class="character__level__list__level">Lv.80</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Carpenter</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Blacksmith</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Armorer</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Goldsmith</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Leatherworker</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Weaver</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Alchemist</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Culinarian</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Miner</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Botanist</p><p class="character__level__list__level">Lv.100</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Fisher</p><p class="character__level__list__level">Lv.100</p></div>
			</div>`,
			wantJobs: 33,
		},
		{
			name: "casual_character_few_jobs",
			html: `<div class="character__level__list">
				<div class="character__level__list__entry"><p class="character__level__list__name">Paladin</p><p class="character__level__list__level">Lv.50</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">White Mage</p><p class="character__level__list__level">Lv.30</p></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Miner</p><p class="character__level__list__level">Lv.15</p></div>
			</div>`,
			wantJobs: 3,
			wantIDs:  []uint8{19, 24, 16},
		},
		{
			name:     "new_character_no_jobs",
			html:     `<div class="character__level__list"></div>`,
			wantJobs: 0,
		},
		{
			name:     "missing_level_list_section",
			html:     `<div>no job data here</div>`,
			wantJobs: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			jobs := parseClassJobs(tt.html, 12345)
			if len(jobs) != tt.wantJobs {
				t.Fatalf("got %d jobs, want %d", len(jobs), tt.wantJobs)
			}
			for _, j := range jobs {
				if j.ClassJobID == 0 {
					t.Errorf("job %q has ClassJobID=0", j.Name)
				}
				if j.CharacterID != 12345 {
					t.Errorf("job %q has CharacterID=%d, want 12345", j.Name, j.CharacterID)
				}
			}
			if tt.wantIDs != nil {
				for i, wantID := range tt.wantIDs {
					if i >= len(jobs) {
						break
					}
					if jobs[i].ClassJobID != wantID {
						t.Errorf("job[%d].ClassJobID = %d, want %d", i, jobs[i].ClassJobID, wantID)
					}
				}
			}
		})
	}
}
```

### Step 6: Verify existing tests still pass

Run the full Lodestone test suite and the handler tests (which use mock clients, not real HTML):

```bash
go test -v ./infrastructure/lodestone/
go test -v ./domain/census/handler/
go test -v ./domain/census/
make test
```

### Step 7: Update documentation

After implementation, update these docs:

**`docs/lodestone.md`** — Add a section documenting the `lodestoneJobIDs` lookup table and the `parseClassJobs` behavior:

```markdown
### Job Level Parsing

`parseClassJobs` extracts class/job entries from the `character__level__list` HTML
section. The Lodestone HTML provides job names (e.g. "Paladin") but not numeric IDs.
A static lookup table (`lodestoneJobIDs`) maps each known job name to its official
`ClassJobID`. Unknown job names are skipped to avoid inserting rows with
`class_job_id=0` (which would collide in the primary key).

When a new expansion adds jobs, add entries to `lodestoneJobIDs` in
`infrastructure/lodestone/client.go`.
```

**`docs/census.md`** — In the `character_jobs` table section, note that `class_job_id` values come from either the Tomestone REST API (direct) or the Lodestone name→ID lookup table (indirect).

## Critical files & anchors

| File | Symbol/Region | Why |
|---|---|---|
| `infrastructure/lodestone/client.go:643-697` | `parseClassJobs` | The broken function — needs ClassJobID lookup |
| `infrastructure/lodestone/client.go:688-694` | job append block | Where ClassJobID must be set (currently missing) |
| `infrastructure/lodestone/client_test.go` | entire file | Add 7 new test functions |
| `port/contract/census.go:44-51` | `ClassJobRecord` | Struct definition — ClassJobID field is `uint8` |
| `docs/lodestone.md` | Job Level Parsing section | Document the lookup table |

## Verification

1. **Unit tests pass:** `go test -v -run TestParseClassJobs ./infrastructure/lodestone/` — all 7 new tests pass.

2. **No ClassJobID=0 regression:** `TestParseClassJobs_NoClassJobIDZero` explicitly asserts no job gets ID 0.

3. **HTML contamination check:** `TestParseCharacterProfile_JobsNoHTMLContamination` verifies job names contain no `<`, `>`, or `&` characters after parsing.

4. **All known jobs covered:** `TestParseClassJobs_AllKnownJobs` iterates every entry in `lodestoneJobIDs` and verifies the lookup table is complete.

5. **Multi-character profiles:** `TestParseClassJobs_VariousCharacterProfiles` tests max-level (33 jobs), casual (3 jobs), new (0 jobs), and missing-section edge cases.

6. **Existing tests unbroken:** `make test` — full suite passes with no regressions.

7. **Manual DB verification (if running):** After deploying, trigger a character-census for a known Lodestone-synced character and query:
   ```sql
   SELECT class_job_id, name, level FROM character_jobs
   WHERE character_id = <id> ORDER BY class_job_id;
   ```
   Before fix: 1 row with `class_job_id=0`. After fix: multiple rows with correct IDs (e.g. 19 for Paladin, 21 for Warrior, etc.).

## Assumptions & Contingencies

- **Lodestone HTML job names match the map exactly.** If Lodestone changes names (e.g. "Dark Knight" → "DarkKnight"), the lookup fails and that job is skipped (not inserted with ID 0). The `TestParseClassJobs_AllKnownJobs` test catches map completeness issues at CI time.
- **ClassJobID values are stable across FFXIV patches.** Square Enix has never changed existing job IDs; new jobs get new IDs. The map only needs updating when new jobs are added.
- **Tomestone path is unaffected.** It already provides correct IDs from its REST API.
- **If a new expansion adds jobs before the map is updated:** Those jobs are silently skipped for Lodestone-synced characters. Tomestone-synced characters are unaffected. The fix is a one-line addition to `lodestoneJobIDs`.

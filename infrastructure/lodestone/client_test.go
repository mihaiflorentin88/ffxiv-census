package lodestone

import "testing"

func TestStripTags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain text", "Hello World", "Hello World"},
		{"single tag", `<a href="x">Name</a>`, "Name"},
		{"nested tags", `<div><b>Bold</b> text</div>`, "Bold text"},
		{"html entities", `It&#39;s &amp; &lt;test&gt;`, "It's & <test>"},
		{"mixed content", `<a href="x">Name</a> on <i>World</i>`, "Name on World"},
		{"whitespace collapse", "  lots   of   space  ", "lots of space"},
		{"nbsp entity", `hello&nbsp;world`, "hello world"},
		{"br tag becomes space", `Hyur<br />Highlander / ♂`, "Hyur Highlander / ♂"},
		{"empty string", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTags(tt.in)
			if got != tt.want {
				t.Errorf("stripTags(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestExtractTextBetween_StripsTags(t *testing.T) {
	html := `<p class="frame__chara__name"><a href="/character/123">Tataru Taru</a></p>`
	got := extractTextBetween(html, `class="frame__chara__name"`, "</p>")
	want := "Tataru Taru"
	if got != want {
		t.Errorf("extractTextBetween = %q, want %q", got, want)
	}
}

func TestExtractAllTextBetween_StripsTags(t *testing.T) {
	html := `<p class="character-block__name"><a href="x">Hyur</a></p><p class="character-block__name"><i>Midlander</i></p>`
	got := extractAllTextBetween(html, `class="character-block__name"`, "</p>")
	want := []string{"Hyur", "Midlander"}
	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("result[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestParseCharacterProfile_RealLodestoneHTML tests with actual Lodestone HTML
// patterns that caused contamination (i tags in world, br tags in race/tribe,
// nested tags in FC name).
func TestParseCharacterProfile_RealLodestoneHTML(t *testing.T) {
	// Simulate actual Lodestone HTML structure that caused contamination
	html := `
	<p class="frame__chara__name"><a href="/lodestone/character/12345">Tataru Taru</a></p>
	<p class="frame__chara__world"><i class="xiv-lds xiv-lds-home-world js__tooltip" data-tooltip="Home World"></i>Ultros [Primal]</p>
	<div class="character__profile__state"><p>Adventurer</p></div>
	<p class="character-block__name">Miqo&#39;te<br />Keeper of the Moon / ♀</p>
	<p class="character-block__name">Nophica, the Matron</p>
	<p class="character-block__name">Gridania</p>
	<p class="character-block__name">Immortal Flames / Flame Captain</p>
	<div class="character__freecompany__name"><p>Free Company</p><h4><a href="/lodestone/freecompany/1234567890/">My Free Company</a></h4></div>
	`
	profile, err := parseCharacterProfile(html, 12345)
	if err != nil {
		t.Fatalf("parseCharacterProfile: %v", err)
	}

	// Name should be clean text
	if profile.Name != "Tataru Taru" {
		t.Errorf("Name = %q, want %q", profile.Name, "Tataru Taru")
	}

	// World should NOT contain <i> tags - this was the main contamination
	if profile.World != "Ultros" {
		t.Errorf("World = %q, want %q", profile.World, "Ultros")
	}
	if profile.Datacenter != "Primal" {
		t.Errorf("Datacenter = %q, want %q", profile.Datacenter, "Primal")
	}

	// Race should be just the race name
	if profile.Race != "Miqo'te" {
		t.Errorf("Race = %q, want %q", profile.Race, "Miqo'te")
	}

	// Tribe should be parsed from first character-block__name, NOT the patron deity
	if profile.Tribe != "Keeper of the Moon" {
		t.Errorf("Tribe = %q, want %q (patron deity 'Nophica, the Matron' must NOT be stored as tribe)", profile.Tribe, "Keeper of the Moon")
	}

	// Gender should be extracted from ♀ symbol
	if profile.Gender != 2 {
		t.Errorf("Gender = %d, want %d", profile.Gender, 2)
	}

	// Grand Company should be just the company name, not rank
	if profile.GrandCompany != "Immortal Flames" {
		t.Errorf("GrandCompany = %q, want %q", profile.GrandCompany, "Immortal Flames")
	}

	// FC Name should NOT contain "Free Company" prefix or HTML tags
	if profile.FreeCompanyName != "My Free Company" {
		t.Errorf("FreeCompanyName = %q, want %q", profile.FreeCompanyName, "My Free Company")
	}

	// FC ID should be extracted from href
	if profile.FreeCompanyID != "1234567890" {
		t.Errorf("FreeCompanyID = %q, want %q", profile.FreeCompanyID, "1234567890")
	}
}

// TestParseCharacterProfile_AuRa tests the two-word "Au Ra" race name parsing.
func TestParseCharacterProfile_AuRa(t *testing.T) {
	html := `
	<p class="frame__chara__name"><a href="/lodestone/character/203">Test Au Ra</a></p>
	<p class="frame__chara__world"><i class="xiv-lds"></i>Tonberry [Elemental]</p>
	<p class="character-block__name">Au Ra<br />Xaela / ♀</p>
	<p class="character-block__name">Nald'thal, the Traders</p>
	<p class="character-block__name">Gridania</p>
	<p class="character-block__name">Maelstrom / Second Storm Lieutenant</p>
	`
	profile, err := parseCharacterProfile(html, 203)
	if err != nil {
		t.Fatalf("parseCharacterProfile: %v", err)
	}
	if profile.Race != "Au Ra" {
		t.Errorf("Race = %q, want %q", profile.Race, "Au Ra")
	}
	if profile.Tribe != "Xaela" {
		t.Errorf("Tribe = %q, want %q", profile.Tribe, "Xaela")
	}
	if profile.Gender != 2 {
		t.Errorf("Gender = %d, want %d", profile.Gender, 2)
	}
}

// TestParseCharacterProfile_MaleGender tests male gender parsing from Lodestone HTML.
func TestParseCharacterProfile_MaleGender(t *testing.T) {
	html := `
	<p class="frame__chara__name"><a href="/lodestone/character/999">Test Char</a></p>
	<p class="frame__chara__world"><i class="xiv-lds"></i>Hyperion [Primal]</p>
	<p class="character-block__name">Hyur<br />Highlander / ♂</p>
	<p class="character-block__name">Halone, the Fury</p>
	`
	profile, err := parseCharacterProfile(html, 999)
	if err != nil {
		t.Fatalf("parseCharacterProfile: %v", err)
	}
	if profile.Race != "Hyur" {
		t.Errorf("Race = %q, want %q", profile.Race, "Hyur")
	}
	if profile.Tribe != "Highlander" {
		t.Errorf("Tribe = %q, want %q", profile.Tribe, "Highlander")
	}
	if profile.Gender != 1 {
		t.Errorf("Gender = %d, want %d", profile.Gender, 1)
	}
}

// TestStripTags_LodestoneEntities tests HTML entities found in actual Lodestone pages.
func TestStripTags_LodestoneEntities(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"apostrophe entity", `Miqo&#39;te`, "Miqo'te"},
		{"br tag", `Keeper of the Moon / ♀`, "Keeper of the Moon / ♀"},
		{"i tag with class", `<i class="xiv-lds xiv-lds-home-world"></i>Ultros`, "Ultros"},
		{"a tag in fc name", `<a href="/lodestone/freecompany/123/">My FC</a>`, "My FC"},
		{"nested p h4 a", `<p>Free Company</p><h4><a href="/x/">Name</a></h4>`, "Free Company Name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripTags(tt.in)
			if got != tt.want {
				t.Errorf("stripTags(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseClassJobs_SetsClassJobID(t *testing.T) {
	// Minimal HTML mimicking Lodestone's character__level__list structure.
	// Each entry has a name and level element.
	html := `
<div class="character__level__list">
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Paladin</p>
		<p class="character__level__list__level">Lv.90</p>
	</li></div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Warrior</p>
		<p class="character__level__list__level">Lv.80</p>
	</li></div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">White Mage</p>
		<p class="character__level__list__level">Lv.100</p>
	</li></div>
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
	</li></div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">FutureJobNotInMap</p>
		<p class="character__level__list__level">Lv.50</p>
	</li></div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Warrior</p>
		<p class="character__level__list__level">Lv.80</p>
	</li></div>
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
	</li></div>
	<div class="character__level__list__entry">
		<p class="character__level__list__name">Dark Knight</p>
		<p class="character__level__list__level">Lv.80</p>
	</li></div>
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
		</li></div>`
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
	</li></div>
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

func TestParseClassJobs_VariousCharacterProfiles(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		wantJobs int
		wantIDs  []uint8
	}{
		{
			name: "omni_crafter_max_level",
			html: `<div class="character__level__list">
				<div class="character__level__list__entry"><p class="character__level__list__name">Paladin</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Warrior</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Dark Knight</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Gunbreaker</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">White Mage</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Scholar</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Astrologian</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Sage</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Monk</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Dragoon</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Ninja</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Samurai</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Reaper</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Viper</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Bard</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Machinist</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Dancer</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Black Mage</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Summoner</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Red Mage</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Pictomancer</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Blue Mage</p><p class="character__level__list__level">Lv.80</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Carpenter</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Blacksmith</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Armorer</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Goldsmith</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Leatherworker</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Weaver</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Alchemist</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Culinarian</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Miner</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Botanist</p><p class="character__level__list__level">Lv.100</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Fisher</p><p class="character__level__list__level">Lv.100</p></li></div>
			</div>`,
			wantJobs: 33,
		},
		{
			name: "casual_character_few_jobs",
			html: `<div class="character__level__list">
				<div class="character__level__list__entry"><p class="character__level__list__name">Paladin</p><p class="character__level__list__level">Lv.50</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">White Mage</p><p class="character__level__list__level">Lv.30</p></li></div>
				<div class="character__level__list__entry"><p class="character__level__list__name">Miner</p><p class="character__level__list__level">Lv.15</p></li></div>
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

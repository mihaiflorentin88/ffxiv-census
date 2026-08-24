package lodestone

import (
	"testing"
	"time"
)

func TestExtractTimestamp(t *testing.T) {
	tests := []struct {
		name string
		html string
		want time.Time
	}{
		{
			name: "single ldst_strftime",
			html: `<script>ldst_strftime(1690531200, 'datetime')</script>`,
			want: time.Unix(1690531200, 0),
		},
		{
			name: "multiple ldst_strftime uses last",
			html: `<script>ldst_strftime(1690000000, 'datetime')</script>
			       <script>ldst_strftime(1690531200, 'datetime')</script>`,
			want: time.Unix(1690531200, 0),
		},
		{
			name: "no ldst_strftime returns zero",
			html: `<div>no timestamp here</div>`,
			want: time.Time{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractTimestamp(tt.html)
			if !got.Equal(tt.want) {
				t.Errorf("extractTimestamp() = %v, want %v", got, tt.want)
			}
		})
	}
}

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
	// Real Lodestone HTML: <li><img ... data-tooltip="JobName">Level</li>
	html := `<div class="character__level__list"><ul>
		<li><img src="x" data-tooltip="Paladin">90</li>
		<li><img src="x" data-tooltip="Warrior">80</li>
		<li><img src="x" data-tooltip="White Mage">100</li>
	</ul></div>`

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

func TestParseClassJobs_CombinedNames(t *testing.T) {
	// Lodestone shows "Paladin / Gladiator" for classes with job upgrades.
	html := `<div class="character__level__list"><ul>
		<li><img src="x" data-tooltip="Paladin / Gladiator">100</li>
		<li><img src="x" data-tooltip="Bard / Archer">50</li>
		<li><img src="x" data-tooltip="White Mage / Conjurer">80</li>
	</ul></div>`

	jobs := parseClassJobs(html, 12345)
	if len(jobs) != 3 {
		t.Fatalf("parseClassJobs returned %d jobs, want 3", len(jobs))
	}
	// Should use first part of combined name for lookup.
	if jobs[0].Name != "Paladin" || jobs[0].ClassJobID != 19 {
		t.Errorf("job[0] = {Name:%q, ClassJobID:%d}, want {Paladin, 19}", jobs[0].Name, jobs[0].ClassJobID)
	}
	if jobs[1].Name != "Bard" || jobs[1].ClassJobID != 23 {
		t.Errorf("job[1] = {Name:%q, ClassJobID:%d}, want {Bard, 23}", jobs[1].Name, jobs[1].ClassJobID)
	}
	if jobs[2].Name != "White Mage" || jobs[2].ClassJobID != 24 {
		t.Errorf("job[2] = {Name:%q, ClassJobID:%d}, want {White Mage, 24}", jobs[2].Name, jobs[2].ClassJobID)
	}
}

func TestParseClassJobs_LimitedJobs(t *testing.T) {
	// Blue Mage and Beastmaster have "(Limited Job)" suffix.
	html := `<div class="character__level__list"><ul>
		<li><img src="x" data-tooltip="Blue Mage (Limited Job)">80</li>
		<li><img src="x" data-tooltip="Beastmaster (Limited Job)">-</li>
	</ul></div>`

	jobs := parseClassJobs(html, 12345)
	if len(jobs) != 1 {
		t.Fatalf("parseClassJobs returned %d jobs, want 1 (Beastmaster has level -)", len(jobs))
	}
	if jobs[0].Name != "Blue Mage" || jobs[0].ClassJobID != 36 {
		t.Errorf("job[0] = {Name:%q, ClassJobID:%d}, want {Blue Mage, 36}", jobs[0].Name, jobs[0].ClassJobID)
	}
}

func TestParseClassJobs_SkipsUnleveled(t *testing.T) {
	// Jobs with level "-" should be skipped.
	html := `<div class="character__level__list"><ul>
		<li><img src="x" data-tooltip="Paladin">90</li>
		<li><img src="x" data-tooltip="Warrior">-</li>
		<li><img src="x" data-tooltip="Dark Knight">80</li>
	</ul></div>`

	jobs := parseClassJobs(html, 12345)
	if len(jobs) != 2 {
		t.Fatalf("parseClassJobs returned %d jobs, want 2 (unleveled should be skipped)", len(jobs))
	}
	if jobs[0].Name != "Paladin" || jobs[0].Level != 90 {
		t.Errorf("job[0] = {Name:%q, Level:%d}, want {Paladin, 90}", jobs[0].Name, jobs[0].Level)
	}
	if jobs[1].Name != "Dark Knight" || jobs[1].Level != 80 {
		t.Errorf("job[1] = {Name:%q, Level:%d}, want {Dark Knight, 80}", jobs[1].Name, jobs[1].Level)
	}
}

func TestParseClassJobs_SkipsUnknownJobName(t *testing.T) {
	html := `<div class="character__level__list"><ul>
		<li><img src="x" data-tooltip="Paladin">90</li>
		<li><img src="x" data-tooltip="FutureJobNotInMap">50</li>
		<li><img src="x" data-tooltip="Warrior">80</li>
	</ul></div>`

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
	html := `<div class="character__level__list"><ul>
		<li><img src="x" data-tooltip="Paladin">90</li>
		<li><img src="x" data-tooltip="Dark Knight">80</li>
	</ul></div>`

	jobs := parseClassJobs(html, 12345)
	for _, j := range jobs {
		if j.ClassJobID == 0 {
			t.Errorf("job %q has ClassJobID=0 — this was the original bug", j.Name)
		}
	}
}

func TestParseClassJobs_AllKnownJobs(t *testing.T) {
	var entries string
	for name := range lodestoneJobIDs {
		entries += `<li><img src="x" data-tooltip="` + name + `">50</li>`
	}
	html := `<div class="character__level__list"><ul>` + entries + `</ul></div>`

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

func TestParseClassJobs_EmptyList(t *testing.T) {
	jobs := parseClassJobs(`<div class="character__level__list"><ul></ul></div>`, 12345)
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
	}{
		{
			name: "max_level_omni",
			html: `<div class="character__level__list"><ul>
				<li><img src="x" data-tooltip="Paladin">100</li>
				<li><img src="x" data-tooltip="Warrior">100</li>
				<li><img src="x" data-tooltip="Dark Knight">100</li>
				<li><img src="x" data-tooltip="Gunbreaker">100</li>
				<li><img src="x" data-tooltip="White Mage">100</li>
				<li><img src="x" data-tooltip="Scholar">100</li>
				<li><img src="x" data-tooltip="Astrologian">100</li>
				<li><img src="x" data-tooltip="Sage">100</li>
				<li><img src="x" data-tooltip="Monk">100</li>
				<li><img src="x" data-tooltip="Dragoon">100</li>
				<li><img src="x" data-tooltip="Ninja">100</li>
				<li><img src="x" data-tooltip="Samurai">100</li>
				<li><img src="x" data-tooltip="Reaper">100</li>
				<li><img src="x" data-tooltip="Viper">100</li>
				<li><img src="x" data-tooltip="Bard">100</li>
				<li><img src="x" data-tooltip="Machinist">100</li>
				<li><img src="x" data-tooltip="Dancer">100</li>
				<li><img src="x" data-tooltip="Black Mage">100</li>
				<li><img src="x" data-tooltip="Summoner">100</li>
				<li><img src="x" data-tooltip="Red Mage">100</li>
				<li><img src="x" data-tooltip="Pictomancer">100</li>
				<li><img src="x" data-tooltip="Blue Mage (Limited Job)">80</li>
				<li><img src="x" data-tooltip="Carpenter">100</li>
				<li><img src="x" data-tooltip="Blacksmith">100</li>
				<li><img src="x" data-tooltip="Armorer">100</li>
				<li><img src="x" data-tooltip="Goldsmith">100</li>
				<li><img src="x" data-tooltip="Leatherworker">100</li>
				<li><img src="x" data-tooltip="Weaver">100</li>
				<li><img src="x" data-tooltip="Alchemist">100</li>
				<li><img src="x" data-tooltip="Culinarian">100</li>
				<li><img src="x" data-tooltip="Miner">100</li>
				<li><img src="x" data-tooltip="Botanist">100</li>
				<li><img src="x" data-tooltip="Fisher">100</li>
			</ul></div>`,
			wantJobs: 33,
		},
		{
			name: "casual_few_jobs",
			html: `<div class="character__level__list"><ul>
				<li><img src="x" data-tooltip="Paladin">50</li>
				<li><img src="x" data-tooltip="White Mage">30</li>
				<li><img src="x" data-tooltip="Miner">15</li>
			</ul></div>`,
			wantJobs: 3,
		},
		{
			name:     "new_character_no_jobs",
			html:     `<div class="character__level__list"><ul></ul></div>`,
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
		})
	}
}

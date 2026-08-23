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

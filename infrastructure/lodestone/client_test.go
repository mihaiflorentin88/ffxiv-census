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
	<p class="character-block__name">Miqo&#39;te</p>
	<p class="character-block__name">Keeper of the Moon</p>
	<p class="character__freecompany__name"><a href="/lodestone/freecompany/1234567890/">My Free Company</a></p>
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

	// Race should NOT contain &#39; entity
	if profile.Race != "Miqo'te" {
		t.Errorf("Race = %q, want %q", profile.Race, "Miqo'te")
	}

	// Tribe should be clean
	if profile.Tribe != "Keeper of the Moon" {
		t.Errorf("Tribe = %q, want %q", profile.Tribe, "Keeper of the Moon")
	}

	// FC Name should NOT contain <a> tags
	if profile.FreeCompanyName != "My Free Company" {
		t.Errorf("FreeCompanyName = %q, want %q", profile.FreeCompanyName, "My Free Company")
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
		{"nested p h4 a", `<p>Free Company</p><h4><a href="/x/">Name</a></h4>`, "Free CompanyName"},
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

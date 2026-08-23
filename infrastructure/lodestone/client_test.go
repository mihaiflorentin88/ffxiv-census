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

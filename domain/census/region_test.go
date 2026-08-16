package census

import "testing"

func TestRegionForDatacenter(t *testing.T) {
	cases := []struct{ dc, want string }{
		{"Aether", "NA"}, {"Primal", "NA"}, {"Crystal", "NA"}, {"Dynamis", "NA"},
		{"Chaos", "EU"}, {"Light", "EU"},
		{"Elemental", "JP"}, {"Gaia", "JP"}, {"Mana", "JP"}, {"Meteor", "JP"},
		{"Materia", "OCE"},
		{"Unknown", ""},
	}
	for _, c := range cases {
		if got := RegionForDatacenter(c.dc); got != c.want {
			t.Errorf("RegionForDatacenter(%q) = %q, want %q", c.dc, got, c.want)
		}
	}
}

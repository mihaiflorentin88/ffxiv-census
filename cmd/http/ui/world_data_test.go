package ui

import (
	"testing"
)

func TestDCsForRegion(t *testing.T) {
	tests := []struct {
		name   string
		region string
		want   []string
	}{
		{
			name:   "NA returns four DCs sorted",
			region: "NA",
			want:   []string{"Aether", "Crystal", "Dynamis", "Primal"},
		},
		{
			name:   "EU returns two DCs sorted",
			region: "EU",
			want:   []string{"Chaos", "Light"},
		},
		{
			name:   "JP returns four DCs sorted",
			region: "JP",
			want:   []string{"Elemental", "Gaia", "Mana", "Meteor"},
		},
		{
			name:   "OCE returns Materia",
			region: "OCE",
			want:   []string{"Materia"},
		},
		{
			name:   "empty region returns nil",
			region: "",
			want:   nil,
		},
		{
			name:   "unknown region returns nil",
			region: "MARS",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DCsForRegion(tt.region)
			if len(got) != len(tt.want) {
				t.Fatalf("DCsForRegion(%q) = %v (len %d), want %v (len %d)", tt.region, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("DCsForRegion(%q)[%d] = %q, want %q", tt.region, i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestWorldsForDC(t *testing.T) {
	tests := []struct {
		name string
		dc   string
		want []string
	}{
		{
			name: "Aether returns 8 worlds sorted",
			dc:   "Aether",
			want: []string{
				"Adamantoise", "Cactuar", "Faerie", "Gilgamesh",
				"Jenova", "Midgardsormr", "Sargatanas", "Siren",
			},
		},
		{
			name: "Materia returns 5 worlds sorted",
			dc:   "Materia",
			want: []string{"Bismarck", "Ravana", "Sephirot", "Sophia", "Zurvan"},
		},
		{
			name: "empty DC returns nil",
			dc:   "",
			want: nil,
		},
		{
			name: "unknown DC returns nil",
			dc:   "NonExistent",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WorldsForDC(tt.dc)
			if len(got) != len(tt.want) {
				t.Fatalf("WorldsForDC(%q) = %v (len %d), want %v (len %d)", tt.dc, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("WorldsForDC(%q)[%d] = %q, want %q", tt.dc, i, got[i], tt.want[i])
				}
			}
		})
	}
}

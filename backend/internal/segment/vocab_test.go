package segment

import (
	"slices"
	"testing"
)

func TestCities_returnsCopy(t *testing.T) {
	t.Parallel()
	got := Cities()
	got[0] = "tampered"
	if Cities()[0] == "tampered" {
		t.Error("Cities() exposes the internal slice")
	}
	if !slices.Contains(Cities(), "İstanbul") || slices.Contains(Cities(), "Istanbul") {
		t.Error("canonical list must hold the dotted İstanbul and not the ASCII spelling")
	}
}

func TestLanguages_returnsCopy(t *testing.T) {
	t.Parallel()
	got := Languages()
	got[0] = "tampered"
	if Languages()[0] == "tampered" {
		t.Error("Languages() exposes the internal slice")
	}
}

func TestParseKurus(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in     string
		want   int64
		wantOK bool
	}{
		{"0", 0, true},
		{"100", 10000, true},
		{"1500.5", 150050, true},
		{"1500.50", 150050, true},
		{"0.05", 5, true},
		{"0.5", 50, true},
		{"999999999999.99", 99999999999999, true},
		{"", 0, false},
		{"-5", 0, false},
		{"12.345", 0, false},
		{"12.", 0, false},
		{".5", 0, false},
		{"1e3", 0, false},
		{"1E3", 0, false},
		{"+5", 0, false},
		{"1000000000000", 0, false}, // 13 integer digits
		{"1,5", 0, false},
		{"NaN", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			got, ok := parseKurus(tt.in)
			if got != tt.want || ok != tt.wantOK {
				t.Errorf("parseKurus(%q) = %d, %v; want %d, %v", tt.in, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

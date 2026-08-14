package oreate

import "testing"

func TestSeedanceCreditTableMatchesOfficialConfig(t *testing.T) {
	ranges := []struct {
		start  int
		points []int
	}{
		{14001, []int{11, 15, 23, 38, 45, 98, 21, 38, 45, 90, 98, 203}},
		{14072, []int{38, 38, 105, 105, 98, 98, 218, 218}},
		{14080, []int{60, 60, 135, 135, 338, 338, 120, 120, 270, 270, 683, 683}},
		{14092, []int{60, 98, 143, 120, 203, 285, 135, 210, 300, 270, 435, 600, 345, 540, 750, 675, 1050, 1500}},
		{14110, []int{38, 75, 113, 98, 165, 225, 105, 173, 240, 210, 345, 480}},
		{14198, []int{30, 30, 68, 68, 60, 60, 135, 135}},
		{14206, []int{30, 45, 60, 60, 105, 150, 60, 90, 135, 135, 225, 300}},
		{14218, []int{50, 50, 110, 110, 100, 100, 230, 230, 200, 200, 470, 470, 300, 300, 700, 700}},
		{14234, []int{50, 80, 110, 120, 190, 250, 100, 160, 230, 240, 380, 520, 210, 330, 460, 480, 760, 1000, 200, 500, 690, 720, 1100, 1500}},
	}

	wantEntries := 0
	for _, priceRange := range ranges {
		for offset, want := range priceRange.points {
			aiType := priceRange.start + offset
			if got := seedanceCreditsByAIType[aiType]; got != want {
				t.Errorf("credits for aiType %d = %d, want %d", aiType, got, want)
			}
			wantEntries++
		}
	}
	if len(seedanceCreditsByAIType) != wantEntries {
		t.Fatalf("credit table has %d entries, want %d", len(seedanceCreditsByAIType), wantEntries)
	}
}

func TestSeedanceRequiredCredits(t *testing.T) {
	tests := []struct {
		name              string
		model             string
		resolution        string
		duration          int
		audio             bool
		referenceDuration int
		want              int
	}{
		{name: "mini cheap", model: "seedance-2.0-mini", resolution: "480p", duration: 5, want: 30},
		{name: "fast", model: "seedance-2.0-fast", resolution: "480p", duration: 10, audio: true, want: 98},
		{name: "1.5 pro", model: "seedance-1.5-pro", resolution: "1080p", duration: 10, audio: true, want: 203},
		{name: "2.0", model: "seedance-2.0", resolution: "480p", duration: 10, want: 120},
		{name: "2.5 long", model: "seedance-2.5", resolution: "720p", duration: 30, audio: true, want: 700},
		{name: "mini reference", model: "seedance-2.0-mini", resolution: "720p", duration: 5, referenceDuration: 10, want: 105},
		{name: "fast reference", model: "seedance-2.0-fast", resolution: "480p", duration: 5, referenceDuration: 15, want: 113},
		{name: "2.0 reference", model: "seedance-2.0", resolution: "1080p", duration: 10, referenceDuration: 15, want: 1500},
		{name: "2.5 reference", model: "seedance-2.5", resolution: "480p", duration: 5, referenceDuration: 10, want: 80},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SeedanceRequiredCredits(tt.model, tt.resolution, tt.duration, tt.audio, tt.referenceDuration)
			if err != nil {
				t.Fatalf("SeedanceRequiredCredits() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("SeedanceRequiredCredits() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestSeedanceRequiredCreditsRejectsUnsupportedRequest(t *testing.T) {
	if _, err := SeedanceRequiredCredits("seedance-2.0", "720p", 20, false, 0); err == nil {
		t.Fatal("SeedanceRequiredCredits() unexpectedly succeeded")
	}
}

func TestSeedanceConfig(t *testing.T) {
	tests := []struct {
		model      string
		resolution string
		duration   int
		audio      bool
		name       string
		aiType     int
	}{
		{"seedance-2.0-mini", "480p", 5, false, "Seedance 2.0 Mini", 14198},
		{"seedance-2.0-mini", "720", 10, true, "Seedance 2.0 Mini", 14205},
		{"seedance-2.0-fast", "480", 5, true, "Seedance 2.0 Fast", 14073},
		{"seedance-2.0-fast", "720p", 10, false, "Seedance 2.0 Fast", 14078},
		{"seedance-1.5-pro", "480", 5, false, "Seedance 1.5 Pro", 14001},
		{"seedance-1.5-pro", "1080p", 10, true, "Seedance 1.5 Pro", 14012},
		{"seedance-2.0", "720", 5, true, "Seedance 2.0", 14083},
		{"seedance-2.0", "1080p", 10, false, "Seedance 2.0", 14090},
		{"seedance-2.5", "480p", 5, false, "Seedance 2.5", 14218},
		{"seedance-2.5", "720p", 5, true, "Seedance 2.5", 14221},
		{"seedance-2.5", "480p", 20, false, "Seedance 2.5", 14226},
		{"seedance-2.5", "720p", 30, true, "Seedance 2.5", 14233},
	}
	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.resolution, func(t *testing.T) {
			name, aiType, err := SeedanceConfig(tt.model, tt.resolution, tt.duration, tt.audio)
			if err != nil {
				t.Fatalf("SeedanceConfig() error = %v", err)
			}
			if name != tt.name || aiType != tt.aiType {
				t.Fatalf("SeedanceConfig() = (%q, %d), want (%q, %d)", name, aiType, tt.name, tt.aiType)
			}
		})
	}
}

func TestSeedanceReferenceConfig(t *testing.T) {
	tests := []struct {
		model       string
		resolution  string
		duration    int
		refDuration int
		name        string
		aiType      int
		band        string
	}{
		{"seedance-2.0-mini", "480p", 5, 2, "Seedance 2.0 Mini", 14206, "2-5"},
		{"seedance-2.0-mini", "720p", 10, 6, "Seedance 2.0 Mini", 14216, "6-10"},
		{"seedance-2.0-fast", "480p", 10, 10, "Seedance 2.0 Fast", 14114, "6-10"},
		{"seedance-2.0-fast", "720p", 5, 11, "Seedance 2.0 Fast", 14118, "10-15"},
		{"seedance-2.0", "1080p", 10, 15, "Seedance 2.0", 14109, "10-15"},
		{"seedance-2.5", "480p", 20, 5, "Seedance 2.5", 14246, "2-5"},
		{"seedance-2.5", "720p", 30, 10, "Seedance 2.5", 14256, "6-10"},
	}
	for _, tt := range tests {
		t.Run(tt.model+"/"+tt.band, func(t *testing.T) {
			name, aiType, band, err := SeedanceReferenceConfig(tt.model, tt.resolution, tt.duration, tt.refDuration)
			if err != nil {
				t.Fatalf("SeedanceReferenceConfig() error = %v", err)
			}
			if name != tt.name || aiType != tt.aiType || band != tt.band {
				t.Fatalf("SeedanceReferenceConfig() = (%q, %d, %q), want (%q, %d, %q)", name, aiType, band, tt.name, tt.aiType, tt.band)
			}
		})
	}
}

func TestSeedanceReferenceConfigRejectsUnsupportedValues(t *testing.T) {
	for _, tt := range []struct {
		name        string
		model       string
		resolution  string
		duration    int
		refDuration int
	}{
		{"model", "seedance-1.5-pro", "480", 5, 5},
		{"resolution", "seedance-2.0-mini", "1080", 5, 5},
		{"duration", "seedance-2.0", "480", 20, 5},
		{"reference-too-short", "seedance-2.5", "480", 5, 1},
		{"reference-too-long", "seedance-2.5", "480", 5, 16},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, _, err := SeedanceReferenceConfig(tt.model, tt.resolution, tt.duration, tt.refDuration); err == nil {
				t.Fatal("SeedanceReferenceConfig() unexpectedly succeeded")
			}
		})
	}
}

func TestSeedanceConfigRejectsUnsupportedValues(t *testing.T) {
	for _, tt := range []struct {
		name       string
		model      string
		resolution string
		duration   int
	}{
		{"model", "other", "480", 5},
		{"resolution", "seedance-2.0-mini", "1080", 5},
		{"duration", "seedance-2.0", "480", 3},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if _, _, err := SeedanceConfig(tt.model, tt.resolution, tt.duration, false); err == nil {
				t.Fatal("SeedanceConfig() unexpectedly succeeded")
			}
		})
	}
}

func TestValidRatio(t *testing.T) {
	for _, ratio := range []string{"16:9", "1:1", "3:4", "4:3", "9:16", "21:9"} {
		if !validRatio(ratio) {
			t.Errorf("validRatio(%q) = false", ratio)
		}
	}
	for _, ratio := range []string{"", "2:3", "16/9"} {
		if validRatio(ratio) {
			t.Errorf("validRatio(%q) = true", ratio)
		}
	}
}

package oreate

import "testing"

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

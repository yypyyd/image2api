package handler

import "testing"

func TestNormalizeVideoResolutionOverride(t *testing.T) {
	for _, tt := range []struct {
		input string
		want  string
	}{
		{"480", "480p"},
		{" 480P ", "480p"},
		{"1080p", "1080p"},
		{"", ""},
		{"auto", "auto"},
	} {
		if got := normalizeVideoResolutionOverride(tt.input); got != tt.want {
			t.Errorf("normalizeVideoResolutionOverride(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

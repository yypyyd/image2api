package repo

import (
	"reflect"
	"testing"
)

func TestCanonicalRatio(t *testing.T) {
	tests := map[string]string{
		"16x9":    "16:9",
		"9X16":    "9:16",
		" 21x9 ":  "21:9",
		"1:1":     "1:1",
		"auto":    "auto",
		"model-x": "model-x",
	}
	for input, want := range tests {
		if got := CanonicalRatio(input); got != want {
			t.Errorf("CanonicalRatio(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestCanonicalRatiosDoesNotMutateInput(t *testing.T) {
	input := []string{"16x9", "1:1", "9X16"}
	got := CanonicalRatios(input)
	want := []string{"16:9", "1:1", "9:16"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("CanonicalRatios() = %#v, want %#v", got, want)
	}
	if input[0] != "16x9" {
		t.Fatalf("CanonicalRatios mutated its input: %#v", input)
	}
}

package service

import (
	"reflect"
	"testing"

	"backend/internal/model"
	"gorm.io/datatypes"
)

func TestV1ModelEntryStrictByDefault(t *testing.T) {
	item := model.ModelConfig{
		ID:             "upstream-name",
		Alias:          "public-name",
		Provider:       "grok",
		Type:           "video",
		Ratios:         datatypes.JSON([]byte(`["16x9"]`)),
		Resolutions:    datatypes.JSON([]byte(`["720p"]`)),
		DurationPrices: datatypes.JSONMap{"8s": 1, "5s": 1},
	}

	got := v1ModelEntry(item, 123, false)
	want := map[string]any{
		"id":       "public-name",
		"object":   "model",
		"created":  int64(123),
		"owned_by": "grok",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("strict model entry mismatch\n got: %#v\nwant: %#v", got, want)
	}
}

func TestV1ModelEntryExtendedCapabilities(t *testing.T) {
	item := model.ModelConfig{
		ID:                 "video-model",
		Provider:           "adobe",
		Type:               "video",
		Ratios:             datatypes.JSON([]byte(`["16:9"]`)),
		Resolutions:        datatypes.JSON([]byte(`["1080p"]`)),
		DurationPrices:     datatypes.JSONMap{"8s": 1, "5s": 1},
		MaxReferenceImages: 2,
		ReferenceMode:      "frame",
	}

	got := v1ModelEntry(item, 123, true)
	if got["kind"] != "video" {
		t.Fatalf("kind = %#v, want video", got["kind"])
	}
	if !reflect.DeepEqual(got["supported_ratios"], []string{"16:9"}) {
		t.Fatalf("supported_ratios = %#v", got["supported_ratios"])
	}
	if !reflect.DeepEqual(got["supported_resolutions"], []string{"1080p"}) {
		t.Fatalf("supported_resolutions = %#v", got["supported_resolutions"])
	}
	if !reflect.DeepEqual(got["supported_durations"], []string{"5s", "8s"}) {
		t.Fatalf("supported_durations = %#v", got["supported_durations"])
	}
	if got["max_reference_images"] != 2 {
		t.Fatalf("max_reference_images = %#v, want 2", got["max_reference_images"])
	}
	if got["reference_mode"] != "frame" {
		t.Fatalf("reference_mode = %#v, want frame", got["reference_mode"])
	}
}

func TestV1ModelEntryExtendedReferenceDefaults(t *testing.T) {
	item := model.ModelConfig{
		ID:                 "text-model",
		Provider:           "chatgpt",
		Type:               "text",
		MaxReferenceImages: -1,
	}

	got := v1ModelEntry(item, 123, true)
	if got["max_reference_images"] != 0 {
		t.Fatalf("max_reference_images = %#v, want 0", got["max_reference_images"])
	}
	if got["reference_mode"] != "none" {
		t.Fatalf("reference_mode = %#v, want none", got["reference_mode"])
	}
}

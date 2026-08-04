package service

import (
	"errors"
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
		ID:                  "video-model",
		Provider:            "adobe",
		Type:                "video",
		Ratios:              datatypes.JSON([]byte(`["16:9"]`)),
		Resolutions:         datatypes.JSON([]byte(`["1080p"]`)),
		DurationPrices:      datatypes.JSONMap{"8s": 1, "5s": 1},
		MaxReferenceImages:  2,
		MaxReferenceVideos:  1,
		MaxReferenceAudios:  1,
		MaxReferenceMedia:   3,
		SupportsAudioOutput: true,
		ReferenceMode:       "frame",
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
	if got["max_reference_videos"] != 1 || got["max_reference_audios"] != 1 {
		t.Fatalf("media capabilities = %#v/%#v", got["max_reference_videos"], got["max_reference_audios"])
	}
	if got["max_reference_media"] != 3 {
		t.Fatalf("max_reference_media = %#v, want 3", got["max_reference_media"])
	}
	if got["supports_audio_output"] != true {
		t.Fatalf("supports_audio_output = %#v", got["supports_audio_output"])
	}
	if got["reference_mode"] != "frame" {
		t.Fatalf("reference_mode = %#v, want frame", got["reference_mode"])
	}
}

func TestValidateMediaReferences(t *testing.T) {
	if err := validateMediaReferences([]MediaReference{{Data: []byte("video"), ContentType: "video/mp4"}}, "video"); err != nil {
		t.Fatalf("valid MP4 rejected: %v", err)
	}
	if err := validateMediaReferences([]MediaReference{{Data: []byte("video"), ContentType: "video/webm"}}, "video"); err == nil {
		t.Fatal("WebM reference should be rejected")
	}
	if err := validateMediaReferences([]MediaReference{{Data: []byte("audio"), ContentType: "audio/mpeg"}}, "audio"); err != nil {
		t.Fatalf("valid MP3 rejected: %v", err)
	}
}

func TestValidateVideoReferenceLimitsSharedTotal(t *testing.T) {
	item := &model.ModelConfig{
		MaxReferenceImages: 9,
		MaxReferenceVideos: 3,
		MaxReferenceAudios: 3,
		MaxReferenceMedia:  9,
	}
	withinLimit := V1VideoRequest{
		ReferenceImages: make([]string, 3),
		ReferenceVideos: make([]MediaReference, 3),
		ReferenceAudios: make([]MediaReference, 3),
	}
	if err := validateVideoReferenceLimits(item, withinLimit); err != nil {
		t.Fatalf("9 total references rejected: %v", err)
	}
	overLimit := withinLimit
	overLimit.ReferenceImages = make([]string, 4)
	if err := validateVideoReferenceLimits(item, overLimit); !errors.Is(err, ErrUnsupportedParams) {
		t.Fatalf("10 total references error = %v, want ErrUnsupportedParams", err)
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

func TestAdobeAccountSupportsModel(t *testing.T) {
	normal := model.TokenAccount{Meta: datatypes.JSONMap{"cached_quota_total": 10}}
	points := model.TokenAccount{Meta: datatypes.JSONMap{"cached_quota_total": 10000}}

	if !adobeAccountSupportsModel(normal, "firefly-image-5", "image") {
		t.Fatal("ordinary account should support native Image 5")
	}
	if !adobeAccountSupportsModel(normal, "firefly-gpt-image-2", "image") {
		t.Fatal("ordinary account should support partner image models")
	}
	if !adobeAccountSupportsModel(normal, "firefly-nano-banana-2", "image") {
		t.Fatal("ordinary account should support newly added partner image models")
	}
	if adobeAccountSupportsModel(normal, "gemini-veo31-lite", "video") {
		t.Fatal("ordinary account should not be scheduled for partner video models")
	}
	if !adobeAccountSupportsModel(normal, "firefly-video", "video") {
		t.Fatal("ordinary account should support native Firefly Video")
	}
	if !adobeAccountSupportsModel(normal, "gemini-veo31", "video") {
		t.Fatal("ordinary account should support the standard Veo 3.1 route")
	}
	if !adobeAccountSupportsModel(normal, "firefly-ray", "video") {
		t.Fatal("ordinary account should support Luma Ray")
	}
	if !adobeAccountSupportsModel(points, "gemini-veo31-lite", "video") {
		t.Fatal("points account should support partner video models")
	}
}

func TestResolveAdobeVideoEngineVeoLite(t *testing.T) {
	engine, upstream := resolveAdobeVideoEngine("gemini-veo31-lite")
	if engine != "veo31-lite" || upstream != "" {
		t.Fatalf("resolveAdobeVideoEngine() = %q, %q", engine, upstream)
	}
}

func TestResolveNewAdobeVideoEngines(t *testing.T) {
	tests := map[string]string{
		"firefly-kling-3":         "kling-v3",
		"firefly-kling-o3":        "kling-o3",
		"firefly-runway-4.5":      "runway45",
		"firefly-seedance-2":      "seedance20",
		"firefly-seedance-2-fast": "seedance20-fast",
	}
	for modelID, want := range tests {
		engine, upstream := resolveAdobeVideoEngine(modelID)
		if engine != want || upstream != "" {
			t.Fatalf("%s resolved to %q, %q", modelID, engine, upstream)
		}
	}
}

func TestPoolAccountConcurrencyAdobePoints(t *testing.T) {
	ordinary := model.TokenAccount{Meta: datatypes.JSONMap{"cached_quota_total": 10}}
	points := model.TokenAccount{Meta: datatypes.JSONMap{"cached_quota_total": 10000}}
	explicit := model.TokenAccount{Concurrency: 8, Meta: datatypes.JSONMap{"cached_quota_total": 10000}}
	clamped := model.TokenAccount{Concurrency: 99, Meta: datatypes.JSONMap{"cached_quota_total": 10000}}

	if got := poolAccountConcurrency("adobe", ordinary); got != 1 {
		t.Fatalf("ordinary Adobe concurrency = %d, want 1", got)
	}
	if got := poolAccountConcurrency("adobe", points); got != adobePointsConcurrencyPerAccount {
		t.Fatalf("points Adobe concurrency = %d, want %d", got, adobePointsConcurrencyPerAccount)
	}
	if got := poolAccountConcurrency("adobe", explicit); got != 8 {
		t.Fatalf("explicit Adobe concurrency = %d, want 8", got)
	}
	if got := poolAccountConcurrency("adobe", clamped); got != 20 {
		t.Fatalf("clamped Adobe concurrency = %d, want 20", got)
	}
}

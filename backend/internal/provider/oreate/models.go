package oreate

import (
	"fmt"
	"strings"
)

type modelSpec struct {
	Name        string
	BaseAIType  int
	Resolutions map[string]int
	Durations   map[int]int
	AudioStep   int
}

var seedanceModels = map[string]modelSpec{
	"seedance-2.0-mini": {
		Name: "Seedance 2.0 Mini", BaseAIType: 14198,
		Resolutions: map[string]int{"480": 0, "720": 2}, Durations: map[int]int{5: 0, 10: 4}, AudioStep: 1,
	},
	"seedance-2.0-fast": {
		Name: "Seedance 2.0 Fast", BaseAIType: 14072,
		Resolutions: map[string]int{"480": 0, "720": 2}, Durations: map[int]int{5: 0, 10: 4}, AudioStep: 1,
	},
	"seedance-1.5-pro": {
		Name: "Seedance 1.5 Pro", BaseAIType: 14001,
		Resolutions: map[string]int{"480": 0, "720": 2, "1080": 4}, Durations: map[int]int{5: 0, 10: 6}, AudioStep: 1,
	},
	"seedance-2.0": {
		Name: "Seedance 2.0", BaseAIType: 14080,
		Resolutions: map[string]int{"480": 0, "720": 2, "1080": 4}, Durations: map[int]int{5: 0, 10: 6}, AudioStep: 1,
	},
	"seedance-2.5": {
		Name: "Seedance 2.5", BaseAIType: 14218,
		Resolutions: map[string]int{"480": 0, "720": 2}, Durations: map[int]int{5: 0, 10: 4, 20: 8, 30: 12}, AudioStep: 1,
	},
}

type referenceModelSpec struct {
	BaseAIType  int
	Resolutions map[string]int
	Durations   map[int]int
}

var seedanceReferenceModels = map[string]referenceModelSpec{
	"seedance-2.0-mini": {
		BaseAIType: 14206, Resolutions: map[string]int{"480": 0, "720": 3}, Durations: map[int]int{5: 0, 10: 6},
	},
	"seedance-2.0-fast": {
		BaseAIType: 14110, Resolutions: map[string]int{"480": 0, "720": 6}, Durations: map[int]int{5: 0, 10: 3},
	},
	"seedance-2.0": {
		BaseAIType: 14092, Resolutions: map[string]int{"480": 0, "720": 6, "1080": 12}, Durations: map[int]int{5: 0, 10: 3},
	},
	"seedance-2.5": {
		BaseAIType: 14234, Resolutions: map[string]int{"480": 0, "720": 3}, Durations: map[int]int{5: 0, 10: 6, 20: 12, 30: 18},
	},
}

func normalizeModelID(id string) string {
	id = strings.ToLower(strings.TrimSpace(id))
	id = strings.ReplaceAll(id, "_", "-")
	return id
}

func SeedanceConfig(modelID, resolution string, duration int, audio bool) (modelName string, aiType int, err error) {
	spec, ok := seedanceModels[normalizeModelID(modelID)]
	if !ok {
		return "", 0, fmt.Errorf("oreate: unsupported Seedance model %q", modelID)
	}
	resolution = normalizeResolution(resolution)
	resOffset, ok := spec.Resolutions[resolution]
	if !ok {
		return "", 0, fmt.Errorf("oreate: %s does not support resolution %q", spec.Name, resolution)
	}
	durationOffset, ok := spec.Durations[duration]
	if !ok {
		return "", 0, fmt.Errorf("oreate: %s does not support duration %ds", spec.Name, duration)
	}
	aiType = spec.BaseAIType + resOffset + durationOffset
	if audio {
		aiType += spec.AudioStep
	}
	return spec.Name, aiType, nil
}

// SeedanceReferenceConfig maps requests containing at least one reference video
// to Oreate's separate reference-scene aiType table. referenceDuration is the
// rounded-up total length of all reference videos.
func SeedanceReferenceConfig(modelID, resolution string, duration, referenceDuration int) (modelName string, aiType int, durationBand string, err error) {
	modelID = normalizeModelID(modelID)
	model, ok := seedanceModels[modelID]
	if !ok {
		return "", 0, "", fmt.Errorf("oreate: unsupported Seedance model %q", modelID)
	}
	spec, ok := seedanceReferenceModels[modelID]
	if !ok {
		return "", 0, "", fmt.Errorf("oreate: %s does not support reference videos", model.Name)
	}
	resolution = normalizeResolution(resolution)
	resolutionOffset, ok := spec.Resolutions[resolution]
	if !ok {
		return "", 0, "", fmt.Errorf("oreate: %s does not support resolution %q", model.Name, resolution)
	}
	durationOffset, ok := spec.Durations[duration]
	if !ok {
		return "", 0, "", fmt.Errorf("oreate: %s does not support duration %ds", model.Name, duration)
	}
	bandOffset := 0
	switch {
	case referenceDuration >= 2 && referenceDuration <= 5:
		durationBand = "2-5"
	case referenceDuration >= 6 && referenceDuration <= 10:
		durationBand = "6-10"
		bandOffset = 1
	case referenceDuration >= 11 && referenceDuration <= 15:
		durationBand = "10-15"
		bandOffset = 2
	default:
		return "", 0, "", fmt.Errorf("oreate: reference videos must total 2 to 15 seconds")
	}
	return model.Name, spec.BaseAIType + resolutionOffset + durationOffset + bandOffset, durationBand, nil
}

func normalizeResolution(resolution string) string {
	resolution = strings.ToLower(strings.TrimSpace(resolution))
	resolution = strings.TrimSuffix(resolution, "p")
	return strings.TrimSuffix(resolution, "k")
}

func validRatio(ratio string) bool {
	switch strings.TrimSpace(ratio) {
	case "16:9", "1:1", "3:4", "4:3", "9:16", "21:9":
		return true
	default:
		return false
	}
}

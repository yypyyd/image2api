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

// seedanceCreditsByAIType mirrors the point costs returned by Oreate's
// /oreate/aivideo/getmodelconfigv3 endpoint. Keep the explicit aiType mapping:
// several reference-video prices are not derivable from a stable formula.
var seedanceCreditsByAIType = map[int]int{
	// Seedance 1.5 Pro.
	14001: 11, 14002: 15, 14003: 23, 14004: 38, 14005: 45, 14006: 98,
	14007: 21, 14008: 38, 14009: 45, 14010: 90, 14011: 98, 14012: 203,

	// Seedance 2.0 Fast, text/image and reference-video scenes.
	14072: 38, 14073: 38, 14074: 105, 14075: 105,
	14076: 98, 14077: 98, 14078: 218, 14079: 218,
	14110: 38, 14111: 75, 14112: 113, 14113: 98, 14114: 165, 14115: 225,
	14116: 105, 14117: 173, 14118: 240, 14119: 210, 14120: 345, 14121: 480,

	// Seedance 2.0, text/image and reference-video scenes.
	14080: 60, 14081: 60, 14082: 135, 14083: 135, 14084: 338, 14085: 338,
	14086: 120, 14087: 120, 14088: 270, 14089: 270, 14090: 683, 14091: 683,
	14092: 60, 14093: 98, 14094: 143, 14095: 120, 14096: 203, 14097: 285,
	14098: 135, 14099: 210, 14100: 300, 14101: 270, 14102: 435, 14103: 600,
	14104: 345, 14105: 540, 14106: 750, 14107: 675, 14108: 1050, 14109: 1500,

	// Seedance 2.0 Mini, text/image and reference-video scenes.
	14198: 30, 14199: 30, 14200: 68, 14201: 68,
	14202: 60, 14203: 60, 14204: 135, 14205: 135,
	14206: 30, 14207: 45, 14208: 60, 14209: 60, 14210: 105, 14211: 150,
	14212: 60, 14213: 90, 14214: 135, 14215: 135, 14216: 225, 14217: 300,

	// Seedance 2.5, text/image and reference-video scenes.
	14218: 50, 14219: 50, 14220: 110, 14221: 110,
	14222: 100, 14223: 100, 14224: 230, 14225: 230,
	14226: 200, 14227: 200, 14228: 470, 14229: 470,
	14230: 300, 14231: 300, 14232: 700, 14233: 700,
	14234: 50, 14235: 80, 14236: 110, 14237: 120, 14238: 190, 14239: 250,
	14240: 100, 14241: 160, 14242: 230, 14243: 240, 14244: 380, 14245: 520,
	14246: 210, 14247: 330, 14248: 460, 14249: 480, 14250: 760, 14251: 1000,
	14252: 200, 14253: 500, 14254: 690, 14255: 720, 14256: 1100, 14257: 1500,
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

// SeedanceRequiredCredits returns Oreate's current point price for a validated
// request. referenceDuration is zero for text/image/ordered-frame scenes and is
// the rounded-up aggregate reference-video duration otherwise.
func SeedanceRequiredCredits(modelID, resolution string, duration int, audio bool, referenceDuration int) (int, error) {
	var (
		aiType int
		err    error
	)
	if referenceDuration > 0 {
		_, aiType, _, err = SeedanceReferenceConfig(modelID, resolution, duration, referenceDuration)
	} else {
		_, aiType, err = SeedanceConfig(modelID, resolution, duration, audio)
	}
	if err != nil {
		return 0, err
	}
	credits, ok := seedanceCreditsByAIType[aiType]
	if !ok {
		return 0, fmt.Errorf("oreate: missing point cost for aiType %d", aiType)
	}
	return credits, nil
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

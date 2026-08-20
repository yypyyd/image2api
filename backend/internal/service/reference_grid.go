package service

import (
	"context"
	"strings"
)

const (
	maxReferencePixels = 100_000_000
)

// shouldApplyReferenceGrid is retained as the wire-compatible name for the
// reference_grid flag. The local Pigo face-panel transform is enabled for the
// Adobe and Oreate Seedance routes that accept image references; all other
// models receive the original reference bytes unchanged, even when the legacy
// flag is present.
func (s *V1Service) shouldApplyReferenceGrid(_ context.Context, modelID string, _ bool) bool {
	switch strings.ToLower(strings.TrimSpace(modelID)) {
	case "firefly-seedance-2", "firefly-seedance-2-fast", "seedance20", "seedance20-fast", "sd2.0", "sd2.0-fast",
		"oreate-seedance-1.5-pro", "oreate-seedance-2.0-mini", "oreate-seedance-2.0-fast", "oreate-seedance-2.0", "oreate-seedance-2.5":
		return true
	default:
		return false
	}
}

package service

import (
	"context"
	"strings"
)

const (
	maxReferencePixels = 100_000_000
)

// shouldApplyReferenceGrid is retained as the wire-compatible name for the
// reference_grid flag. It now controls the local Pigo face-swap preprocessing.
// Adobe references are processed by default; other providers must opt in.
func (s *V1Service) shouldApplyReferenceGrid(ctx context.Context, modelID string, requested bool) bool {
	if requested {
		return true
	}
	item, err := s.models.Get(ctx, strings.TrimSpace(modelID))
	return err == nil && strings.EqualFold(strings.TrimSpace(item.Provider), "adobe")
}

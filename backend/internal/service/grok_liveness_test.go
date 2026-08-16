package service

import (
	"testing"
	"time"

	"backend/internal/model"
	"gorm.io/datatypes"
)

func TestSelectGrokLivenessCandidatesLimitsAndOrdersDueAccounts(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	account := func(id string, checkedAt int64) model.TokenAccount {
		meta := datatypes.JSONMap{}
		if checkedAt > 0 {
			meta["grok_liveness_checked_at"] = checkedAt
		}
		return model.TokenAccount{
			ID:        id,
			Pool:      "grok",
			Value:     "token-" + id,
			Status:    "active",
			Meta:      meta,
			CreatedAt: now.Add(-time.Duration(len(id)) * time.Hour),
		}
	}

	items := []model.TokenAccount{
		account("old-8h", now.Add(-8*time.Hour).Unix()),
		account("recent", now.Add(-time.Hour).Unix()),
		account("old-9h", now.Add(-9*time.Hour).Unix()),
		account("old-7h", now.Add(-7*time.Hour).Unix()),
		account("old-10h", now.Add(-10*time.Hour).Unix()),
		account("never", 0),
	}
	fallbackRecent := account("cached-recent", 0)
	fallbackRecent.Meta["cached_quota_at"] = now.Add(-time.Hour).Unix()
	items = append(items, fallbackRecent)
	disabled := account("disabled", 0)
	disabled.Status = "disabled"
	items = append(items, disabled)

	got := selectGrokLivenessCandidates(items, now)
	if len(got) != grokLivenessBatchSize {
		t.Fatalf("candidate count = %d, want %d", len(got), grokLivenessBatchSize)
	}
	want := []string{"never", "old-10h", "old-9h", "old-8h"}
	for i := range want {
		if got[i].ID != want[i] {
			t.Fatalf("candidate[%d] = %q, want %q", i, got[i].ID, want[i])
		}
	}
}

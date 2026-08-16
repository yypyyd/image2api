package service

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"backend/internal/model"
	"backend/internal/provider/oreate"
	"gorm.io/datatypes"
)

func TestOreateAccountFromToken(t *testing.T) {
	item := model.TokenAccount{
		Value:        "OUID=device-cookie; ouss=session",
		AccountEmail: "user@example.com",
		Meta: datatypes.JSONMap{
			"user_agent": "test-agent",
			"ouid":       "device-meta",
			"bid":        "bid-value",
			"vip":        "2",
			"reg_ts":     float64(123456),
		},
	}
	account := oreateAccountFromToken(item)
	if account.Cookie != item.Value || account.Email != item.AccountEmail || account.UserAgent != "test-agent" {
		t.Fatalf("oreateAccountFromToken() identity = %#v", account)
	}
	if account.OUID != "device-meta" || account.BID != "bid-value" || account.VIP != "2" || account.RegTS != 123456 {
		t.Fatalf("oreateAccountFromToken() metadata = %#v", account)
	}
}

func TestOreatePoolRegistration(t *testing.T) {
	if normalizePool(" OREATE ") != "oreate" || poolToType("oreate") != "oreate" {
		t.Fatal("oreate pool is not registered")
	}
	if id := newTokenID("oreate"); !strings.HasPrefix(id, "OR") || len(id) != 12 {
		t.Fatalf("newTokenID(oreate) = %q", id)
	}
}

func TestOreateStatusForBalance(t *testing.T) {
	tests := []struct {
		name       string
		item       model.TokenAccount
		remaining  int
		known      bool
		wantStatus string
		wantChange bool
	}{
		{name: "low active is parked", item: model.TokenAccount{Status: "active"}, remaining: 59, known: true, wantStatus: "quota", wantChange: true},
		{name: "zero quota stays parked", item: model.TokenAccount{Status: "quota"}, remaining: 0, known: true, wantStatus: "quota"},
		{name: "floor reactivates quota", item: model.TokenAccount{Status: "quota"}, remaining: 60, known: true, wantStatus: "active", wantChange: true},
		{name: "healthy active is unchanged", item: model.TokenAccount{Status: "active"}, remaining: 61, known: true},
		{name: "unknown is unchanged", item: model.TokenAccount{Status: "quota"}, remaining: 0, known: false},
		{name: "disabled is respected", item: model.TokenAccount{Status: "disabled"}, remaining: 80, known: true},
		{name: "dead is respected", item: model.TokenAccount{Status: "quota", Dead: true}, remaining: 80, known: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, changed := oreateStatusForBalance(tt.item, tt.remaining, tt.known)
			if status != tt.wantStatus || changed != tt.wantChange {
				t.Fatalf("oreateStatusForBalance() = (%q, %v), want (%q, %v)", status, changed, tt.wantStatus, tt.wantChange)
			}
		})
	}
}

func TestOreateQuotaPatches(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	meta, patch := oreateQuotaPatches(model.TokenAccount{Status: "active"}, map[string]any{
		"remaining":   59,
		"total":       80,
		"reset_after": "2033-05-18T03:33:20Z",
	}, now)
	if meta["cached_quota_remaining"] != 59 || meta["cached_quota_total"] != 80 || meta["cached_quota_at"] != int(now.Unix()) {
		t.Fatalf("quota metadata = %#v", meta)
	}
	if patch["status"] != "quota" || patch["cached_quota_reset_after"] != "2033-05-18T03:33:20Z" {
		t.Fatalf("low-credit patch = %#v", patch)
	}

	_, patch = oreateQuotaPatches(model.TokenAccount{Status: "quota"}, map[string]any{"remaining": 60}, now)
	if patch["status"] != "active" || patch["fails"] != 0 {
		t.Fatalf("recovery patch = %#v", patch)
	}
	if _, ok := patch["quota_recover_at"]; !ok {
		t.Fatalf("recovery patch did not clear quota_recover_at: %#v", patch)
	}
}

func TestFilterOreateAccountsByCredits(t *testing.T) {
	account := func(id string, remaining any) model.TokenAccount {
		meta := datatypes.JSONMap{}
		if remaining != nil {
			meta["cached_quota_remaining"] = remaining
		}
		return model.TokenAccount{ID: id, Pool: "oreate", Status: "active", Value: "cookie", Meta: meta}
	}

	tests := []struct {
		name             string
		items            []model.TokenAccount
		required         int
		wantIDs          []string
		wantInsufficient bool
	}{
		{name: "80 cannot run 100 point task", items: []model.TokenAccount{account("80", 80)}, required: 100, wantInsufficient: true},
		{name: "below operating floor cannot run cheap task", items: []model.TokenAccount{account("59", 59)}, required: 38, wantInsufficient: true},
		{name: "exact balance is eligible", items: []model.TokenAccount{account("100", 100)}, required: 100, wantIDs: []string{"100"}},
		{name: "80 can run cheap task", items: []model.TokenAccount{account("80", 80)}, required: 38, wantIDs: []string{"80"}},
		{name: "unknown balance remains eligible", items: []model.TokenAccount{account("unknown", nil)}, required: 100, wantIDs: []string{"unknown"}},
		{
			name:     "mixed pool keeps only sufficient and unknown",
			items:    []model.TokenAccount{account("low", 80), account("exact", 100), account("high", 124), account("unknown", nil)},
			required: 100, wantIDs: []string{"exact", "high", "unknown"}, wantInsufficient: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, insufficient := filterOreateAccountsByCredits(tt.items, tt.required)
			if insufficient != tt.wantInsufficient {
				t.Fatalf("knownInsufficient = %v, want %v", insufficient, tt.wantInsufficient)
			}
			gotIDs := make([]string, 0, len(got))
			for _, item := range got {
				gotIDs = append(gotIDs, item.ID)
			}
			if strings.Join(gotIDs, ",") != strings.Join(tt.wantIDs, ",") {
				t.Fatalf("eligible IDs = %v, want %v", gotIDs, tt.wantIDs)
			}
		})
	}
}

func TestPinnedOreateAccountCannotBypassCreditCheck(t *testing.T) {
	for _, tc := range []struct {
		name      string
		remaining int
		required  int
	}{
		{name: "task cost", remaining: 80, required: 100},
		{name: "operating floor", remaining: 59, required: 38},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := []model.TokenAccount{{
				ID: "pinned", Pool: "oreate", Status: "disabled", Value: "cookie",
				Meta: datatypes.JSONMap{"cached_quota_remaining": tc.remaining},
			}}
			pinned := pinTestAccount(items, nil, "pinned")
			eligible, insufficient := filterOreateAccountsByCredits(pinned, tc.required)
			if len(eligible) != 0 || !insufficient {
				t.Fatalf("pinned credit filter = (%v, %v), want (empty, true)", eligible, insufficient)
			}
		})
	}
}

func TestTaskSpecificQuotaDoesNotDisableAccount(t *testing.T) {
	taskErr := fmt.Errorf("%w: %w", oreate.ErrQuotaExhausted, errAccountTaskQuota)
	if shouldMarkAccountQuota(taskErr) {
		t.Fatal("task-specific quota should not move the account to the global quota state")
	}
	if !shouldMarkAccountQuota(oreate.ErrQuotaExhausted) {
		t.Fatal("upstream quota exhaustion should still update the account state")
	}
}

func TestPrioritizeOreate80CreditAccounts(t *testing.T) {
	account := func(id string, remaining any) model.TokenAccount {
		meta := datatypes.JSONMap{}
		if remaining != nil {
			meta["cached_quota_remaining"] = remaining
		}
		return model.TokenAccount{ID: id, Meta: meta}
	}
	ids := func(items []model.TokenAccount) string {
		out := make([]string, 0, len(items))
		for _, item := range items {
			out = append(out, item.ID)
		}
		return strings.Join(out, ",")
	}

	t.Run("cheap task puts 80 point tier first", func(t *testing.T) {
		svc := &V1Service{}
		items := []model.TokenAccount{
			account("124-a", 124), account("unknown", nil), account("80-b", 80),
			account("79", 79), account("80-a", 80), account("124-b", 124),
		}
		svc.prioritizeOreate80CreditAccounts(items, 80)
		if got, want := ids(items), "80-b,80-a,124-a,unknown,79,124-b"; got != want {
			t.Fatalf("account order = %s, want %s", got, want)
		}
	})

	t.Run("expensive task keeps existing order", func(t *testing.T) {
		svc := &V1Service{}
		items := []model.TokenAccount{account("124", 124), account("80", 80)}
		svc.prioritizeOreate80CreditAccounts(items, 81)
		if got, want := ids(items), "124,80"; got != want {
			t.Fatalf("account order = %s, want %s", got, want)
		}
	})

	t.Run("cooling account stays behind healthy accounts", func(t *testing.T) {
		svc := &V1Service{}
		svc.coolDownAccount("oreate", "80-cooling")
		items := []model.TokenAccount{account("80-cooling", 80), account("124-healthy", 124)}
		svc.prioritizeOreate80CreditAccounts(items, 60)
		if got, want := ids(items), "124-healthy,80-cooling"; got != want {
			t.Fatalf("account order = %s, want %s", got, want)
		}
	})
}

func TestSelectOreateQuotaRefreshCandidates(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	account := func(id, status string, remaining any, checkedAt int64) model.TokenAccount {
		meta := datatypes.JSONMap{}
		if remaining != nil {
			meta["cached_quota_remaining"] = remaining
		}
		if checkedAt > 0 {
			meta[oreateQuotaCheckedAtMetaKey] = checkedAt
		}
		return model.TokenAccount{ID: id, Pool: "oreate", Status: status, Value: "cookie", Meta: meta}
	}

	items := []model.TokenAccount{
		account("old-b", "active", 20, now.Add(-time.Hour).Unix()),
		account("at-floor", "active", 60, 0),
		account("quota-unknown", "quota", nil, 0),
		account("recent", "quota", 10, now.Add(-time.Minute).Unix()),
		account("old-a", "active", 59, now.Add(-time.Hour).Unix()),
		account("quota-c", "quota", 0, 0),
		account("quota-b", "quota", 1, 0),
		account("quota-a", "quota", 2, 0),
		account("disabled", "disabled", 0, 0),
	}
	items = append(items, model.TokenAccount{ID: "missing-cookie", Status: "quota", Meta: datatypes.JSONMap{"cached_quota_remaining": 0}})

	got := selectOreateQuotaRefreshCandidates(items, now)
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.ID)
	}
	if want := "quota-a,quota-b,quota-c,quota-unknown"; strings.Join(ids, ",") != want {
		t.Fatalf("quota refresh candidates = %v, want %s", ids, want)
	}
}

package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"backend/internal/model"
	"backend/internal/provider/oreate"
	"gorm.io/datatypes"
)

type recordingOreateDeleter struct {
	calls int
	pool  string
	id    string
	rows  int64
	err   error
}

func (d *recordingOreateDeleter) Delete(_ context.Context, pool, id string) (int64, error) {
	d.calls++
	d.pool = pool
	d.id = id
	return d.rows, d.err
}

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

func TestShouldDeleteOreateAccount(t *testing.T) {
	tests := []struct {
		name string
		data map[string]any
		want bool
	}{
		{name: "zero", data: map[string]any{"remaining": 0}, want: true},
		{name: "below floor", data: map[string]any{"remaining": 59}, want: true},
		{name: "at floor", data: map[string]any{"remaining": 60}, want: false},
		{name: "above floor", data: map[string]any{"remaining": 61}, want: false},
		{name: "missing", data: map[string]any{}, want: false},
		{name: "untyped", data: map[string]any{"remaining": "59"}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldDeleteOreateAccount(tt.data); got != tt.want {
				t.Fatalf("shouldDeleteOreateAccount(%v) = %v, want %v", tt.data, got, tt.want)
			}
		})
	}
}

func TestDeleteOreateAccountBelowCreditFloor(t *testing.T) {
	t.Run("deletes confirmed low balance", func(t *testing.T) {
		deleter := &recordingOreateDeleter{rows: 1}
		handled, err := deleteOreateAccountBelowCreditFloor(context.Background(), deleter, "ORLOW", map[string]any{"remaining": 59})
		if err != nil || !handled {
			t.Fatalf("deleteOreateAccountBelowCreditFloor() = (%v, %v), want (true, nil)", handled, err)
		}
		if deleter.calls != 1 || deleter.pool != "oreate" || deleter.id != "ORLOW" {
			t.Fatalf("Delete calls = %d, pool = %q, id = %q", deleter.calls, deleter.pool, deleter.id)
		}
	})

	t.Run("keeps boundary balance", func(t *testing.T) {
		deleter := &recordingOreateDeleter{rows: 1}
		handled, err := deleteOreateAccountBelowCreditFloor(context.Background(), deleter, "ORKEEP", map[string]any{"remaining": 60})
		if err != nil || handled || deleter.calls != 0 {
			t.Fatalf("deleteOreateAccountBelowCreditFloor() = (%v, %v), calls = %d", handled, err, deleter.calls)
		}
	})

	t.Run("treats concurrent delete as handled", func(t *testing.T) {
		deleter := &recordingOreateDeleter{rows: 0}
		handled, err := deleteOreateAccountBelowCreditFloor(context.Background(), deleter, "ORGONE", map[string]any{"remaining": 1})
		if err != nil || !handled || deleter.calls != 1 {
			t.Fatalf("deleteOreateAccountBelowCreditFloor() = (%v, %v), calls = %d", handled, err, deleter.calls)
		}
	})

	t.Run("surfaces repository failure", func(t *testing.T) {
		wantErr := errors.New("delete failed")
		deleter := &recordingOreateDeleter{err: wantErr}
		handled, err := deleteOreateAccountBelowCreditFloor(context.Background(), deleter, "ORERROR", map[string]any{"remaining": 10})
		if !handled || !errors.Is(err, wantErr) {
			t.Fatalf("deleteOreateAccountBelowCreditFloor() = (%v, %v), want (true, %v)", handled, err, wantErr)
		}
	})
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
	items := []model.TokenAccount{{
		ID: "pinned", Pool: "oreate", Status: "disabled", Value: "cookie",
		Meta: datatypes.JSONMap{"cached_quota_remaining": 80},
	}}
	pinned := pinTestAccount(items, nil, "pinned")
	eligible, insufficient := filterOreateAccountsByCredits(pinned, 100)
	if len(eligible) != 0 || !insufficient {
		t.Fatalf("pinned credit filter = (%v, %v), want (empty, true)", eligible, insufficient)
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

func TestSelectOreateRetirementCandidates(t *testing.T) {
	now := time.Unix(2_000_000_000, 0)
	account := func(id string, remaining any, checkedAt int64) model.TokenAccount {
		meta := datatypes.JSONMap{}
		if remaining != nil {
			meta["cached_quota_remaining"] = remaining
		}
		if checkedAt > 0 {
			meta[oreateRetirementCheckedAtMetaKey] = checkedAt
		}
		return model.TokenAccount{ID: id, Pool: "oreate", Value: "cookie", Meta: meta}
	}

	items := []model.TokenAccount{
		account("old-b", 20, now.Add(-time.Hour).Unix()),
		account("at-floor", 60, 0),
		account("unknown", nil, 0),
		account("recent", 10, now.Add(-time.Minute).Unix()),
		account("old-a", 59, now.Add(-time.Hour).Unix()),
		account("never-c", 0, 0),
		account("never-b", 1, 0),
		account("never-a", 2, 0),
	}
	items = append(items, model.TokenAccount{ID: "missing-cookie", Meta: datatypes.JSONMap{"cached_quota_remaining": 0}})

	got := selectOreateRetirementCandidates(items, now)
	ids := make([]string, 0, len(got))
	for _, item := range got {
		ids = append(ids, item.ID)
	}
	if want := "never-a,never-b,never-c,old-a"; strings.Join(ids, ",") != want {
		t.Fatalf("retirement candidates = %v, want %s", ids, want)
	}
}

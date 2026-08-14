package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"backend/internal/model"
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

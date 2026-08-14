package service

import (
	"strings"
	"testing"

	"backend/internal/model"
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

package oreate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFetchProfileAndCreditsBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "OUID=device; ouss=session" {
			t.Errorf("Cookie = %q", r.Header.Get("Cookie"))
		}
		switch r.URL.Path {
		case "/oreate/user/getuserinfo":
			_, _ = w.Write([]byte(`{"status":{"code":0},"data":{"basicInfo":{"email":"user@example.com","createTime":123},"vipInfo":{"vipType":2}}}`))
		case "/oreate/account/getpointdetail":
			_, _ = w.Write([]byte(`{"status":{"code":0},"data":{"daily":{"amount":30,"endTime":200},"bonus":{"amount":94,"endTime":300},"pro":null}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := NewClient("")
	client.baseURL = server.URL
	account := Account{Cookie: "OUID=device; ouss=session"}
	profile, err := client.FetchProfile(context.Background(), account)
	if err != nil || profile.Email != "user@example.com" || profile.OUID != "device" || profile.VIP != "2" || profile.RegTS != 123 {
		t.Fatalf("FetchProfile() = %#v, %v", profile, err)
	}
	quota, err := client.FetchCreditsBalance(context.Background(), account)
	if err != nil || quota["remaining"] != 124 || quota["total"] != 124 {
		t.Fatalf("FetchCreditsBalance() = %#v, %v", quota, err)
	}
}

func TestFetchProfileAuthFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) }))
	defer server.Close()
	client := NewClient("")
	client.baseURL = server.URL
	_, err := client.FetchProfile(context.Background(), Account{Cookie: "ouss=x"})
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("FetchProfile() error = %v", err)
	}
}

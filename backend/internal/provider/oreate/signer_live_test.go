package oreate

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

type liveProbeInput struct {
	Proxy     string `json:"proxy"`
	Cookie    string `json:"cookie"`
	UserAgent string `json:"user_agent"`
}

func readLiveProbeInput(t *testing.T) liveProbeInput {
	t.Helper()
	var input liveProbeInput
	if err := json.NewDecoder(os.Stdin).Decode(&input); err != nil {
		t.Fatal("invalid live probe input")
	}
	return input
}

// TestLiveSignerProbe is deployment-only and skips during ordinary test runs.
// Credentials arrive through stdin so they never appear in process arguments,
// environment variables, test output, or repository files.
func TestLiveSignerProbe(t *testing.T) {
	if os.Getenv("OREATE_LIVE_TEST") != "1" {
		t.Skip("set OREATE_LIVE_TEST=1 and provide probe JSON on stdin")
	}
	input := readLiveProbeInput(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	signer := newChromiumSigner(func() string { return input.Proxy })
	var probeMu sync.Mutex
	var events []signerProbeEvent
	signer.probe = func(event signerProbeEvent) {
		probeMu.Lock()
		events = append(events, event)
		probeMu.Unlock()
	}
	sig, err := signer.signOnce(ctx, chromiumExecPath(), Account{Cookie: input.Cookie, UserAgent: input.UserAgent})
	if err != nil {
		probeMu.Lock()
		logSignerProbeEvents(t, events)
		probeMu.Unlock()
		t.Fatal(err)
	}
	t.Logf("raw signature length=%d bid=%t", len(sig.JT), sig.BID != "")
	if len(sig.JT) == 0 || len(sig.JT) > maxBantiJTLength {
		probeMu.Lock()
		logSignerProbeEvents(t, events)
		probeMu.Unlock()
		t.Fatalf("unexpected live signature length: %d", len(sig.JT))
	}
}

func logSignerProbeEvents(t *testing.T, events []signerProbeEvent) {
	t.Helper()
	for _, event := range events {
		if event.Kind != "diagnostic" && event.Kind != "failed" && event.Status < 400 &&
			!strings.Contains(event.Target, "banti") && !strings.HasSuffix(event.Target, "/dr") {
			continue
		}
		t.Logf(
			"network elapsed_ms=%d kind=%s target=%s status=%d error=%s",
			event.ElapsedMS, event.Kind, event.Target, event.Status, event.Error,
		)
	}
}

// TestLiveAccountProbe verifies the production account/profile route without
// creating a generation or exposing any credential or upstream response body.
func TestLiveAccountProbe(t *testing.T) {
	if os.Getenv("OREATE_LIVE_TEST") != "1" {
		t.Skip("set OREATE_LIVE_TEST=1 and provide probe JSON on stdin")
	}
	input := readLiveProbeInput(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	client := NewClient(input.Proxy)
	quota, err := client.FetchCreditsBalance(ctx, Account{Cookie: input.Cookie, UserAgent: input.UserAgent})
	if err != nil {
		t.Fatal(err)
	}
	remaining, ok := quota["remaining"].(int)
	if !ok || remaining < 0 {
		t.Fatal("live balance response did not contain a valid remaining value")
	}
	t.Logf("live remaining=%d", remaining)
}

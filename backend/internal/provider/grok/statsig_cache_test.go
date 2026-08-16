package grok

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/singleflight"
)

func resetStatsigCacheForTest() {
	statsigMu.Lock()
	statsigCache = statsigChallenge{}
	statsigMu.Unlock()
	statsigRefresh = singleflight.Group{}
}

func TestStatsigChallengeRefreshIsProcessGlobal(t *testing.T) {
	resetStatsigCacheForTest()
	t.Cleanup(resetStatsigCacheForTest)

	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	var startedOnce sync.Once
	fetch := func(context.Context) (statsigChallenge, error) {
		calls.Add(1)
		startedOnce.Do(func() { close(started) })
		<-release
		return statsigChallenge{
			header:    make([]byte, 49),
			suffix:    "shared",
			trailer:   3,
			fetchedAt: time.Now(),
		}, nil
	}

	const callers = 32
	results := make([]<-chan singleflight.Result, callers)
	for i := range results {
		results[i] = beginStatsigChallengeRefresh(fetch)
	}
	<-started
	close(release)
	for _, result := range results {
		if res := <-result; res.Err != nil {
			t.Fatalf("refresh: %v", res.Err)
		}
	}

	if got := calls.Load(); got != 1 {
		t.Fatalf("homepage fetches = %d, want 1", got)
	}
	ch, ok := loadStatsigChallenge()
	if !ok || ch.suffix != "shared" {
		t.Fatalf("shared challenge not stored: ok=%v suffix=%q", ok, ch.suffix)
	}
}

func TestBotChallengeInvalidatesFreshStatsigSnapshot(t *testing.T) {
	resetStatsigCacheForTest()
	t.Cleanup(resetStatsigCacheForTest)
	storeStatsigChallenge(statsigChallenge{
		header:    make([]byte, 49),
		suffix:    "fresh-but-rejected",
		trailer:   3,
		fetchedAt: time.Now(),
	})

	err := mapStatus("/rest/app-chat/conversations/new", 403, []byte("Request rejected by anti-bot rules."))
	if !errors.Is(err, ErrTemporaryUpstream) {
		t.Fatalf("anti-bot response = %v, want temporary upstream", err)
	}
	if _, ok := loadStatsigChallenge(); ok {
		t.Fatal("anti-bot response kept the rejected Statsig snapshot")
	}
}

func TestAuthForbiddenKeepsStatsigSnapshot(t *testing.T) {
	resetStatsigCacheForTest()
	t.Cleanup(resetStatsigCacheForTest)
	storeStatsigChallenge(statsigChallenge{
		header:    make([]byte, 49),
		suffix:    "still-valid",
		trailer:   3,
		fetchedAt: time.Now(),
	})

	err := mapStatus("/api/auth/session", 403, []byte(`{"error":"forbidden"}`))
	if !errors.Is(err, ErrAuth) {
		t.Fatalf("ordinary 403 = %v, want auth", err)
	}
	if ch, ok := loadStatsigChallenge(); !ok || ch.suffix != "still-valid" {
		t.Fatalf("ordinary 403 invalidated snapshot: ok=%v suffix=%q", ok, ch.suffix)
	}
}

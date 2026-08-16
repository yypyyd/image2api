package grok

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGlobalProxyStatsigE2E is an opt-in production probe. It verifies the
// authenticated global proxy, dynamic Statsig challenge, and submit endpoint
// without consuming an image or video generation credit.
func TestGlobalProxyStatsigE2E(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("GROK_TOK"))
	proxy := strings.TrimSpace(os.Getenv("GROK_TEST_PROXY"))
	if token == "" || proxy == "" {
		t.Skip("set GROK_TOK and GROK_TEST_PROXY to run the live proxy probe")
	}
	c := NewClient(proxy)
	client, err := c.newSubmitTLSClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c.ensureChallenge(ctx, client, token)
	body, err := c.postStream(ctx, client, token, "/rest/app-chat/conversations/new", map[string]any{
		"temporary": true,
		"modelName": "grok-3",
		"message":   "Reply with OK.",
	})
	if err != nil {
		t.Fatalf("global proxy submit: %v", err)
	}
	if strings.TrimSpace(body) == "" {
		t.Fatal("global proxy submit returned an empty stream")
	}
}

// TestGlobalProxyMediaPostE2E covers the gated first step of video generation.
// It creates only the parent media post, not the credit-consuming render job.
func TestGlobalProxyMediaPostE2E(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("GROK_TOK"))
	proxy := strings.TrimSpace(os.Getenv("GROK_TEST_PROXY"))
	if token == "" || proxy == "" {
		t.Skip("set GROK_TOK and GROK_TEST_PROXY to run the live proxy probe")
	}
	c := NewClient(proxy)
	client, err := c.newSubmitTLSClient()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c.ensureChallenge(ctx, client, token)
	postID, userID, err := c.createPost(ctx, client, token, "Proxy route verification")
	if err != nil {
		t.Fatalf("global proxy media post: %v", err)
	}
	if strings.TrimSpace(postID) == "" || strings.TrimSpace(userID) == "" {
		t.Fatal("global proxy media post returned incomplete identifiers")
	}
}

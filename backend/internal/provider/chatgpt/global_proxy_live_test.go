package chatgpt

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// TestGlobalProxyImageE2E is an opt-in production probe. It consumes one image
// allowance and stops after resolving the protected artifact URL.
func TestGlobalProxyImageE2E(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("CHATGPT_TOK"))
	proxy := strings.TrimSpace(os.Getenv("CHATGPT_TEST_PROXY"))
	if token == "" || proxy == "" {
		t.Skip("set CHATGPT_TOK and CHATGPT_TEST_PROXY to run the live proxy probe")
	}
	c := NewClient(proxy)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	_, meta, err := c.GenerateImage(ctx, token, "A small red circle centered on a plain white background.", "gpt-image-2", "1:1", "1K", nil, false)
	if err != nil {
		t.Fatalf("global proxy image: %v", err)
	}
	if strings.TrimSpace(stringValue(meta["conversation_id"])) == "" || strings.TrimSpace(stringValue(meta["image_url"])) == "" {
		t.Fatalf("global proxy image returned incomplete metadata")
	}
}

package chatgpt

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizeImageMime(t *testing.T) {
	tests := map[string]string{
		"image/jpeg":                 "image/jpeg",
		"image/jpg":                  "image/jpeg",
		"image/png":                  "image/png",
		"image/gif":                  "image/gif",
		"image/webp":                 "image/webp",
		"image/x-webp":               "image/webp",
		"IMAGE/WEBP; charset=binary": "image/webp",
		"application/octet-stream":   "",
	}
	for input, want := range tests {
		if got := normalizeImageMime(input); got != want {
			t.Errorf("normalizeImageMime(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestInspectReferenceImageWebP(t *testing.T) {
	const fixture = "UklGRiYAAABXRUJQVlA4IBoAAAAwAQCdASoBAAEAAgA0JZwAA3AA/vpopw8gAA=="
	data, err := base64.StdEncoding.DecodeString(fixture)
	if err != nil {
		t.Fatal(err)
	}
	meta, err := inspectReferenceImage(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if meta.MimeType != "image/webp" || meta.Width != 1 || meta.Height != 1 {
		t.Fatalf("WebP metadata = mime:%q size:%dx%d", meta.MimeType, meta.Width, meta.Height)
	}
	if !strings.HasSuffix(meta.FileName, ".webp") {
		t.Fatalf("WebP filename = %q", meta.FileName)
	}
}

func TestChatGPT403Classification(t *testing.T) {
	edgeErr := ensureOK(403, []byte("<html><body>edge challenge</body></html>"), "bootstrap")
	if !errors.Is(edgeErr, ErrTemporaryUpstream) || errors.Is(edgeErr, ErrAuth) {
		t.Fatalf("bootstrap HTML 403 = %v, want temporary upstream", edgeErr)
	}
	jsonErr := ensureOK(403, []byte(`{"error":{"code":"invalid_access_token"}}`), "chat_requirements_prepare")
	if !errors.Is(jsonErr, ErrAuth) || errors.Is(jsonErr, ErrTemporaryUpstream) {
		t.Fatalf("authenticated JSON 403 = %v, want auth error", jsonErr)
	}
}

func TestDedicatedChatGPTProxyOverridesSharedProxy(t *testing.T) {
	client := NewClient("http://dedicated.example:8118")
	client.SetProxy("http://shared.example:8080")
	if client.proxy != "http://dedicated.example:8118" {
		t.Fatalf("proxy = %q, want dedicated proxy", client.proxy)
	}
	fallback := NewClient("")
	fallback.SetProxy("http://shared.example:8080")
	if fallback.proxy != "http://shared.example:8080" {
		t.Fatalf("fallback proxy = %q, want shared proxy", fallback.proxy)
	}
}

func TestAssistantTextFromEvent(t *testing.T) {
	raw := []byte(`{"message":{"author":{"role":"assistant"},"content":{"content_type":"text","parts":["hello","world"]}}}`)
	if got := assistantTextFromEvent(raw); got != "hello\nworld" {
		t.Fatalf("assistantTextFromEvent() = %q", got)
	}
	if got := textModelSlug("chatgpt-auto"); got != "auto" {
		t.Fatalf("textModelSlug(chatgpt-auto) = %q", got)
	}
	if got := textModelSlug("gpt-5-5-mini"); got != "auto" {
		t.Fatalf("textModelSlug(gpt-5-5-mini) = %q", got)
	}
	start := []byte(`{"v":{"message":{"author":{"role":"assistant"},"content":{"content_type":"text","parts":[""]}}}}`)
	text, active, updated := applyAssistantEvent(start, "", false)
	if !active || !updated || text != "" {
		t.Fatalf("assistant start = %q active:%v updated:%v", text, active, updated)
	}
	patch := []byte(`{"o":"patch","v":[{"p":"/message/content/parts/0","o":"append","v":"hello"}]}`)
	text, active, updated = applyAssistantEvent(patch, text, active)
	if !active || !updated || text != "hello" {
		t.Fatalf("assistant patch = %q active:%v updated:%v", text, active, updated)
	}
	conversation := map[string]any{"mapping": map[string]any{
		"a": map[string]any{"message": map[string]any{
			"author": map[string]any{"role": "assistant"}, "create_time": 1.0,
			"content": map[string]any{"content_type": "text", "parts": []any{"older"}},
		}},
		"b": map[string]any{"message": map[string]any{
			"author": map[string]any{"role": "assistant"}, "create_time": 2.0,
			"content": map[string]any{"content_type": "text", "parts": []any{"newer"}},
		}},
	}}
	if got := latestAssistantText(conversation); got != "newer" {
		t.Fatalf("latestAssistantText() = %q", got)
	}
}

func TestGenerateTextProbe(t *testing.T) {
	token := strings.TrimSpace(os.Getenv("CHATGPT_TEXT_TOKEN"))
	if token == "" {
		t.Skip("no CHATGPT_TEXT_TOKEN")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	text, err := NewClient(os.Getenv("CHATGPT_TEXT_PROXY")).GenerateText(ctx, token, "Reply with exactly: text-proxy-ok", "gpt-5-5-mini")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.ToLower(text), "text-proxy-ok") {
		t.Fatalf("unexpected response: %q", text)
	}
}

func TestContainsAsyncMarker(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "legacy async flag", text: `{"image_gen_async":true}`, want: true},
		{name: "legacy task id", text: `{"image_gen_task_id":"task_123"}`, want: true},
		{name: "new delegated tool signal", text: `{"content":{"content_type":"code","text":"{\"skipped_mainline\":true}"}}`, want: true},
		{name: "new unescaped tool signal", text: `{"skipped_mainline":true}`, want: true},
		{name: "delegated tool signal with whitespace", text: `{"content":{"content_type":"code","text":"{\"skipped_mainline\": true}"}}`, want: true},
		{name: "unescaped signal with whitespace", text: "{\n  \"skipped_mainline\" : true\n}", want: true},
		{name: "multiply escaped delegated signal", text: `{"text":"{\\\"skipped_mainline\\\": true}"}`, want: true},
		{name: "delegation explicitly false", text: `{"skipped_mainline": false}`, want: false},
		{name: "marker word in prompt", text: `{"prompt":"explain skipped_mainline"}`, want: false},
		{name: "ordinary code", text: `{"content":{"content_type":"code","text":"print(1)"}}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsAsyncMarker(tt.text); got != tt.want {
				t.Fatalf("containsAsyncMarker() = %v, want %v", got, tt.want)
			}
		})
	}
}

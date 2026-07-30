package grok

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
)

func TestIsBuildTextModel(t *testing.T) {
	tests := map[string]bool{
		"grok-4.5":           true,
		"grok-3-mini":        true,
		"grok-chat-fast":     false,
		"grok-chat-expert":   false,
		"grok-imagine-image": false,
		"":                   false,
	}
	for model, want := range tests {
		if got := IsBuildTextModel(model); got != want {
			t.Fatalf("IsBuildTextModel(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestBuildResponseText(t *testing.T) {
	body := []byte(`{"id":"resp_1","output":[{"type":"message","content":[{"type":"output_text","text":"hello "},{"type":"output_text","text":"world"}]}]}`)
	text, responseErr := buildResponseText(body)
	if responseErr != "" || text != "hello world" {
		t.Fatalf("buildResponseText = %q, %q", text, responseErr)
	}
}

func TestExchangeBuildTokenKeepsRefreshToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil || r.Form.Get("grant_type") != "refresh_token" {
			t.Fatalf("unexpected form: %v (%v)", r.Form, err)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "new-access", "expires_in": 3600})
	}))
	defer server.Close()

	credential, code, err := exchangeBuildToken(context.Background(), server.Client(), server.URL, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {"old-refresh"},
	}, "old-refresh")
	if err != nil || code != "" || credential.AccessToken != "new-access" || credential.RefreshToken != "old-refresh" {
		t.Fatalf("credential=%#v code=%q err=%v", credential, code, err)
	}
}

func TestGenerateBuildTextRequest(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/responses" {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer access-token" || r.Header.Get("X-XAI-Token-Auth") != "xai-grok-cli" {
			t.Fatalf("missing build auth headers: %#v", r.Header)
		}
		if r.Header.Get("x-grok-model-override") != "grok-4.5" {
			t.Fatalf("model override = %q", r.Header.Get("x-grok-model-override"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"model":"grok-4.5"`) || !strings.Contains(string(body), `"input":"hello"`) {
			t.Fatalf("body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"output":[{"type":"message","content":[{"type":"output_text","text":"hi"}]}]}`))
	}))
	defer server.Close()

	text, err := generateBuildText(context.Background(), server.Client(), server.URL+"/v1/responses", "access-token", "hello", "grok-4.5")
	if err != nil || text != "hi" {
		t.Fatalf("text=%q err=%v", text, err)
	}
}

func TestLiveSSOToBuildAndGrok45(t *testing.T) {
	if os.Getenv("GROK_BUILD_LIVE") == "" {
		t.Skip("set GROK_BUILD_LIVE=1 and GROK_TOK to run the live Build OAuth test")
	}
	token := strings.TrimSpace(os.Getenv("GROK_TOK"))
	if token == "" {
		t.Fatal("GROK_TOK is required")
	}
	client := NewClient("")
	credential, err := client.ConvertSSOToBuild(t.Context(), token)
	if err != nil {
		t.Fatalf("ConvertSSOToBuild: %v", err)
	}
	text, err := client.GenerateBuildText(t.Context(), credential.AccessToken, "Reply with exactly: OK", "grok-4.5")
	if err != nil {
		t.Fatalf("GenerateBuildText: %v", err)
	}
	if !strings.Contains(strings.ToUpper(text), "OK") {
		t.Fatalf("unexpected response: %q", text)
	}
}

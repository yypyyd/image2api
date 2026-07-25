package custom

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChatCompletionsNonStreamRewritesModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer upstream-key" {
			t.Fatalf("authorization was not forwarded")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "upstream-model" || body["temperature"] != float64(0.2) {
			t.Fatalf("unexpected body: %#v", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"chatcmpl-1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`)
	}))
	defer server.Close()

	resp, err := NewClient().ChatCompletions(context.Background(), server.URL, "upstream-key", "upstream-model",
		[]byte(`{"model":"public-alias","messages":[{"role":"user","content":"hi"}],"temperature":0.2}`), false)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(raw), `"content":"ok"`) || resp.Stream {
		t.Fatalf("unexpected response: stream=%v body=%s", resp.Stream, raw)
	}
}

func TestChatCompletionsRejectsHTTP200BusinessError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"error":{"message":"temporary"}}`)
	}))
	defer server.Close()

	_, err := NewClient().ChatCompletions(context.Background(), server.URL, "key", "m",
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`), false)
	if !errors.Is(err, ErrTemporaryUpstream) {
		t.Fatalf("error = %v, want ErrTemporaryUpstream", err)
	}
}

func TestChatCompletionsStreamsSSE(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer server.Close()

	resp, err := NewClient().ChatCompletions(context.Background(), server.URL, "key", "m",
		[]byte(`{"model":"m","messages":[{"role":"user","content":"hi"}],"stream":true}`), true)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if !resp.Stream || !strings.Contains(string(raw), "data: [DONE]") {
		t.Fatalf("unexpected stream: %s", raw)
	}
}

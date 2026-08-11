package service

import (
	"errors"
	"io"
	"strings"
	"testing"

	"backend/internal/config"
	"backend/internal/model"
	"gorm.io/datatypes"
)

func TestNormalizeImageResponseFormat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default is url", want: "url"},
		{name: "base64", input: "b64_json", want: "b64_json"},
		{name: "url", input: " URL ", want: "url"},
		{name: "invalid", input: "bytes", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeImageResponseFormat(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedParams) {
					t.Fatalf("normalizeImageResponseFormat() error = %v, want ErrUnsupportedParams", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeImageResponseFormat() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeImageResponseFormat() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestPublicImageContentURL(t *testing.T) {
	if got := publicImageContentURL("https://HOST:9445/", "evt-OPAQUE"); got != "https://HOST:9445/v1/images/evt-OPAQUE/content" {
		t.Fatalf("publicImageContentURL() = %q", got)
	}
	if got := publicImageContentURL("", "evt-OPAQUE"); got != "/v1/images/evt-OPAQUE/content" {
		t.Fatalf("relative publicImageContentURL() = %q", got)
	}
}

func TestOutputBaseURLPrefersConfiguredPublicOrigin(t *testing.T) {
	svc := &V1Service{cfg: &config.Config{PublicBaseURL: "https://api.example.test/"}}
	if got := svc.outputBaseURL("http://internal:2000"); got != "https://api.example.test" {
		t.Fatalf("outputBaseURL() = %q", got)
	}
}

func TestImageTaskURLUsesPublicOriginAndEscapesRequestID(t *testing.T) {
	svc := &V1Service{cfg: &config.Config{PublicBaseURL: "https://api.example.test/"}}
	if got := svc.imageTaskURL("request id/1", "http://internal:2000"); got != "https://api.example.test/v1/images/tasks?request_id=request+id%2F1" {
		t.Fatalf("imageTaskURL() = %q", got)
	}
}

func TestAsyncImageJobResponseLifecycle(t *testing.T) {
	job := newAsyncImageJob()
	response, pending := asyncImageJobResponse(job, "req-1", "/poll")
	if !pending || response["status"] != "queued" {
		t.Fatalf("pending response = %#v, pending=%v", response, pending)
	}

	job.complete(map[string]any{"data": []map[string]any{{"url": "https://example.test/image.png"}}}, nil)
	response, pending = asyncImageJobResponse(job, "req-1", "/poll")
	if pending || response["status"] != "completed" || response["request_id"] != "req-1" {
		t.Fatalf("completed response = %#v, pending=%v", response, pending)
	}
}

func TestAsyncImageJobResponseFailure(t *testing.T) {
	job := newAsyncImageJob()
	job.complete(nil, errors.New("render failed"))
	response, pending := asyncImageJobResponse(job, "req-2", "/poll")
	if pending || response["status"] != "failed" || response["error"] != "render failed" {
		t.Fatalf("failed response = %#v, pending=%v", response, pending)
	}
}

func TestModelPriceTextPerRequest(t *testing.T) {
	item := &model.ModelConfig{
		Prices:      datatypes.JSONMap{"request": 2.5},
		PricesAgent: datatypes.JSONMap{"request": 1.5},
	}
	if got, ok := modelPrice(item, "text", "", "", false); !ok || got != 2.5 {
		t.Fatalf("normal text price = %v, %v", got, ok)
	}
	if got, ok := modelPrice(item, "text", "", "", true); !ok || got != 1.5 {
		t.Fatalf("agent text price = %v, %v", got, ok)
	}
}

func TestChatAccountingBodyRequiresDoneForStream(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantOK  bool
		wantBad bool
	}{
		{name: "done", body: "data: {}\n\ndata: [DONE]\n\n", wantOK: true},
		{name: "compact done", body: "data: {}\n\ndata:[DONE]\n\n", wantOK: true},
		{name: "truncated", body: "data: {}\n\n", wantBad: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var ok, bad bool
			body := &chatAccountingBody{
				inner:  io.NopCloser(strings.NewReader(tt.body)),
				stream: true,
				finish: func(success bool, _ string, upstreamFailure bool) { ok, bad = success, upstreamFailure },
			}
			_, _ = io.ReadAll(body)
			_ = body.Close()
			if ok != tt.wantOK || bad != tt.wantBad {
				t.Fatalf("finish = ok:%v bad:%v, want ok:%v bad:%v", ok, bad, tt.wantOK, tt.wantBad)
			}
		})
	}
}

func TestOpenAITextResponseAndTranscript(t *testing.T) {
	header, body := openAITextResponse("chatgpt-auto", "hello", true)
	defer body.Close()
	raw, _ := io.ReadAll(body)
	if header.Get("Content-Type") != "text/event-stream" || !strings.Contains(string(raw), `"object":"chat.completion.chunk"`) || !strings.Contains(string(raw), "data: [DONE]") {
		t.Fatalf("unexpected SSE response: header=%v body=%s", header, raw)
	}
	prompt := chatPrompt([]any{
		map[string]any{"role": "system", "content": "be brief"},
		map[string]any{"role": "user", "content": "hi"},
	})
	if prompt != "SYSTEM: be brief\nUSER: hi" {
		t.Fatalf("chatPrompt() = %q", prompt)
	}
}

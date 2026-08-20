package oreate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubSigner struct {
	sig Signature
	err error
}

func (s stubSigner) Sign(context.Context, Account) (Signature, error) { return s.sig, s.err }

func TestParseCreateChat(t *testing.T) {
	id, err := parseCreateChat([]byte(`{"status":{"code":0,"msg":"success"},"data":{"chatId":" chat-1 "}}`))
	if err != nil || id != "chat-1" {
		t.Fatalf("parseCreateChat() = %q, %v", id, err)
	}
	for _, body := range []string{`not-json`, `{"status":{"code":0},"data":{}}`} {
		if _, err := parseCreateChat([]byte(body)); !errors.Is(err, ErrTemporaryUpstream) {
			t.Fatalf("parseCreateChat(%q) error = %v", body, err)
		}
	}
	if _, err := parseCreateChat([]byte(`{"status":{"code":200017,"msg":"point exceed"}}`)); !errors.Is(err, ErrQuotaExhausted) {
		t.Fatalf("quota error = %v", err)
	}
}

func TestParseVideoSSE(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{"direct", "data: {\"event\":\"start\"}\n\ndata: {\"event\":\"end\",\"data\":{\"videoUrl\":\"https://cdn.example/out.mp4\"}}\n", "https://cdn.example/out.mp4"},
		{"nested-json", "data: {\"event\":\"data\",\"data\":\"{\\\"result\\\":{\\\"url\\\":\\\"https:\\\\/\\\\/cdn.example\\\\/nested.mp4?x=1\\\"}}\"}\n", "https://cdn.example/nested.mp4?x=1"},
		{"markdown", "data: {\"event\":\"end\",\"content\":\"[video](https://cdn.example/md.mp4)\"}\n", "https://cdn.example/md.mp4"},
		{"html", "data: <video src=\"https://cdn.example/html.mp4\"></video>\n", "https://cdn.example/html.mp4"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseVideoSSE(strings.NewReader(tt.body))
			if err != nil || got.VideoURL != tt.want {
				t.Fatalf("parseVideoSSE() = %q, %v; want %q", got.VideoURL, err, tt.want)
			}
		})
	}
}

func TestParseVideoSSEError(t *testing.T) {
	for _, tt := range []struct {
		code int
		msg  string
		want error
	}{
		{212361, "spam user", ErrRiskControl},
		{200017, "point exceed", ErrQuotaExhausted},
		{401, "unauthenticated", ErrAuth},
		{500, "busy", ErrTemporaryUpstream},
		{0, "content policy", ErrContentRejected},
	} {
		body := fmt.Sprintf("data: {\"event\":\"error\",\"data\":{\"code\":%d,\"msg\":%q}}\n", tt.code, tt.msg)
		_, err := parseVideoSSE(strings.NewReader(body))
		if !errors.Is(err, tt.want) {
			t.Errorf("code %d error = %v, want %v", tt.code, err, tt.want)
		}
		if tt.code == 212361 && !errors.Is(err, ErrSpamUser) {
			t.Errorf("code %d error = %v, want ErrSpamUser", tt.code, err)
		}
	}
}

func TestParseVideoSSEIncompleteStream(t *testing.T) {
	body := "data: {\"event\":\"start\",\"logId\":\"2098276034\"}\n\ndata: {\"event\":\"generating\",\"logId\":\"2098276034\"}\n"
	got, err := parseVideoSSE(strings.NewReader(body))
	if !errors.Is(err, errStreamIncomplete) || !errors.Is(err, ErrTemporaryUpstream) {
		t.Fatalf("parseVideoSSE() error = %v", err)
	}
	if !got.Started || got.LogID != "2098276034" || got.VideoURL != "" {
		t.Fatalf("parseVideoSSE() = %#v", got)
	}
}

// A stream that drops after the job was accepted must still hand back the
// rendered file: the submit already spent the account's credits.
func TestGenerateVideoRecoversDroppedStreamByLogID(t *testing.T) {
	cdnHits := 0
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/777.mp4" {
			http.NotFound(w, r)
			return
		}
		cdnHits++
		_, _ = w.Write([]byte("recovered-mp4"))
	}))
	defer cdn.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oreate/create/chat" {
			_, _ = io.WriteString(w, `{"status":{"code":0},"data":{"chatId":"chat-3"}}`)
			return
		}
		_, _ = io.WriteString(w, "data: {\"event\":\"start\",\"logId\":\"777\"}\n")
	}))
	defer server.Close()
	client := NewClient("")
	client.baseURL = server.URL
	client.cdnBaseURL = cdn.URL
	client.SetSigner(stubSigner{sig: Signature{JT: "signed"}})
	data, meta, err := client.GenerateVideo(context.Background(), Account{Cookie: "ouss=x"}, VideoOptions{
		ModelID: "seedance-2.0-mini", Prompt: "hello", Ratio: "16:9", Resolution: "480p",
		Duration: 5, DownloadResult: true,
	})
	if err != nil || string(data) != "recovered-mp4" || cdnHits == 0 {
		t.Fatalf("GenerateVideo() = %q, %v", data, err)
	}
	if meta["video_url"] != cdn.URL+"/777.mp4" || meta["log_id"] != "777" {
		t.Fatalf("meta = %#v", meta)
	}
}

// Without a logId there is nothing to recover, and an upstream verdict such as
// a spam-user rejection must never be retried as a recovery.
func TestGenerateVideoSkipsRecoveryWithoutRecoverableJob(t *testing.T) {
	streams := map[string]string{
		"no-log-id": "data: {\"event\":\"start\"}\n",
		"spam-user": "data: {\"event\":\"start\",\"logId\":\"777\"}\n\ndata: {\"event\":\"error\",\"logId\":\"777\",\"data\":{\"code\":212361,\"msg\":\"spam user\"}}\n",
	}
	for name, stream := range streams {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/oreate/create/chat" {
					_, _ = io.WriteString(w, `{"status":{"code":0},"data":{"chatId":"chat-4"}}`)
					return
				}
				_, _ = io.WriteString(w, stream)
			}))
			defer server.Close()
			cdnCalls := 0
			cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				cdnCalls++
				_, _ = w.Write([]byte("unexpected"))
			}))
			defer cdn.Close()
			client := NewClient("")
			client.baseURL = server.URL
			client.cdnBaseURL = cdn.URL
			client.SetSigner(stubSigner{sig: Signature{JT: "signed"}})
			_, _, err := client.GenerateVideo(context.Background(), Account{Cookie: "ouss=x"}, VideoOptions{
				ModelID: "seedance-2.0-mini", Prompt: "hello", Ratio: "16:9", Resolution: "480p", Duration: 5,
			})
			if err == nil || cdnCalls != 0 {
				t.Fatalf("GenerateVideo() error = %v, cdn calls = %d", err, cdnCalls)
			}
			if name == "spam-user" && !errors.Is(err, ErrSpamUser) {
				t.Fatalf("GenerateVideo() error = %v, want ErrSpamUser", err)
			}
		})
	}
}

func TestGenerateVideoRequestAndDownload(t *testing.T) {
	video := []byte("test-mp4")
	var gotRequest videoRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oreate/create/chat":
			assertOreateHeaders(t, r)
			_, _ = io.WriteString(w, `{"status":{"code":0},"data":{"chatId":"chat-1"}}`)
		case "/oreate/sse/stream":
			assertOreateHeaders(t, r)
			if r.Header.Get("Accept") != "text/event-stream" {
				t.Errorf("Accept = %q", r.Header.Get("Accept"))
			}
			if err := json.NewDecoder(r.Body).Decode(&gotRequest); err != nil {
				t.Errorf("decode request: %v", err)
			}
			_, _ = fmt.Fprintf(w, "data: {\\\"event\\\":\\\"end\\\",\\\"data\\\":{\\\"url\\\":%q}}\\n", serverURL(r)+"/video.mp4")
		case "/video.mp4":
			_, _ = w.Write(video)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient("")
	client.baseURL = server.URL
	client.SetSigner(stubSigner{sig: Signature{JT: "signed", BID: "browser-bid"}})
	account := Account{Cookie: "OUID=device-1; ouss=session", UserAgent: "test-agent", Email: "user@example.com", VIP: "0", RegTS: 123}
	data, meta, err := client.GenerateVideo(context.Background(), account, VideoOptions{
		ModelID: "seedance-2.0-mini", Prompt: "hello", Ratio: "16:9", Resolution: "480p",
		Duration: 5, Audio: false, DownloadResult: true,
	})
	if err != nil {
		t.Fatalf("GenerateVideo() error = %v", err)
	}
	if string(data) != string(video) || meta["video_url"] == "" {
		t.Fatalf("GenerateVideo() data/meta = %q, %#v", data, meta)
	}
	if gotRequest.ChatID != "chat-1" || gotRequest.FocusID != "chat-1" || gotRequest.VideoConfig.AIType != 14198 || gotRequest.VideoConfig.Scene != "text_or_image" {
		t.Fatalf("request identity/config = %#v", gotRequest)
	}
	if gotRequest.JT != "signed" || gotRequest.Extra.BID != "browser-bid" || gotRequest.Extra.DeviceID != "device-1" || len(gotRequest.Messages) != 1 || gotRequest.Messages[0].Content != "hello" {
		t.Fatalf("request auth/content = %#v", gotRequest)
	}
}

// The stream request must carry the tracking cookies the signer browser minted
// alongside the stored auth cookies; stored values win on name conflicts.
func TestGenerateVideoMergesSignerCookies(t *testing.T) {
	var streamCookie string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oreate/create/chat" {
			_, _ = io.WriteString(w, `{"status":{"code":0},"data":{"chatId":"chat-1"}}`)
			return
		}
		streamCookie = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, "data: {\"event\":\"end\",\"data\":{\"url\":\"https://example.com/video.mp4\"}}\n")
	}))
	defer server.Close()
	client := NewClient("")
	client.baseURL = server.URL
	client.SetSigner(stubSigner{sig: Signature{JT: "signed", Cookie: "_ga=GA1; __bid_n=fresh; OUID=browser-ouid"}})
	_, _, err := client.GenerateVideo(context.Background(), Account{Cookie: "OUID=device-1; ouss=session"}, VideoOptions{
		ModelID: "seedance-2.0-mini", Prompt: "hello", Ratio: "16:9", Resolution: "480p", Duration: 5,
	})
	if err != nil {
		t.Fatalf("GenerateVideo() error = %v", err)
	}
	if streamCookie != "OUID=device-1; ouss=session; _ga=GA1; __bid_n=fresh" {
		t.Fatalf("stream cookie = %q", streamCookie)
	}
}

func TestGenerateVideoURLOnly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/oreate/create/chat" {
			_, _ = io.WriteString(w, `{"status":{"code":0},"data":{"chatId":"chat-2"}}`)
			return
		}
		_, _ = io.WriteString(w, "data: {\"event\":\"end\",\"url\":\"https://cdn.example/url-only.mp4\"}\n")
	}))
	defer server.Close()
	client := NewClient("")
	client.baseURL = server.URL
	client.SetSigner(stubSigner{sig: Signature{JT: "signed"}})
	data, meta, err := client.GenerateVideo(context.Background(), Account{Cookie: "ouss=x"}, VideoOptions{
		ModelID: "seedance-2.0-fast", Prompt: "hello", Ratio: "1:1", Resolution: "720",
		Duration: 10, Audio: true, DownloadResult: false,
	})
	if err != nil || data != nil || meta["video_url"] != "https://cdn.example/url-only.mp4" {
		t.Fatalf("GenerateVideo() = %q, %#v, %v", data, meta, err)
	}
}

func TestGenerateVideoReferenceScenes(t *testing.T) {
	tests := []struct {
		name        string
		options     VideoOptions
		wantScene   string
		wantAIType  int
		wantImages  int
		wantVideos  int
		wantRefBand string
	}{
		{
			name: "seedance-2.5-image-reference",
			options: VideoOptions{
				ModelID: "seedance-2.5", Prompt: "animate", Ratio: "16:9", Resolution: "480p", Duration: 20, Audio: true,
				ReferenceImages: []MediaReference{{Data: []byte("png-data"), ContentType: "image/png"}},
			},
			wantScene: "reference", wantAIType: 14227, wantImages: 1,
		},
		{
			name: "seedance-1.5-two-frames",
			options: VideoOptions{
				ModelID: "seedance-1.5-pro", Prompt: "transition", Ratio: "1:1", Resolution: "720p", Duration: 5,
				ReferenceImages: []MediaReference{
					{Data: []byte("first"), ContentType: "image/jpeg"},
					{Data: []byte("last"), ContentType: "image/jpeg"},
				},
			},
			wantScene: "frame_based", wantAIType: 14003, wantImages: 2,
		},
		{
			name: "seedance-2.5-video-reference",
			options: VideoOptions{
				ModelID: "seedance-2.5", Prompt: "continue motion", Ratio: "9:16", Resolution: "720p", Duration: 10, Audio: true,
				ReferenceVideos: []MediaReference{{Data: []byte("mp4-data"), ContentType: "video/mp4", DurationSec: 8.25}},
			},
			wantScene: "reference", wantAIType: 14244, wantVideos: 1, wantRefBand: "6-10",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, request, uploadCount, closeServer := referenceVideoTestClient(t)
			defer closeServer()
			_, _, err := client.GenerateVideo(context.Background(), Account{Cookie: "ouss=session"}, tt.options)
			if err != nil {
				t.Fatalf("GenerateVideo() error = %v", err)
			}
			got := request()
			if got.VideoConfig.Scene != tt.wantScene || got.VideoConfig.AIType != tt.wantAIType {
				t.Fatalf("scene/aiType = %q/%d, want %q/%d", got.VideoConfig.Scene, got.VideoConfig.AIType, tt.wantScene, tt.wantAIType)
			}
			if len(got.Messages) != 1 || len(got.Messages[0].Attachments) != tt.wantImages+tt.wantVideos || uploadCount() != tt.wantImages+tt.wantVideos {
				t.Fatalf("attachment/upload counts = %d/%d", len(got.Messages[0].Attachments), uploadCount())
			}
			switch tt.wantScene {
			case "reference":
				if got.VideoConfig.Reference == nil || len(got.VideoConfig.Reference.ReferenceImages) != tt.wantImages || len(got.VideoConfig.Reference.ReferenceVideos) != tt.wantVideos || got.VideoConfig.Reference.RefDuration != tt.wantRefBand {
					t.Fatalf("reference config = %#v", got.VideoConfig.Reference)
				}
			case "frame_based":
				if got.VideoConfig.FrameBased == nil || got.VideoConfig.FrameBased.FirstFrame == "" || got.VideoConfig.FrameBased.LastFrame == "" {
					t.Fatalf("frame config = %#v", got.VideoConfig.FrameBased)
				}
			}
		})
	}
}

func TestGenerateVideoRejectsReferenceVideoForSeedance15(t *testing.T) {
	client := NewClient("")
	client.SetSigner(stubSigner{sig: Signature{JT: "signed"}})
	_, _, err := client.GenerateVideo(context.Background(), Account{Cookie: "ouss=session"}, VideoOptions{
		ModelID: "seedance-1.5-pro", Prompt: "hello", Ratio: "16:9", Resolution: "480p", Duration: 5,
		ReferenceVideos: []MediaReference{{Data: []byte("video"), ContentType: "video/mp4", DurationSec: 5}},
	})
	if err == nil || !strings.Contains(err.Error(), "does not support reference videos") {
		t.Fatalf("GenerateVideo() error = %v", err)
	}
}

func referenceVideoTestClient(t *testing.T) (*Client, func() videoRequest, func() int, func()) {
	t.Helper()
	var got videoRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case uploadTokenPath:
			var tokenRequest uploadTokenRequest
			if err := json.NewDecoder(r.Body).Decode(&tokenRequest); err != nil {
				t.Errorf("decode upload token request: %v", err)
			}
			keys := map[string]uploadCredential{}
			for _, file := range tokenRequest.Files {
				filename := file.Filename + "." + file.FileExt
				keys[filename] = uploadCredential{Bucket: "ot-pt", ObjectPath: "uploaded/" + filename, SessionKey: "test-token"}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"status": map[string]any{"code": 0}, "data": map[string]any{"KeyList": keys}})
		case "/oreate/create/chat":
			_, _ = io.WriteString(w, `{"status":{"code":0},"data":{"chatId":"chat-ref"}}`)
		case "/oreate/sse/stream":
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Errorf("decode generation request: %v", err)
			}
			_, _ = io.WriteString(w, "data: {\"event\":\"end\",\"url\":\"https://cdn.example/reference.mp4\"}\n")
		default:
			http.NotFound(w, r)
		}
	}))
	uploads := 0
	client := NewClient("")
	client.baseURL = server.URL
	client.SetSigner(stubSigner{sig: Signature{JT: "signed"}})
	client.directClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodPost:
			return testHTTPResponse(http.StatusOK, http.Header{"Location": {"https://storage.googleapis.com/resumable/session"}}, nil), nil
		case http.MethodPut:
			uploads++
			return testHTTPResponse(http.StatusOK, nil, nil), nil
		default:
			return nil, fmt.Errorf("unexpected direct request %s", req.Method)
		}
	})}
	return client, func() videoRequest { return got }, func() int { return uploads }, server.Close
}

func assertOreateHeaders(t *testing.T, r *http.Request) {
	t.Helper()
	for key, want := range map[string]string{"Cookie": "OUID=device-1; ouss=session", "User-Agent": "test-agent", "Client-Type": "pc", "Locale": "zh-CN"} {
		if got := r.Header.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func serverURL(r *http.Request) string { return "http://" + r.Host }

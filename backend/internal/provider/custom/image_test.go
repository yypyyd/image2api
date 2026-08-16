package custom

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGenerateImageForwardsQualityInMultipartEdit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/images/edits" {
			t.Errorf("path = %q, want /v1/images/edits", r.URL.Path)
			http.Error(w, "unexpected path", http.StatusNotFound)
			return
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart form: %v", err)
			http.Error(w, "invalid multipart", http.StatusBadRequest)
			return
		}
		if got := r.FormValue("size"); got != "3072x4096" {
			t.Errorf("size = %q, want 3072x4096", got)
		}
		if got := r.FormValue("quality"); got != "high" {
			t.Errorf("quality = %q, want high", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"url":"https://example.test/image.png"}]}`))
	}))
	defer server.Close()

	_, imageURL, err := NewClient().GenerateImage(
		context.Background(), server.URL, "test-key", "test-model", "test prompt",
		"3072x4096", "high", [][]byte{[]byte("reference")}, false,
	)
	if err != nil {
		t.Fatalf("GenerateImage() error = %v", err)
	}
	if imageURL != "https://example.test/image.png" {
		t.Fatalf("image URL = %q", imageURL)
	}
}

package handler

import (
	"bytes"
	"encoding/base64"
	"mime/multipart"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestDurationRange(t *testing.T) {
	want := []string{"3s", "4s", "5s", "6s"}
	if got := durationRange(3, 6); !reflect.DeepEqual(got, want) {
		t.Fatalf("durationRange(3, 6) = %#v, want %#v", got, want)
	}
	if got := durationRange(5, 4); len(got) != 0 {
		t.Fatalf("invalid duration range = %#v, want empty", got)
	}
}

func TestBindGenerateMultipartMedia(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("model", "firefly-seedance-2")
	_ = w.WriteField("prompt", "test")
	_ = w.WriteField("generate_audio", "true")
	video, err := w.CreateFormFile("reference_videos", "source.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = video.Write([]byte("video"))
	audio, err := w.CreateFormFile("reference_audios", "source.mp3")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = audio.Write([]byte("audio"))
	_ = w.Close()

	req := httptest.NewRequest("POST", "/admin/api/generate", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req
	got, err := bindGenerateRequest(c)
	if err != nil {
		t.Fatal(err)
	}
	if !got.GenerateAudio || len(got.ReferenceVideos) != 1 || len(got.ReferenceAudios) != 1 {
		t.Fatalf("multipart media = %#v", got)
	}
	if got.ReferenceVideos[0].ContentType != "video/mp4" || got.ReferenceAudios[0].ContentType != "audio/mpeg" {
		t.Fatalf("multipart MIME = %q/%q", got.ReferenceVideos[0].ContentType, got.ReferenceAudios[0].ContentType)
	}
}

func TestDecodeJSONMedia(t *testing.T) {
	raw := base64.StdEncoding.EncodeToString([]byte("video"))
	refs, err := decodeJSONMedia([]string{raw}, "video/mp4")
	if err != nil || len(refs) != 1 {
		t.Fatalf("decodeJSONMedia() = %#v, %v", refs, err)
	}
	if refs[0].ContentType != "video/mp4" || string(refs[0].Data) != "video" {
		t.Fatalf("decoded media = %#v", refs[0])
	}
	refs, err = decodeJSONMedia([]string{"data:audio/wav;base64," + base64.StdEncoding.EncodeToString([]byte("audio"))}, "audio/mpeg")
	if err != nil || refs[0].ContentType != "audio/wav" {
		t.Fatalf("data URI decode = %#v, %v", refs, err)
	}
}

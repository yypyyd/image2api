package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

func TestSynchronousImageResponseFlushesKeepaliveBeforeSuccess(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	results := make(chan imageSyncResult, 1)

	go func() {
		time.Sleep(25 * time.Millisecond)
		results <- imageSyncResult{response: map[string]any{
			"created": int64(123),
			"data":    []map[string]any{{"url": "https://example.test/image.png"}},
		}}
	}()

	h := &V1Handler{}
	h.writeSynchronousImageResponse(c, results, 5*time.Millisecond, 5*time.Millisecond)

	if !recorder.Flushed {
		t.Fatal("expected the response writer to flush a keepalive")
	}
	var body map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("response with leading keepalive whitespace is not valid JSON: %v", err)
	}
	if body["created"] != float64(123) {
		t.Fatalf("created = %#v, want 123", body["created"])
	}
	if recorder.Header().Get("X-Accel-Buffering") != "no" {
		t.Fatalf("X-Accel-Buffering = %q, want no", recorder.Header().Get("X-Accel-Buffering"))
	}
}

func TestSynchronousImageResponseKeepsSlowErrorJSONValid(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/edits", nil)
	results := make(chan imageSyncResult, 1)

	go func() {
		time.Sleep(15 * time.Millisecond)
		results <- imageSyncResult{err: service.ErrContentRejected}
	}()

	h := &V1Handler{}
	h.writeSynchronousImageResponse(c, results, 5*time.Millisecond, 5*time.Millisecond)

	var body struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("slow error response is not valid JSON: %v", err)
	}
	if body.Error["code"] != "content_policy_violation" {
		t.Fatalf("error code = %#v", body.Error["code"])
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("committed streaming status = %d, want 200", recorder.Code)
	}
}

func TestV1ErrorResponsePreservesFastStatus(t *testing.T) {
	status, body := v1ErrorResponse(errors.Join(service.ErrUnknownModel, errors.New("missing")), nil)
	if status != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", status)
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Error map[string]any `json:"error"`
	}
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Error["code"] != "model_not_found" {
		t.Fatalf("error code = %#v", decoded.Error["code"])
	}
}

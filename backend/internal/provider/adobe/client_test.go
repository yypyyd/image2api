package adobe

import (
	"errors"
	"strings"
	"testing"
)

func TestContentRejectionClassification(t *testing.T) {
	privacyBody := `{"error_code":"reference_image_privacy_error","message":"The reference image contains a real person's face and cannot be used to generate content."}`
	if !isContentRejection(451, privacyBody) {
		t.Fatal("reference image privacy refusal should be classified as content rejection")
	}
	err := contentRejectionError(451, privacyBody)
	if !errors.Is(err, ErrContentRejected) || !strings.Contains(err.Error(), "真人面部") {
		t.Fatalf("privacy refusal = %v, want friendly ErrContentRejected", err)
	}
	if !isContentRejection(451, `{"error_code":"image_unsafe"}`) {
		t.Fatal("image_unsafe should be classified as content rejection")
	}
	imageErr := contentRejectionError(451, `{"error_code":"image_unsafe"}`)
	var imageRejection *ContentRejectionError
	if !errors.As(imageErr, &imageRejection) || imageRejection.Code != "image_unsafe" || !isRetryableGeneratedImageRejection(imageErr) {
		t.Fatalf("image refusal = %#v, want retryable typed image_unsafe", imageErr)
	}
	promptErr := contentRejectionError(451, `{"error":{"error_code":"prompt_unsafe"}}`)
	var promptRejection *ContentRejectionError
	if !errors.As(promptErr, &promptRejection) || promptRejection.Code != "prompt_unsafe" || isRetryableGeneratedImageRejection(promptErr) {
		t.Fatalf("prompt refusal = %#v, want non-retryable typed prompt_unsafe", promptErr)
	}
	if isRetryableGeneratedImageRejection(err) {
		t.Fatal("reference image privacy refusal must not retry")
	}
	if isContentRejection(451, `{"error_code":"legal_error","message":"{}"}`) {
		t.Fatal("generic legal_error must remain an upstream/legal failure")
	}
	if isContentRejection(500, privacyBody) {
		t.Fatal("non-451 response must not be classified as a content rejection")
	}
}

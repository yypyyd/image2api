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
	if isContentRejection(451, `{"error_code":"legal_error","message":"{}"}`) {
		t.Fatal("generic legal_error must remain an upstream/legal failure")
	}
	if isContentRejection(500, privacyBody) {
		t.Fatal("non-451 response must not be classified as a content rejection")
	}
}

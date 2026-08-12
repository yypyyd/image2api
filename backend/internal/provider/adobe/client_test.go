package adobe

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestSubmitGateSerializesAndSpacesStarts(t *testing.T) {
	client := NewClient("test", "")
	start := time.Now()
	_, releaseFirst, err := client.acquireSubmit(context.Background(), submitURL)
	if err != nil {
		t.Fatal(err)
	}

	acquired := make(chan time.Time, 1)
	go func() {
		_, release, acquireErr := client.acquireSubmit(context.Background(), submitURL)
		if acquireErr != nil {
			return
		}
		acquired <- time.Now()
		release()
	}()

	select {
	case <-acquired:
		t.Fatal("second submit acquired the gate before the first released it")
	case <-time.After(25 * time.Millisecond):
	}

	releaseFirst()
	select {
	case secondStarted := <-acquired:
		if spacing := secondStarted.Sub(start); spacing < adobeSubmitMinInterval-50*time.Millisecond {
			t.Fatalf("submit spacing = %v, want at least %v", spacing, adobeSubmitMinInterval)
		}
	case <-time.After(adobeSubmitMinInterval + time.Second):
		t.Fatal("second submit never acquired the gate")
	}
}

// A busy/paced lane must not hold up a different submit endpoint: 3p overload
// used to stall native image and video submits behind one global gate.
func TestSubmitLanesAreIndependent(t *testing.T) {
	client := NewClient("test", "")
	_, release, err := client.acquireSubmit(context.Background(), submitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	done := make(chan struct{})
	go func() {
		_, otherRelease, otherErr := client.acquireSubmit(context.Background(), image5SubmitURL)
		if otherErr == nil {
			otherRelease()
			close(done)
		}
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("native image submit blocked behind the 3p lane")
	}
}

func TestSubmitBreakerTripsAndFailsFast(t *testing.T) {
	client := NewClient("test", "")
	lane := client.lane(submitURL)
	for i := 0; i < submitBreakerThreshold; i++ {
		lane.record(isUpstreamOverloaded(408, []byte(`{"error_code":"timeout_error","message":"system under load"}`)))
	}
	if _, _, err := client.acquireSubmit(context.Background(), submitURL); !errors.Is(err, ErrUpstreamBusy) || !errors.Is(err, ErrTemporaryUpstream) {
		t.Fatalf("acquireSubmit() error = %v, want ErrUpstreamBusy wrapping ErrTemporaryUpstream", err)
	}
	// Another endpoint stays open, and a good response closes the breaker.
	if _, release, err := client.acquireSubmit(context.Background(), image5SubmitURL); err != nil {
		t.Fatalf("native image lane = %v, want open", err)
	} else {
		release()
	}
	lane.record(false)
	if _, release, err := client.acquireSubmit(context.Background(), submitURL); err != nil {
		t.Fatalf("acquireSubmit() after recovery = %v, want nil", err)
	} else {
		release()
	}
}

func TestSubmitGateHonorsCancellation(t *testing.T) {
	client := NewClient("test", "")
	_, release, err := client.acquireSubmit(context.Background(), submitURL)
	if err != nil {
		t.Fatal(err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := client.acquireSubmit(ctx, submitURL); !errors.Is(err, context.Canceled) {
		t.Fatalf("acquireSubmit() error = %v, want context.Canceled", err)
	}
}

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

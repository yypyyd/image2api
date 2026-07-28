package grok

import "testing"

// A Lite stream delivers the rendered image as a generated_image_card: a preview
// chunk ("-part-0", progress 50) followed by the finished one (progress 100).
const liteImageStream = `{"result":{"conversation":{"conversationId":"c1"}}}
{"result":{"response":{"token":"Generating","isThinking":true}}}
{"result":{"response":{"cardAttachment":{"jsonData":"{\"cardType\":\"generated_image_card\",\"image_chunk\":{\"imageUrl\":\"users/u1/generated/img-part-0/image.jpg\",\"progress\":50}}"}}}}
{"result":{"response":{"cardAttachment":{"jsonData":"{\"cardType\":\"generated_image_card\",\"image_chunk\":{\"imageUrl\":\"users/u1/generated/img/image.jpg\",\"progress\":100,\"moderated\":false}}"}}}}
{"result":{"response":{"token":"","isSoftStop":true}}}`

func TestFirstGeneratedImageCardAttachment(t *testing.T) {
	if got, want := firstGeneratedImage(liteImageStream), "users/u1/generated/img/image.jpg"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFirstGeneratedImageStreamingResponse(t *testing.T) {
	stream := `{"result":{"response":{"streamingImageGenerationResponse":{"imageUrl":"a-part-0.jpg","progress":40}}}}
{"result":{"response":{"streamingImageGenerationResponse":{"imageUrl":"a.jpg","progress":100}}}}`
	if got, want := firstGeneratedImage(stream), "a.jpg"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestFirstGeneratedImageSkipsModerated(t *testing.T) {
	stream := `{"result":{"response":{"streamingImageGenerationResponse":{"imageUrl":"bad.jpg","progress":100,"moderated":true}}}}
{"result":{"response":{"modelResponse":{"generatedImageUrls":["bad.jpg"]}}}}`
	if got := firstGeneratedImage(stream); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}

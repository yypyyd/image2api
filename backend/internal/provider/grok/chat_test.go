package grok

import "testing"

func TestChatResponseTextPrefersModelResponse(t *testing.T) {
	body := `{"result":{"conversation":{"conversationId":"c1"}}}` +
		`{"result":{"response":{"token":"Hel","isThinking":false,"messageTag":"final"}}}` +
		`{"result":{"response":{"token":"lo","isThinking":false,"messageTag":"final"}}}` +
		`{"result":{"response":{"modelResponse":{"message":"Hello, world."}}}}`
	text, err := chatResponseText(body)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if text != "Hello, world." {
		t.Fatalf("expected modelResponse message, got %q", text)
	}
}

func TestChatResponseTextFallsBackToTokens(t *testing.T) {
	body := `{"result":{"response":{"token":"think…","isThinking":true}}}` +
		`{"result":{"response":{"token":"<xai:tool_usage_card/>","isThinking":false,"messageTag":"tool_usage_card"}}}` +
		`{"result":{"response":{"token":"Hi ","isThinking":false,"messageTag":"final"}}}` +
		`{"result":{"response":{"token":"there","isThinking":false}}}`
	text, err := chatResponseText(body)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if text != "Hi there" {
		t.Fatalf("expected concatenated visible tokens, got %q", text)
	}
}

func TestChatResponseTextSurfacesStreamError(t *testing.T) {
	body := `{"result":{"response":{"error":{"message":"rate limited","code":8}}}}`
	text, err := chatResponseText(body)
	if text != "" {
		t.Fatalf("expected no text, got %q", text)
	}
	if err == nil || err.Error() != "rate limited" {
		t.Fatalf("expected stream error, got %v", err)
	}
}

func TestChatModeForModel(t *testing.T) {
	cases := map[string]string{
		"grok-chat-fast":   "fast",
		"grok-chat-expert": "expert",
		"grok-chat-auto":   "auto",
		"grok-chat-heavy":  "heavy",
		"grok-3":           "fast",
		"":                 "fast",
	}
	for in, want := range cases {
		if got := ChatModeForModel(in); got != want {
			t.Fatalf("ChatModeForModel(%q) = %q, want %q", in, got, want)
		}
	}
}

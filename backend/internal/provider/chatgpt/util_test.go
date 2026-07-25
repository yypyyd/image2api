package chatgpt

import "testing"

func TestContainsAsyncMarker(t *testing.T) {
	tests := []struct {
		name string
		text string
		want bool
	}{
		{name: "legacy async flag", text: `{"image_gen_async":true}`, want: true},
		{name: "legacy task id", text: `{"image_gen_task_id":"task_123"}`, want: true},
		{name: "new delegated tool signal", text: `{"content":{"content_type":"code","text":"{\"skipped_mainline\":true}"}}`, want: true},
		{name: "new unescaped tool signal", text: `{"skipped_mainline":true}`, want: true},
		{name: "delegated tool signal with whitespace", text: `{"content":{"content_type":"code","text":"{\"skipped_mainline\": true}"}}`, want: true},
		{name: "unescaped signal with whitespace", text: "{\n  \"skipped_mainline\" : true\n}", want: true},
		{name: "multiply escaped delegated signal", text: `{"text":"{\\\"skipped_mainline\\\": true}"}`, want: true},
		{name: "delegation explicitly false", text: `{"skipped_mainline": false}`, want: false},
		{name: "marker word in prompt", text: `{"prompt":"explain skipped_mainline"}`, want: false},
		{name: "ordinary code", text: `{"content":{"content_type":"code","text":"print(1)"}}`, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := containsAsyncMarker(tt.text); got != tt.want {
				t.Fatalf("containsAsyncMarker() = %v, want %v", got, tt.want)
			}
		})
	}
}

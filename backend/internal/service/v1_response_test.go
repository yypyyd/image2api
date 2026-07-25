package service

import (
	"errors"
	"testing"
)

func TestNormalizeImageResponseFormat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "default is base64", want: "b64_json"},
		{name: "base64", input: "b64_json", want: "b64_json"},
		{name: "url", input: " URL ", want: "url"},
		{name: "invalid", input: "bytes", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeImageResponseFormat(tt.input)
			if tt.wantErr {
				if !errors.Is(err, ErrUnsupportedParams) {
					t.Fatalf("normalizeImageResponseFormat() error = %v, want ErrUnsupportedParams", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("normalizeImageResponseFormat() error = %v", err)
			}
			if got != tt.want {
				t.Fatalf("normalizeImageResponseFormat() = %q, want %q", got, tt.want)
			}
		})
	}
}

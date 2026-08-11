package service

import "testing"

func TestValidateLoginIdentifierAllowsLegacyShortUsername(t *testing.T) {
	got, err := ValidateLoginIdentifier(" admin ")
	if err != nil {
		t.Fatalf("ValidateLoginIdentifier() error = %v", err)
	}
	if got != "admin" {
		t.Fatalf("ValidateLoginIdentifier() = %q, want %q", got, "admin")
	}
}

func TestValidateUsernameStillRejectsLegacyShortUsername(t *testing.T) {
	if _, err := ValidateUsername("admin"); err == nil {
		t.Fatal("ValidateUsername() accepted a username shorter than the registration minimum")
	}
}

func TestValidateLoginIdentifierRejectsInvalidUsername(t *testing.T) {
	tests := []struct {
		name       string
		identifier string
	}{
		{name: "empty", identifier: "   "},
		{name: "invalid character", identifier: "old_admin"},
		{name: "too long", identifier: "abcdefghijklmnopqrstuvwxy"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := ValidateLoginIdentifier(tt.identifier); err == nil {
				t.Fatalf("ValidateLoginIdentifier(%q) unexpectedly succeeded", tt.identifier)
			}
		})
	}
}

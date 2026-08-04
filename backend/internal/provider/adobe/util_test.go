package adobe

import (
	"encoding/base64"
	"encoding/json"
	"regexp"
	"testing"
)

func TestBuildARPSessionIDUsesFreshBrowserShape(t *testing.T) {
	first := buildARPSessionID()
	second := buildARPSessionID()
	if first == second {
		t.Fatal("buildARPSessionID() reused a session value")
	}

	raw, err := base64.StdEncoding.DecodeString(first)
	if err != nil {
		t.Fatalf("decode session: %v", err)
	}
	var session map[string]string
	if err := json.Unmarshal(raw, &session); err != nil {
		t.Fatalf("decode session json: %v", err)
	}
	if !regexp.MustCompile(`^[0-9a-f-]{36}$`).MatchString(session["sid"]) {
		t.Fatalf("unexpected sid: %q", session["sid"])
	}
	if !regexp.MustCompile(`^[0-9a-f]{32}_[0-9]+_[0-9]+_dUAL43-mnts-ants-d4_31ck__tt$`).MatchString(session["ftr"]) {
		t.Fatalf("unexpected ftr: %q", session["ftr"])
	}
}

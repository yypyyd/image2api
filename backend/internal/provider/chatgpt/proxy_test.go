package chatgpt

import "testing"

func TestMediaSessionStaysDirectWhenProxyConfigured(t *testing.T) {
	client := NewClient("http://%zz")
	if _, err := client.newDirectSession("token"); err != nil {
		t.Fatalf("direct media session used configured proxy: %v", err)
	}
	if _, err := client.newSession("token"); err == nil {
		t.Fatal("control session accepted malformed proxy")
	}
}

package imagine

import "testing"

func TestMediaClientStaysDirectWhenProxyConfigured(t *testing.T) {
	client := NewClient("http://%zz")
	if _, err := client.newDirectTLSClient(); err != nil {
		t.Fatalf("direct media client used configured proxy: %v", err)
	}
	if _, err := client.newTLSClient(); err == nil {
		t.Fatal("control client accepted malformed proxy")
	}
}

package chatgpt

import "testing"

func TestProxySessionsUseConfiguredProxy(t *testing.T) {
	client := NewClient("http://%zz")
	if _, err := client.newDirectSession("token"); err != nil {
		t.Fatalf("direct non-submit session used configured proxy: %v", err)
	}
	if _, err := client.newProxySession("token"); err == nil {
		t.Fatal("proxy session accepted malformed proxy")
	}
	if _, err := client.newSubmitSession("token"); err == nil {
		t.Fatal("submit session accepted malformed proxy")
	}
}

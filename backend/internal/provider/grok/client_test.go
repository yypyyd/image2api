package grok

import "testing"

func TestGlobalGrokProxyReplacesAndClearsPreviousValue(t *testing.T) {
	client := NewClient("http://legacy-proxy:8118")
	client.SetProxy(" socks5://explicit-proxy:1080 ")
	if client.proxyValue() != "socks5://explicit-proxy:1080" {
		t.Fatalf("explicit proxy = %q", client.proxyValue())
	}
	client.SetProxy("")
	if client.proxyValue() != "" {
		t.Fatalf("cleared proxy = %q, want direct egress", client.proxyValue())
	}
}

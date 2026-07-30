package grok

import "testing"

func TestConfiguredProxyUsesGrokEnvironmentFallback(t *testing.T) {
	t.Setenv("GROK_PROXY_URL", " http://grok-proxy:8118 ")

	client := NewClient("")
	if client.proxy != "http://grok-proxy:8118" {
		t.Fatalf("NewClient proxy = %q", client.proxy)
	}

	client.SetProxy("")
	if client.proxy != "http://grok-proxy:8118" {
		t.Fatalf("SetProxy fallback = %q", client.proxy)
	}

	client.SetProxy(" socks5://explicit-proxy:1080 ")
	if client.proxy != "socks5://explicit-proxy:1080" {
		t.Fatalf("explicit proxy = %q", client.proxy)
	}
}

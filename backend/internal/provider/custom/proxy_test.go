package custom

import (
	"net/http"
	"testing"
)

func TestHTTPClientUsesAndClearsGlobalProxy(t *testing.T) {
	client := NewClient()
	client.SetProxy(" http://proxy.example:8080 ")
	proxied, err := client.httpClient()
	if err != nil {
		t.Fatal(err)
	}
	transport := proxied.Transport.(*http.Transport)
	req, _ := http.NewRequest(http.MethodGet, "https://upstream.example/v1/models", nil)
	proxyURL, err := transport.Proxy(req)
	if err != nil || proxyURL == nil || proxyURL.Host != "proxy.example:8080" {
		t.Fatalf("proxy = %v err=%v, want proxy.example:8080", proxyURL, err)
	}

	client.SetProxy("")
	direct, err := client.httpClient()
	if err != nil {
		t.Fatal(err)
	}
	if direct.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("empty global proxy must disable environment and explicit proxies")
	}
}

func TestInvalidProxyErrorDoesNotLeakCredentials(t *testing.T) {
	client := NewClient()
	client.SetProxy("http://broken@")
	_, err := client.httpClient()
	if err == nil {
		t.Fatal("invalid proxy accepted")
	}
	if got := err.Error(); got != "invalid global proxy configuration" {
		t.Fatalf("error = %q, want sanitized configuration error", got)
	}
}

func TestDirectMediaClientIgnoresConfiguredProxy(t *testing.T) {
	client := NewClient()
	client.SetProxy("http://broken@")
	direct, err := client.httpClientP(false)
	if err != nil {
		t.Fatalf("direct media client used configured proxy: %v", err)
	}
	if direct.Transport.(*http.Transport).Proxy != nil {
		t.Fatal("direct media client must disable configured and environment proxies")
	}
}

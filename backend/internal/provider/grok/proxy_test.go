package grok

import (
	"testing"
	"time"
)

func TestProxyClientsUseConfiguredProxy(t *testing.T) {
	client := NewClient("http://%zz")
	if _, err := client.newDirectTLSClient(); err != nil {
		t.Fatalf("direct non-submit client used configured proxy: %v", err)
	}
	if _, err := client.newProxyTLSClient(); err == nil {
		t.Fatal("proxy client accepted malformed proxy")
	}
	if _, err := client.newSubmitTLSClient(); err == nil {
		t.Fatal("submit client accepted malformed proxy")
	}
	if _, err := client.newBuildHTTPClient(time.Second, false); err != nil {
		t.Fatalf("direct Build OAuth client used configured proxy: %v", err)
	}
	if _, err := client.newBuildHTTPClient(time.Second, true); err == nil {
		t.Fatal("Build submit client accepted malformed proxy")
	}
}

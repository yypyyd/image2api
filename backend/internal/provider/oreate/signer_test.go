package oreate

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

func TestBantiReportWaiterCompletesOnlyArmedReport(t *testing.T) {
	waiter := newBantiReportWaiter()
	preArmID := network.RequestID("pre-arm")
	waiter.listen(&network.EventRequestWillBeSent{
		RequestID: preArmID,
		Request:   &network.Request{URL: "https://banti.oreateai.com/dr"},
	})
	waiter.listen(&network.EventResponseReceived{
		RequestID: preArmID,
		Response:  &network.Response{URL: "https://banti.oreateai.com/dr", Status: http.StatusOK},
	})
	waiter.listen(&network.EventLoadingFinished{RequestID: preArmID})
	select {
	case <-waiter.done:
		t.Fatal("report created before arm completed the waiter")
	default:
	}

	waiter.arm()
	reportID := network.RequestID("armed-report")
	waiter.listen(&network.EventRequestWillBeSent{
		RequestID: reportID,
		Request:   &network.Request{URL: "https://banti.oreateai.com/dr"},
	})
	waiter.listen(&network.EventResponseReceived{
		RequestID: reportID,
		Response:  &network.Response{URL: "https://banti.oreateai.com/dr", Status: http.StatusOK},
	})
	waiter.listen(&network.EventLoadingFinished{RequestID: reportID})
	if err := waiter.wait(context.Background(), time.Second); err != nil {
		t.Fatalf("wait() error = %v", err)
	}
}

func TestBantiReportWaiterRejectsNonSuccess(t *testing.T) {
	waiter := newBantiReportWaiter()
	waiter.arm()
	reportID := network.RequestID("failed-report")
	waiter.listen(&network.EventRequestWillBeSent{
		RequestID: reportID,
		Request:   &network.Request{URL: "https://banti.oreateai.com/dr"},
	})
	waiter.listen(&network.EventResponseReceived{
		RequestID: reportID,
		Response:  &network.Response{URL: "https://banti.oreateai.com/dr", Status: http.StatusBadGateway},
	})
	waiter.listen(&network.EventLoadingFinished{RequestID: reportID})
	if err := waiter.wait(context.Background(), time.Second); !errors.Is(err, ErrRiskControl) {
		t.Fatalf("wait() error = %v, want ErrRiskControl", err)
	}
}

func TestParseChromiumProxyStripsCredentials(t *testing.T) {
	proxy, err := parseChromiumProxy(" http://proxy-user:proxy%20pass@proxy.example:8080/ ")
	if err != nil {
		t.Fatalf("parseChromiumProxy() error = %v", err)
	}
	if proxy.server != "http://proxy.example:8080" {
		t.Fatalf("server = %q", proxy.server)
	}
	if proxy.username != "proxy-user" || proxy.password != "proxy pass" || !proxy.authenticate {
		t.Fatalf("credentials were not preserved in memory")
	}
	if strings.Contains(proxy.server, "proxy-user") || strings.Contains(proxy.server, "proxy%20pass") {
		t.Fatalf("safe proxy server contains credentials: %q", proxy.server)
	}
}

func TestParseChromiumProxyWithoutCredentials(t *testing.T) {
	proxy, err := parseChromiumProxy("socks5://proxy.example:1080")
	if err != nil || proxy.server != "socks5://proxy.example:1080" || proxy.authenticate {
		t.Fatalf("parseChromiumProxy() = %#v, %v", proxy, err)
	}
}

func TestParseChromiumProxyErrorsDoNotEchoInput(t *testing.T) {
	const sentinel = "credential-that-must-not-leak"
	for _, raw := range []string{
		"http://user:" + sentinel + "@",
		"socks5://user:" + sentinel + "@proxy.example:1080",
		"file://user:" + sentinel + "@proxy.example/path",
	} {
		_, err := parseChromiumProxy(raw)
		if err == nil {
			t.Fatalf("parseChromiumProxy(%q) unexpectedly succeeded", raw)
		}
		if strings.Contains(err.Error(), sentinel) {
			t.Fatalf("error disclosed proxy credentials: %v", err)
		}
	}
}

func TestChromiumProxyAuthentication(t *testing.T) {
	chromePath := chromiumExecPath()
	if chromePath == "" {
		t.Skip("Chrome/Chromium is not installed")
	}
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "<html><head><title>proxy-auth-ok</title></head></html>")
	}))
	defer target.Close()

	const username, passphrase = "proxy-user", "proxy-pass"
	wantAuth := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+passphrase))
	var authenticated atomic.Bool
	proxyServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != wantAuth {
			w.Header().Set("Proxy-Authenticate", `Basic realm="oreate-test"`)
			w.WriteHeader(http.StatusProxyAuthRequired)
			return
		}
		authenticated.Store(true)
		if r.Method != http.MethodConnect {
			http.Error(w, "CONNECT required", http.StatusMethodNotAllowed)
			return
		}
		upstream, err := net.DialTimeout("tcp", r.Host, 5*time.Second)
		if err != nil {
			http.Error(w, "upstream unavailable", http.StatusBadGateway)
			return
		}
		defer upstream.Close()
		client, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer client.Close()
		_, _ = fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, client)
			_ = upstream.(*net.TCPConn).CloseWrite()
			close(done)
		}()
		_, _ = io.Copy(client, upstream)
		<-done
	}))
	defer proxyServer.Close()

	rawProxy := "http://" + username + ":" + passphrase + "@" + strings.TrimPrefix(proxyServer.URL, "http://")
	proxy, err := parseChromiumProxy(rawProxy)
	if err != nil {
		t.Fatalf("parseChromiumProxy() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bridge, err := startProxyBridge(ctx, proxy)
	if err != nil {
		t.Fatalf("startProxyBridge() error = %v", err)
	}
	defer bridge.Close()
	opts := append([]chromedp.ExecAllocatorOption{}, chromedp.DefaultExecAllocatorOptions[:]...)
	opts = append(opts,
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("ignore-certificate-errors", true),
		chromedp.Flag("proxy-bypass-list", "<-loopback>"),
		chromedp.ProxyServer(bridge.URL()),
	)
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(ctx, opts...)
	defer cancelAlloc()
	browserCtx, cancelBrowser := chromedp.NewContext(allocCtx)
	defer cancelBrowser()
	var title string
	err = chromedp.Run(browserCtx,
		chromedp.ActionFunc(setSignerBlockedURLs),
		chromedp.Navigate(target.URL),
		chromedp.Title(&title),
	)
	if err != nil {
		t.Fatalf("Chromium navigation error: %v", err)
	}
	if title != "proxy-auth-ok" || !authenticated.Load() {
		t.Fatalf("title/authenticated = %q/%t", title, authenticated.Load())
	}
}

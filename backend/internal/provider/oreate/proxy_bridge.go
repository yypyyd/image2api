package oreate

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// proxyBridge gives Chromium a loopback proxy without exposing the upstream
// proxy's userinfo in Chrome arguments or to the browser's website requests.
// It is deliberately short-lived: one signer invocation owns one bridge.
type proxyBridge struct {
	listener  net.Listener
	server    *http.Server
	transport *http.Transport
	upstream  *url.URL
	auth      string
	once      sync.Once
}

func startProxyBridge(ctx context.Context, upstream chromiumProxy) (*proxyBridge, error) {
	parsed, err := url.Parse(upstream.server)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return nil, errors.New("oreate signer: authenticated proxy bridge requires HTTP or HTTPS")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("oreate signer: proxy bridge listen failed")
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(upstream.username+":"+upstream.password))
	transport := &http.Transport{
		Proxy: func(*http.Request) (*url.URL, error) { return parsed, nil },
		ProxyConnectHeader: http.Header{
			"Proxy-Authorization": []string{auth},
		},
		DialContext:           (&net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   20 * time.Second,
		ResponseHeaderTimeout: 45 * time.Second,
		IdleConnTimeout:       30 * time.Second,
	}
	bridge := &proxyBridge{
		listener:  listener,
		transport: transport,
		upstream:  parsed,
		auth:      auth,
	}
	bridge.server = &http.Server{
		Handler:           http.HandlerFunc(bridge.handle),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		_ = bridge.server.Serve(listener)
	}()
	go func() {
		<-ctx.Done()
		bridge.Close()
	}()
	return bridge, nil
}

func (b *proxyBridge) URL() string {
	if b == nil || b.listener == nil {
		return ""
	}
	return "http://" + b.listener.Addr().String()
}

func (b *proxyBridge) Close() {
	if b == nil {
		return
	}
	b.once.Do(func() {
		if b.server != nil {
			_ = b.server.Close()
		}
		if b.transport != nil {
			b.transport.CloseIdleConnections()
		}
	})
}

func (b *proxyBridge) handle(w http.ResponseWriter, r *http.Request) {
	if strings.EqualFold(r.Method, http.MethodConnect) {
		b.handleConnect(w, r)
		return
	}
	if r.URL == nil || !r.URL.IsAbs() || r.URL.Host == "" {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	req := r.Clone(r.Context())
	req.RequestURI = ""
	req.Header = r.Header.Clone()
	stripProxyHeaders(req.Header)
	req.Header.Set("Proxy-Authorization", b.auth)
	resp, err := b.transport.RoundTrip(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	copyProxyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (b *proxyBridge) handleConnect(w http.ResponseWriter, r *http.Request) {
	upstream, err := b.dialUpstream(r.Context())
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	deadline := time.Now().Add(30 * time.Second)
	_ = upstream.SetDeadline(deadline)
	if _, err := fmt.Fprintf(upstream,
		"CONNECT %s HTTP/1.1\r\nHost: %s\r\nProxy-Authorization: %s\r\n\r\n",
		r.Host, r.Host, b.auth); err != nil {
		_ = upstream.Close()
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	response, err := http.ReadResponse(bufio.NewReader(upstream), r)
	if err != nil || response.StatusCode != http.StatusOK {
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		_ = upstream.Close()
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	_ = upstream.SetDeadline(time.Time{})
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		_ = upstream.Close()
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		_ = upstream.Close()
		return
	}
	defer client.Close()
	defer upstream.Close()
	_, _ = fmt.Fprint(client, "HTTP/1.1 200 Connection Established\r\n\r\n")
	done := make(chan struct{}, 1)
	go func() {
		_, _ = io.Copy(upstream, client)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, upstream)
		done <- struct{}{}
	}()
	<-done
}

func (b *proxyBridge) dialUpstream(ctx context.Context) (net.Conn, error) {
	dialer := &net.Dialer{Timeout: 20 * time.Second, KeepAlive: 30 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", b.upstream.Host)
	if err != nil {
		return nil, err
	}
	if b.upstream.Scheme != "https" {
		return conn, nil
	}
	tlsConn := tls.Client(conn, &tls.Config{ServerName: b.upstream.Hostname(), MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func stripProxyHeaders(header http.Header) {
	for _, name := range []string{"Connection", "Keep-Alive", "Proxy-Connection", "Proxy-Authenticate", "Proxy-Authorization", "TE", "Trailer", "Transfer-Encoding", "Upgrade"} {
		header.Del(name)
	}
}

func copyProxyHeaders(dst, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, "Proxy-Authenticate") || strings.EqualFold(key, "Proxy-Authorization") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
	stripProxyHeaders(dst)
}

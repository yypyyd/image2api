package proxybridge

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

// Bridge gives Chromium a loopback proxy while keeping authenticated upstream
// proxy credentials out of Chrome arguments and destination requests.
type Bridge struct {
	listener  net.Listener
	server    *http.Server
	transport *http.Transport
	upstream  *url.URL
	auth      string
	done      chan struct{}
	once      sync.Once
	tunnelMu  sync.Mutex
	tunnels   map[*connectTunnel]struct{}
	closed    bool
}

type connectTunnel struct {
	client   net.Conn
	upstream net.Conn
}

// Start creates a short-lived loopback bridge for an authenticated HTTP(S)
// proxy URL. The bridge closes when ctx is done or Close is called.
func Start(ctx context.Context, rawURL string) (*Bridge, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Opaque != "" || parsed.Host == "" || parsed.User == nil ||
		(parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("authenticated proxy bridge requires an HTTP or HTTPS proxy URL")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("authenticated proxy bridge requires an HTTP or HTTPS proxy URL")
	}
	username := parsed.User.Username()
	password, _ := parsed.User.Password()
	parsed.User = nil
	parsed.Path = ""
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.Fragment = ""

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("authenticated proxy bridge could not listen on loopback")
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password))
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
	bridge := &Bridge{
		listener:  listener,
		transport: transport,
		upstream:  parsed,
		auth:      auth,
		done:      make(chan struct{}),
		tunnels:   make(map[*connectTunnel]struct{}),
	}
	bridge.server = &http.Server{
		Handler:           http.HandlerFunc(bridge.handle),
		ReadHeaderTimeout: 15 * time.Second,
	}
	go func() {
		_ = bridge.server.Serve(listener)
	}()
	go func() {
		select {
		case <-ctx.Done():
			bridge.Close()
		case <-bridge.done:
		}
	}()
	return bridge, nil
}

func (b *Bridge) URL() string {
	if b == nil || b.listener == nil {
		return ""
	}
	return "http://" + b.listener.Addr().String()
}

func (b *Bridge) Close() {
	if b == nil {
		return
	}
	b.once.Do(func() {
		b.tunnelMu.Lock()
		b.closed = true
		active := make([]*connectTunnel, 0, len(b.tunnels))
		for tunnel := range b.tunnels {
			active = append(active, tunnel)
		}
		b.tunnels = nil
		b.tunnelMu.Unlock()
		close(b.done)
		if b.server != nil {
			_ = b.server.Close()
		}
		for _, tunnel := range active {
			_ = tunnel.client.Close()
			_ = tunnel.upstream.Close()
		}
		if b.transport != nil {
			b.transport.CloseIdleConnections()
		}
	})
}

func (b *Bridge) handle(w http.ResponseWriter, r *http.Request) {
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

func (b *Bridge) handleConnect(w http.ResponseWriter, r *http.Request) {
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
	tunnel := &connectTunnel{client: client, upstream: upstream}
	if !b.registerTunnel(tunnel) {
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	defer b.unregisterTunnel(tunnel)
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

func (b *Bridge) registerTunnel(tunnel *connectTunnel) bool {
	b.tunnelMu.Lock()
	defer b.tunnelMu.Unlock()
	if b.closed {
		return false
	}
	b.tunnels[tunnel] = struct{}{}
	return true
}

func (b *Bridge) unregisterTunnel(tunnel *connectTunnel) {
	b.tunnelMu.Lock()
	delete(b.tunnels, tunnel)
	b.tunnelMu.Unlock()
}

func (b *Bridge) dialUpstream(ctx context.Context) (net.Conn, error) {
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

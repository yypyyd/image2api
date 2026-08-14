package grok

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	xproxy "golang.org/x/net/proxy"
)

const (
	buildClientID      = "b1a00492-073a-47ea-816f-4c329264a828"
	buildScope         = "openid profile email offline_access grok-cli:access api:access conversations:read conversations:write workspaces:read workspaces:write"
	buildAccountsURL   = "https://accounts.x.ai/"
	buildDeviceURL     = "https://auth.x.ai/oauth2/device/code"
	buildVerifyURL     = "https://auth.x.ai/oauth2/device/verify"
	buildApproveURL    = "https://auth.x.ai/oauth2/device/approve"
	buildTokenURL      = "https://auth.x.ai/oauth2/token"
	buildResponsesURL  = "https://cli-chat-proxy.grok.com/v1/responses"
	buildClientVersion = "0.2.111"
	maxBuildBody       = 8 << 20
)

// BuildCredential is the renewable xAI OAuth credential obtained by authorizing
// Grok Build with an existing grok.com SSO session.
type BuildCredential struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	ExpiresAt    time.Time
}

type buildOAuthEndpoints struct {
	Accounts string
	Device   string
	Verify   string
	Approve  string
	Token    string
}

var defaultBuildOAuthEndpoints = buildOAuthEndpoints{
	Accounts: buildAccountsURL,
	Device:   buildDeviceURL,
	Verify:   buildVerifyURL,
	Approve:  buildApproveURL,
	Token:    buildTokenURL,
}

// IsBuildTextModel distinguishes literal Grok Build models from Grok Web's
// mode-based chat aliases. Web sends modeId=fast/auto/expert/heavy; Build sends
// the actual model name to the Responses endpoint.
func IsBuildTextModel(model string) bool {
	name := strings.ToLower(strings.TrimSpace(model))
	if name == "" || strings.HasPrefix(name, "grok-chat-") || strings.Contains(name, "imagine") {
		return false
	}
	return strings.HasPrefix(name, "grok-")
}

// ConvertSSOToBuild performs the same unattended Device OAuth conversion used
// by grok2api: the Web SSO cookie opens accounts.x.ai, verifies and approves the
// device request, then exchanges it for renewable Build tokens.
func (c *Client) ConvertSSOToBuild(ctx context.Context, sso string) (BuildCredential, error) {
	sso = normalizeSSOToken(sso)
	if sso == "" {
		return BuildCredential{}, ErrAuth
	}
	client, err := c.newBuildHTTPClient(100*time.Second, true)
	if err != nil {
		return BuildCredential{}, fmt.Errorf("%w: create oauth client: %v", ErrTemporaryUpstream, err)
	}
	flow := &buildOAuthFlow{
		client: client,
		cookies: map[string]string{
			"sso":    sso,
			"sso-rw": sso,
		},
		endpoints: defaultBuildOAuthEndpoints,
	}
	credential, err := flow.convert(ctx)
	if err != nil {
		return BuildCredential{}, err
	}
	return credential, nil
}

// RefreshBuildCredential rotates an expiring Build OAuth access token.
func (c *Client) RefreshBuildCredential(ctx context.Context, refreshToken string) (BuildCredential, error) {
	refreshToken = strings.TrimSpace(refreshToken)
	if refreshToken == "" {
		return BuildCredential{}, ErrAuth
	}
	client, err := c.newBuildHTTPClient(45*time.Second, true)
	if err != nil {
		return BuildCredential{}, fmt.Errorf("%w: create oauth client: %v", ErrTemporaryUpstream, err)
	}
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {buildClientID},
		"refresh_token": {refreshToken},
	}
	credential, oauthCode, err := exchangeBuildToken(ctx, client, buildTokenURL, form, refreshToken)
	if err != nil {
		if oauthCode == "access_denied" || oauthCode == "expired_token" || oauthCode == "invalid_grant" || oauthCode == "invalid_token" {
			return BuildCredential{}, fmt.Errorf("%w: build refresh denied", ErrAuth)
		}
		return BuildCredential{}, err
	}
	return credential, nil
}

// GenerateBuildText calls Grok Build's native Responses API and returns the
// final assistant text. The service wraps it as Chat Completions JSON/SSE for
// downstream compatibility.
func (c *Client) GenerateBuildText(ctx context.Context, accessToken, prompt, model string) (string, error) {
	client, err := c.newBuildHTTPClient(10*time.Minute, true)
	if err != nil {
		return "", fmt.Errorf("%w: create build client: %v", ErrTemporaryUpstream, err)
	}
	return generateBuildText(ctx, client, buildResponsesURL, accessToken, prompt, model)
}

func generateBuildText(ctx context.Context, client *http.Client, endpoint, accessToken, prompt, model string) (string, error) {
	accessToken = strings.TrimSpace(strings.TrimPrefix(accessToken, "Bearer "))
	prompt = strings.TrimSpace(prompt)
	model = strings.TrimSpace(model)
	if accessToken == "" {
		return "", ErrAuth
	}
	if prompt == "" || model == "" {
		return "", fmt.Errorf("grok build: prompt and model required")
	}
	payload, _ := json.Marshal(map[string]any{
		"model":  model,
		"input":  prompt,
		"stream": false,
		"store":  false,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-XAI-Token-Auth", "xai-grok-cli")
	req.Header.Set("x-authenticateresponse", "authenticate-response")
	req.Header.Set("x-grok-client-version", buildClientVersion)
	req.Header.Set("x-grok-client-identifier", "grok-shell")
	req.Header.Set("x-grok-client-mode", "headless")
	req.Header.Set("x-grok-agent-id", uuid.NewString())
	req.Header.Set("x-grok-req-id", uuid.NewString())
	req.Header.Set("x-grok-model-override", model)
	req.Header.Set("User-Agent", "grok-shell/"+buildClientVersion+" (linux; x86_64)")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: build request failed", ErrTemporaryUpstream)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBuildBody+1))
	if err != nil {
		return "", fmt.Errorf("%w: build response read failed", ErrTemporaryUpstream)
	}
	if len(body) > maxBuildBody {
		return "", fmt.Errorf("%w: build response too large", ErrTemporaryUpstream)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", mapBuildStatus(resp.StatusCode, body)
	}
	text, responseErr := buildResponseText(body)
	if text == "" {
		if responseErr != "" {
			return "", fmt.Errorf("%w: %s", ErrTemporaryUpstream, responseErr)
		}
		return "", fmt.Errorf("%w: empty build response", ErrTemporaryUpstream)
	}
	return text, nil
}

type buildOAuthFlow struct {
	client    *http.Client
	cookies   map[string]string
	endpoints buildOAuthEndpoints
}

func (f *buildOAuthFlow) convert(ctx context.Context) (BuildCredential, error) {
	status, finalURL, _, err := f.do(ctx, http.MethodGet, f.endpoints.Accounts, nil)
	if err != nil {
		return BuildCredential{}, err
	}
	if status == http.StatusUnauthorized || strings.Contains(finalURL, "sign-in") || strings.Contains(finalURL, "sign-up") {
		return BuildCredential{}, ErrAuth
	}
	if status < 200 || status >= 400 {
		return BuildCredential{}, fmt.Errorf("%w: validate SSO HTTP %d", ErrTemporaryUpstream, status)
	}

	form := url.Values{"client_id": {buildClientID}, "scope": {buildScope}, "referrer": {"grok-build"}}
	status, _, body, err := f.do(ctx, http.MethodPost, f.endpoints.Device, form)
	if err != nil {
		return BuildCredential{}, err
	}
	if status < 200 || status >= 300 {
		return BuildCredential{}, fmt.Errorf("%w: start device oauth HTTP %d", ErrTemporaryUpstream, status)
	}
	var device struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		Interval                int    `json:"interval"`
		ExpiresIn               int    `json:"expires_in"`
	}
	if json.Unmarshal(body, &device) != nil || device.DeviceCode == "" || device.UserCode == "" || device.VerificationURIComplete == "" {
		return BuildCredential{}, fmt.Errorf("%w: incomplete device oauth response", ErrTemporaryUpstream)
	}
	if device.Interval <= 0 {
		device.Interval = 5
	}
	if device.ExpiresIn <= 0 {
		device.ExpiresIn = 1800
	}

	status, _, _, err = f.do(ctx, http.MethodGet, device.VerificationURIComplete, nil)
	if err != nil || status < 200 || status >= 400 {
		if err != nil {
			return BuildCredential{}, err
		}
		return BuildCredential{}, fmt.Errorf("%w: open verification HTTP %d", ErrTemporaryUpstream, status)
	}
	status, finalURL, _, err = f.do(ctx, http.MethodPost, f.endpoints.Verify, url.Values{"user_code": {device.UserCode}})
	if err != nil {
		return BuildCredential{}, err
	}
	if status < 200 || status >= 400 || !strings.Contains(finalURL, "consent") {
		return BuildCredential{}, fmt.Errorf("%w: SSO device verification failed", ErrAuth)
	}
	status, finalURL, _, err = f.do(ctx, http.MethodPost, f.endpoints.Approve, url.Values{
		"user_code": {device.UserCode}, "action": {"allow"}, "principal_type": {"User"}, "principal_id": {""},
	})
	if err != nil {
		return BuildCredential{}, err
	}
	if status < 200 || status >= 400 || !strings.Contains(finalURL, "done") {
		return BuildCredential{}, fmt.Errorf("%w: SSO device approval failed", ErrAuth)
	}

	deadline := time.Now().Add(min(time.Duration(device.ExpiresIn)*time.Second, 75*time.Second))
	interval := time.Duration(device.Interval) * time.Second
	if interval < time.Second {
		interval = time.Second
	}
	for time.Now().Before(deadline) {
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return BuildCredential{}, ctx.Err()
		case <-timer.C:
		}
		credential, code, tokenErr := exchangeBuildToken(ctx, f.client, f.endpoints.Token, url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {buildClientID}, "device_code": {device.DeviceCode},
		}, "")
		if tokenErr == nil {
			return credential, nil
		}
		switch code {
		case "authorization_pending":
			continue
		case "slow_down":
			interval += 5 * time.Second
			continue
		case "access_denied", "expired_token":
			return BuildCredential{}, ErrAuth
		default:
			return BuildCredential{}, tokenErr
		}
	}
	return BuildCredential{}, fmt.Errorf("%w: device oauth timed out", ErrTemporaryUpstream)
}

func (f *buildOAuthFlow) do(ctx context.Context, method, endpoint string, form url.Values) (int, string, []byte, error) {
	currentURL, currentMethod, currentForm := endpoint, method, form
	for redirects := 0; redirects <= 8; redirects++ {
		if !safeXAIURL(currentURL) {
			return 0, currentURL, nil, fmt.Errorf("%w: unsafe oauth redirect", ErrTemporaryUpstream)
		}
		var body io.Reader
		if currentForm != nil {
			body = strings.NewReader(currentForm.Encode())
		}
		req, err := http.NewRequestWithContext(ctx, currentMethod, currentURL, body)
		if err != nil {
			return 0, currentURL, nil, err
		}
		req.Header.Set("Accept", "application/json, text/html;q=0.9, */*;q=0.8")
		req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
		req.Header.Set("User-Agent", userAgent)
		if cookie := f.cookieHeader(); cookie != "" {
			req.Header.Set("Cookie", cookie)
		}
		if currentForm != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := f.client.Do(req)
		if err != nil {
			return 0, currentURL, nil, fmt.Errorf("%w: oauth request failed", ErrTemporaryUpstream)
		}
		f.captureCookies(resp)
		data, readErr := io.ReadAll(io.LimitReader(resp.Body, 2<<20+1))
		_ = resp.Body.Close()
		if readErr != nil || len(data) > 2<<20 {
			return resp.StatusCode, currentURL, nil, fmt.Errorf("%w: invalid oauth response", ErrTemporaryUpstream)
		}
		if resp.StatusCode < 300 || resp.StatusCode > 399 {
			return resp.StatusCode, currentURL, data, nil
		}
		location := strings.TrimSpace(resp.Header.Get("Location"))
		if location == "" {
			return resp.StatusCode, currentURL, data, fmt.Errorf("%w: oauth redirect missing location", ErrTemporaryUpstream)
		}
		base, _ := url.Parse(currentURL)
		next, parseErr := url.Parse(location)
		if parseErr != nil {
			return resp.StatusCode, currentURL, data, parseErr
		}
		currentURL = base.ResolveReference(next).String()
		if resp.StatusCode == http.StatusSeeOther || ((resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound) && currentMethod != http.MethodGet && currentMethod != http.MethodHead) {
			currentMethod, currentForm = http.MethodGet, nil
		}
	}
	return 0, currentURL, nil, fmt.Errorf("%w: too many oauth redirects", ErrTemporaryUpstream)
}

func (f *buildOAuthFlow) captureCookies(resp *http.Response) {
	for _, cookie := range resp.Cookies() {
		name, value := strings.TrimSpace(cookie.Name), strings.TrimSpace(cookie.Value)
		if name == "" || len(name) > 128 || len(value) > 16384 || strings.ContainsAny(name+value, "\r\n\x00") {
			continue
		}
		if cookie.MaxAge < 0 {
			delete(f.cookies, name)
		} else {
			f.cookies[name] = value
		}
	}
}

func (f *buildOAuthFlow) cookieHeader() string {
	keys := make([]string, 0, len(f.cookies))
	for key := range f.cookies {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+f.cookies[key])
	}
	return strings.Join(parts, "; ")
}

func exchangeBuildToken(ctx context.Context, client *http.Client, endpoint string, form url.Values, fallbackRefresh string) (BuildCredential, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return BuildCredential{}, "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return BuildCredential{}, "", fmt.Errorf("%w: token exchange failed", ErrTemporaryUpstream)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20+1))
	if err != nil || len(body) > 1<<20 {
		return BuildCredential{}, "", fmt.Errorf("%w: invalid token response", ErrTemporaryUpstream)
	}
	var value struct {
		AccessToken      string `json:"access_token"`
		RefreshToken     string `json:"refresh_token"`
		IDToken          string `json:"id_token"`
		ExpiresIn        int    `json:"expires_in"`
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if err := json.Unmarshal(body, &value); err != nil {
		return BuildCredential{}, "", fmt.Errorf("%w: malformed token response", ErrTemporaryUpstream)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || value.AccessToken == "" {
		code := strings.TrimSpace(value.Error)
		if code == "" {
			code = "oauth_http_" + strconv.Itoa(resp.StatusCode)
		}
		return BuildCredential{}, code, fmt.Errorf("%w: xAI oauth %s", ErrTemporaryUpstream, code)
	}
	if value.RefreshToken == "" {
		value.RefreshToken = fallbackRefresh
	}
	if value.ExpiresIn <= 0 {
		value.ExpiresIn = 3600
	}
	return BuildCredential{
		AccessToken: value.AccessToken, RefreshToken: value.RefreshToken, IDToken: value.IDToken,
		ExpiresAt: time.Now().UTC().Add(time.Duration(value.ExpiresIn) * time.Second),
	}, "", nil
}

func (c *Client) newBuildHTTPClient(timeout time.Duration, useProxy bool) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// Empty global proxy means direct local egress, regardless of process-level
	// HTTP_PROXY/HTTPS_PROXY environment variables.
	transport.Proxy = nil
	if proxyRaw := c.proxyValue(); useProxy && proxyRaw != "" {
		proxyURL, err := url.Parse(proxyRaw)
		if err != nil || proxyURL.Host == "" {
			return nil, errors.New("invalid global proxy configuration")
		}
		switch strings.ToLower(proxyURL.Scheme) {
		case "http", "https":
			transport.Proxy = http.ProxyURL(proxyURL)
		case "socks5", "socks5h":
			var auth *xproxy.Auth
			if proxyURL.User != nil {
				password, _ := proxyURL.User.Password()
				auth = &xproxy.Auth{User: proxyURL.User.Username(), Password: password}
			}
			dialer, dialErr := xproxy.SOCKS5("tcp", proxyURL.Host, auth, &net.Dialer{Timeout: timeout})
			if dialErr != nil {
				return nil, errors.New("invalid global proxy configuration")
			}
			transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
				if contextDialer, ok := dialer.(xproxy.ContextDialer); ok {
					return contextDialer.DialContext(ctx, network, address)
				}
				return dialer.Dial(network, address)
			}
		default:
			return nil, errors.New("unsupported global proxy scheme")
		}
	}
	return &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}, nil
}

func normalizeSSOToken(value string) string {
	value = strings.TrimSpace(strings.TrimPrefix(value, "Bearer "))
	if strings.HasPrefix(strings.ToLower(value), "sso=") {
		value = strings.TrimSpace(value[len("sso="):])
	}
	if token, _, found := strings.Cut(value, ";"); found {
		value = strings.TrimSpace(token)
	}
	return strings.NewReplacer("\r", "", "\n", "", "\x00", "").Replace(value)
}

func safeXAIURL(raw string) bool {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Hostname() == "" {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "x.ai" || strings.HasSuffix(host, ".x.ai")
}

func mapBuildStatus(status int, body []byte) error {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("%w: build HTTP %d", ErrAuth, status)
	case http.StatusTooManyRequests:
		return fmt.Errorf("%w: build rate limit", ErrQuotaExhausted)
	}
	message := buildErrorMessage(body)
	if message == "" {
		message = "HTTP " + strconv.Itoa(status)
	}
	return fmt.Errorf("%w: %s", ErrTemporaryUpstream, message)
}

func buildErrorMessage(body []byte) string {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return ""
	}
	if errValue, ok := root["error"].(map[string]any); ok {
		if message, _ := errValue["message"].(string); strings.TrimSpace(message) != "" {
			return clip([]byte(strings.TrimSpace(message)), 240)
		}
	}
	if message, _ := root["message"].(string); strings.TrimSpace(message) != "" {
		return clip([]byte(strings.TrimSpace(message)), 240)
	}
	return ""
}

func buildResponseText(body []byte) (string, string) {
	var root map[string]any
	if json.Unmarshal(body, &root) != nil {
		return "", "malformed build response"
	}
	if errValue, ok := root["error"].(map[string]any); ok {
		message, _ := errValue["message"].(string)
		return "", strings.TrimSpace(message)
	}
	if text, _ := root["output_text"].(string); strings.TrimSpace(text) != "" {
		return strings.TrimSpace(text), ""
	}
	var parts []string
	var walk func(any)
	walk = func(value any) {
		switch typed := value.(type) {
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			kind, _ := typed["type"].(string)
			if (kind == "output_text" || kind == "text") && typed["text"] != nil {
				if text, _ := typed["text"].(string); text != "" {
					parts = append(parts, text)
				}
				return
			}
			for _, key := range []string{"output", "content"} {
				if child, ok := typed[key]; ok {
					walk(child)
				}
			}
		}
	}
	walk(root["output"])
	return strings.TrimSpace(strings.Join(parts, "")), ""
}

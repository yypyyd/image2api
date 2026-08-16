package oreate

import (
	"context"
	"errors"
	"net/url"

	"backend/internal/provider/proxybridge"
)

type proxyBridge = proxybridge.Bridge

func startProxyBridge(ctx context.Context, upstream chromiumProxy) (*proxyBridge, error) {
	parsed, err := url.Parse(upstream.server)
	if err != nil || parsed.Host == "" {
		return nil, errors.New("oreate signer: invalid authenticated proxy")
	}
	parsed.User = url.UserPassword(upstream.username, upstream.password)
	return proxybridge.Start(ctx, parsed.String())
}

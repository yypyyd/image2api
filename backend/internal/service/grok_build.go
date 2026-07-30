package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"backend/internal/model"
	"backend/internal/provider/grok"

	"gorm.io/datatypes"
)

const (
	grokBuildAccessKey  = "build_access_token"
	grokBuildRefreshKey = "build_refresh_token"
	grokBuildIDKey      = "build_id_token"
	grokBuildExpiryKey  = "build_expires_at"
)

// ensureGrokBuildCredential returns a non-expiring-soon Build access token for
// a Grok Web account. Existing imports are upgraded lazily; refreshed/converted
// tokens are kept in private account metadata and never included in accountRow.
func (s *V1Service) ensureGrokBuildCredential(ctx context.Context, token model.TokenAccount) (string, error) {
	if s.grok == nil || strings.TrimSpace(token.Value) == "" {
		return "", grok.ErrAuth
	}
	lockValue, _ := s.grokBuildLocks.LoadOrStore(token.ID, &sync.Mutex{})
	lock := lockValue.(*sync.Mutex)
	lock.Lock()
	defer lock.Unlock()

	// Re-read after acquiring the lock: a request that waited here should reuse
	// the credential just persisted by the request ahead of it.
	current, err := s.tokens.Get(ctx, "grok", token.ID)
	if err != nil {
		return "", err
	}
	access := strings.TrimSpace(stringValue(current.Meta[grokBuildAccessKey]))
	refresh := strings.TrimSpace(stringValue(current.Meta[grokBuildRefreshKey]))
	expiresUnix, _ := jsonMapInt(current.Meta, grokBuildExpiryKey)
	if access != "" && expiresUnix > int(time.Now().Add(2*time.Minute).Unix()) {
		return access, nil
	}

	var credential grok.BuildCredential
	if refresh != "" {
		credential, err = s.grok.RefreshBuildCredential(ctx, refresh)
	}
	if refresh == "" || err != nil {
		credential, err = s.grok.ConvertSSOToBuild(ctx, current.Value)
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(credential.AccessToken) == "" {
		return "", fmt.Errorf("%w: oauth response missing access token", grok.ErrTemporaryUpstream)
	}

	meta := cloneJSONMap(current.Meta)
	if meta == nil {
		meta = datatypes.JSONMap{}
	}
	meta[grokBuildAccessKey] = credential.AccessToken
	meta[grokBuildRefreshKey] = credential.RefreshToken
	meta[grokBuildIDKey] = credential.IDToken
	meta[grokBuildExpiryKey] = int(credential.ExpiresAt.Unix())
	meta["build_ready"] = true
	if _, updateErr := s.tokens.Update(ctx, "grok", current.ID, map[string]any{"meta": meta}); updateErr != nil {
		return "", updateErr
	}
	return credential.AccessToken, nil
}

package grok

import (
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

var innerChunkRe = regexp.MustCompile(`static/chunks/[a-zA-Z0-9_.~\-/]+\.js`)

func hnew(ctx context.Context, url, token string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header = http.Header{
		"accept":            {"text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8"},
		"accept-language":   {"en-US,en;q=0.9"},
		"user-agent":        {userAgent},
		"cookie":            {"sso=" + token + "; sso-rw=" + token},
		http.HeaderOrderKey: {"accept", "accept-language", "user-agent", "cookie"},
	}
	return req, nil
}

func TestGrokCacheChunks(t *testing.T) {
	token := os.Getenv("DIAG_SSO")
	if token == "" {
		t.Skip("no DIAG_SSO")
	}
	dir := os.Getenv("DIAG_CACHE")
	if dir == "" {
		dir = `f:\ai-gateway\vivid-ai\backend\_chunks`
	}
	os.MkdirAll(dir, 0o755)
	logf, _ := os.Create(filepath.Join(dir, "_log.txt"))
	defer logf.Close()
	lg := func(f string, a ...any) {
		fmt.Fprintf(logf, f+"\n", a...)
		logf.Sync()
	}

	c := NewClient(os.Getenv("DIAG_PROXY"))
	ctx, cancel := context.WithTimeout(context.Background(), 3000*time.Second)
	defer cancel()
	client, err := c.newSubmitTLSClient()
	if err != nil {
		t.Fatal(err)
	}

	hreq, _ := hnew(ctx, apiBase+"/", token)
	hresp, err := client.Do(hreq)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(hresp.Body)
	hresp.Body.Close()
	html := string(body)
	os.WriteFile(filepath.Join(dir, "_home.html"), body, 0o644)

	seen := map[string]bool{}
	queue := dedupe(chunkPathRe.FindAllString(html, -1))
	lg("home chunk refs: %d", len(queue))

	saved := 0
	for len(queue) > 0 && saved < 3000 {
		p := queue[0]
		queue = queue[1:]
		norm := p
		if !strings.HasPrefix(norm, "/_next/") {
			norm = "/_next/" + strings.TrimPrefix(norm, "/")
		}
		if seen[norm] {
			continue
		}
		seen[norm] = true
		src, err := fetchChunk(ctx, client, norm)
		if err != nil {
			continue
		}
		saved++
		base := norm[strings.LastIndex(norm, "/")+1:]
		fn := fmt.Sprintf("%x_%s", sha1.Sum([]byte(norm)), base)
		if len(fn) > 120 {
			fn = fn[:120]
		}
		os.WriteFile(filepath.Join(dir, fn), []byte(src), 0o644)
		for _, m := range innerChunkRe.FindAllString(src, -1) {
			nn := "/_next/" + m
			if !seen[nn] {
				queue = append(queue, nn)
			}
		}
		if saved%50 == 0 {
			lg("saved=%d queue=%d", saved, len(queue))
		}
	}
	lg("CRAWL DONE saved=%d queue_left=%d", saved, len(queue))
	fmt.Printf("CRAWL DONE saved=%d\n", saved)
}

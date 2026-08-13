package grok

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

// The x-statsig-id recipe (49-byte header + salt suffix) encodes grok's per-load
// browser fingerprint "F". grok recomputes F server-side from the homepage
// seed+curves and the byte-indexing that derives it ROTATES on every web reship,
// so any hand-ported or baked-in recipe goes stale (403 "Request rejected by
// anti-bot rules.") within days. The durable fix is to let grok's OWN signer
// produce the recipe: we drive a headless Chrome to grok.com, read the exact
// string grok feeds to crypto.subtle.digest (reveals the current suffix) and one
// of the x-statsig-id headers its client attaches (reveals the current 49-byte
// header + trailer), then feed those into statsigID's existing per-request
// computation. Because we read grok's live output we never reverse-engineer the
// (rotating) obfuscation; a reship just needs a re-capture, which the auto-refresh
// loop does on a timer and whenever a live request hits an anti-bot 403.
//
// The captured recipe is persisted (site settings) so a restart keeps the last
// good values, and every failure is non-fatal — statsigID keeps using whatever
// recipe is currently live (persisted → last capture → static defaults).

// hookJS is installed before grok's own scripts run. It records (a) every
// crypto.subtle.digest input that contains the statsig salt marker — the plaintext
// "METHOD!/path!counter<suffix>" — and (b) every 70-byte x-statsig-id header grok
// attaches to its API calls. Both are only produced once grok's signer succeeds
// (after its keyframe animation settles), so a short wait yields valid values.
const statsigHookJS = `
(() => {
  window.__statsigDigests = []; window.__statsigCaps = [];
  try {
    const td = new TextDecoder('utf-8', {fatal:false});
    const orig = crypto.subtle.digest.bind(crypto.subtle);
    crypto.subtle.digest = function(alg, data) {
      try {
        let buf = data; if (data && data.buffer) buf = data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength);
        const txt = td.decode(new Uint8Array(buf.slice ? buf.slice(0) : buf));
        if (txt.indexOf('` + statsigSaltPrefix + `') >= 0) window.__statsigDigests.push(txt);
      } catch (e) {}
      return orig(alg, data);
    };
    const of = window.fetch;
    window.fetch = function(input, init) {
      try {
        let hdrs = (init && init.headers); let sid = null;
        if (hdrs) { if (typeof hdrs.get === 'function') sid = hdrs.get('x-statsig-id'); else for (const k in hdrs) if (k.toLowerCase() === 'x-statsig-id') sid = hdrs[k]; }
        if (sid && sid.length === 94) window.__statsigCaps.push(sid);
      } catch (e) {}
      return of.apply(this, arguments);
    };
  } catch (e) {}
})();
`

var (
	statsigRefreshTrigger = make(chan struct{}, 1)
	statsigRefreshStarted sync.Once
	statsigLastCapture    time.Time
	statsigLastCaptureMu  sync.Mutex
	statsigProxy          string
	statsigProxyMu        sync.RWMutex
)

// SetStatsigProxy applies the same site-wide egress rule to the headless Grok
// signer capture as to normal Grok API calls.
func SetStatsigProxy(proxy string) {
	statsigProxyMu.Lock()
	statsigProxy = strings.TrimSpace(proxy)
	statsigProxyMu.Unlock()
}

func currentStatsigProxy() string {
	statsigProxyMu.RLock()
	defer statsigProxyMu.RUnlock()
	return statsigProxy
}

// SetStatsigRecipe atomically replaces the live (header, suffix, trailer) used by
// statsigID and drops the per-session challenge cache so the next request adopts
// the new values. headerHex must decode to 49 bytes; suffix must be non-empty.
func SetStatsigRecipe(headerHex, suffix string, trailer int) error {
	h, err := hex.DecodeString(strings.TrimSpace(headerHex))
	if err != nil {
		return err
	}
	if len(h) != 49 {
		return errors.New("statsig: header must be 49 bytes")
	}
	if strings.TrimSpace(suffix) == "" {
		return errors.New("statsig: empty suffix")
	}
	if trailer < 0 || trailer > 255 {
		return errors.New("statsig: trailer out of range")
	}
	statsigMu.Lock()
	statsigHeader = h
	statsigSuffix = suffix
	statsigTrailer = byte(trailer)
	statsigCache = map[string]statsigChallenge{}
	statsigMu.Unlock()
	return nil
}

// chromeExecPath resolves the Chrome/Chromium binary for headless capture, from
// GROK_STATSIG_CHROME or the usual names on PATH. Empty means none available.
func chromeExecPath() string {
	if p := strings.TrimSpace(os.Getenv("GROK_STATSIG_CHROME")); p != "" {
		return p
	}
	for _, name := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

type statsigCapResult struct {
	Digs []string `json:"digs"`
	Caps []string `json:"caps"`
}

// CaptureStatsigRecipe drives a headless Chrome to grok.com and reads grok's own
// signer output, returning the current (headerHex, suffix, trailer). It never
// mutates global state. Returns an error if Chrome is unavailable or grok's
// signer output could not be observed (e.g. blocked before the signer ran).
func CaptureStatsigRecipe(ctx context.Context) (headerHex, suffix string, trailer int, err error) {
	chromePath := chromeExecPath()
	if chromePath == "" {
		return "", "", 0, errors.New("statsig: no chrome binary (set GROK_STATSIG_CHROME)")
	}
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(chromePath),
		chromedp.Flag("headless", "new"),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.UserAgent(userAgent),
	)
	if proxy := currentStatsigProxy(); proxy != "" {
		opts = append(opts, chromedp.ProxyServer(proxy))
	}
	allocCtx, cancel := chromedp.NewExecAllocator(ctx, opts...)
	defer cancel()
	bctx, cancel2 := chromedp.NewContext(allocCtx)
	defer cancel2()

	var res statsigCapResult
	runErr := chromedp.Run(bctx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			_, err := page.AddScriptToEvaluateOnNewDocument(statsigHookJS).Do(ctx)
			return err
		}),
		chromedp.Navigate(apiBase+"/"),
		chromedp.Sleep(14*time.Second),
		chromedp.Evaluate(`({digs: window.__statsigDigests || [], caps: window.__statsigCaps || []})`, &res),
	)
	if runErr != nil {
		return "", "", 0, runErr
	}
	return parseStatsigCapture(res)
}

// parseStatsigCapture extracts (headerHex, suffix, trailer) from the observed
// digest plaintexts and x-statsig-id headers of one page load. Both must be
// present and are self-consistent (same per-load fingerprint F).
func parseStatsigCapture(res statsigCapResult) (headerHex, suffix string, trailer int, err error) {
	for _, d := range res.Digs {
		if i := strings.Index(d, statsigSaltPrefix); i >= 0 {
			suffix = d[i:]
			break
		}
	}
	if suffix == "" {
		return "", "", 0, errors.New("statsig: no signer digest observed (blocked before signer ran?)")
	}
	var header []byte
	for _, sid := range res.Caps {
		raw, derr := base64.RawStdEncoding.DecodeString(sid)
		if derr != nil || len(raw) != 70 {
			continue
		}
		key := raw[0] // header[0] is 0x00, so the XOR mask key == byte 0
		r := make([]byte, 70)
		for i := range raw {
			r[i] = raw[i] ^ key
		}
		if r[0] != 0x00 {
			continue
		}
		header = r[0:49]
		trailer = int(r[69])
		break
	}
	if header == nil {
		return "", "", 0, errors.New("statsig: no valid x-statsig-id header observed")
	}
	return hex.EncodeToString(header), suffix, trailer, nil
}

// TriggerStatsigRefresh asks the auto-refresh loop to re-capture soon (coalesced).
// Called when a live request hits an anti-bot 403 — the recipe likely went stale.
func TriggerStatsigRefresh() {
	select {
	case statsigRefreshTrigger <- struct{}{}:
	default:
	}
}

// StartStatsigAutoRefresh launches the headless capture loop once. It is
// event-driven, NOT a polling loop: it seeds the live recipe from persisted
// values (load), captures once at startup so the process starts fresh, and then
// re-captures ONLY when a live request hits an anti-bot 403 (TriggerStatsigRefresh)
// — i.e. exactly when the recipe actually went stale on a grok reship. The recipe
// has no fixed clock expiry, so there is nothing useful to poll on. An optional
// safetyInterval>0 adds a slow backstop re-capture; pass 0 to disable it (default).
// Captured recipes are handed to `save` for persistence. No-op when a manual
// GROK_STATSIG_* override is set or no Chrome binary is available.
func StartStatsigAutoRefresh(ctx context.Context, safetyInterval time.Duration,
	load func(context.Context) (headerHex, suffix string, trailer int, ok bool),
	save func(ctx context.Context, headerHex, suffix string, trailer int)) {

	statsigRefreshStarted.Do(func() {
		if os.Getenv("GROK_STATSIG_HEADER_HEX") != "" || os.Getenv("GROK_STATSIG_SUFFIX") != "" {
			log.Printf("grok statsig: manual GROK_STATSIG_* override set — headless auto-refresh disabled")
			return
		}
		if chromeExecPath() == "" {
			log.Printf("grok statsig: no chrome binary — headless auto-refresh disabled, using static recipe (set GROK_STATSIG_CHROME to enable)")
			return
		}
		if load != nil {
			if h, s, t, ok := load(ctx); ok {
				if err := SetStatsigRecipe(h, s, t); err != nil {
					log.Printf("grok statsig: persisted recipe invalid, ignoring: %v", err)
				} else {
					log.Printf("grok statsig: loaded persisted recipe header[:6]=%s", safePrefix(h, 12))
				}
			}
		}
		go statsigRefreshLoop(ctx, safetyInterval, save)
	})
}

func statsigRefreshLoop(ctx context.Context, safetyInterval time.Duration, save func(context.Context, string, string, int)) {
	refresh := func(reason string) {
		statsigLastCaptureMu.Lock()
		if time.Since(statsigLastCapture) < 30*time.Second {
			statsigLastCaptureMu.Unlock()
			return // debounce bursts (e.g. a 403 storm across concurrent requests)
		}
		statsigLastCapture = time.Now()
		statsigLastCaptureMu.Unlock()

		cctx, cancel := context.WithTimeout(ctx, 90*time.Second)
		defer cancel()
		h, s, t, err := CaptureStatsigRecipe(cctx)
		if err != nil {
			log.Printf("grok statsig: headless capture (%s) failed, keeping current recipe: %v", reason, err)
			return
		}
		if err := SetStatsigRecipe(h, s, t); err != nil {
			log.Printf("grok statsig: captured recipe invalid: %v", err)
			return
		}
		if save != nil {
			save(ctx, h, s, t)
		}
		log.Printf("grok statsig: headless recipe refreshed (%s) header[:6]=%s suffix[:24]=%s", reason, safePrefix(h, 12), safePrefix(s, 24))
	}

	initial := time.NewTimer(10 * time.Second)
	defer initial.Stop()

	// Optional slow backstop only; disabled (nil channel blocks forever) when
	// safetyInterval<=0 so the loop stays purely event-driven.
	var tickC <-chan time.Time
	if safetyInterval > 0 {
		ticker := time.NewTicker(safetyInterval)
		defer ticker.Stop()
		tickC = ticker.C
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-initial.C:
			refresh("startup")
		case <-tickC:
			refresh("safety-interval")
		case <-statsigRefreshTrigger:
			refresh("anti-bot 403")
		}
	}
}

func safePrefix(s string, n int) string {
	if len(s) < n {
		return s
	}
	return s[:n]
}

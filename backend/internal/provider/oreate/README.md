# OreateAI Seedance Provider

`oreate` is the internal website-protocol client used by image2api to expose
OreateAI Seedance video models through the gateway's `/v1/videos` API.
OreateAI does not publish an OpenAI-compatible API, so this package implements
the authenticated website flow and its browser-generated Banti signature.

## Responsibilities

- Validate Oreate account cookies and read profile and point balances.
- Map the five public `oreate-seedance-*` model IDs to confirmed website model
  names, resolutions, durations, aspect ratios, audio options, and point costs.
- Upload JPEG/PNG/WebP images and MP4/MOV videos with Oreate's short-lived
  Google Storage credentials, then construct text, ordered-frame, or reference
  scenes with the same attachment metadata as the official frontend.
- Parse MP4/MOV movie headers and enforce Oreate's 2-15 second aggregate
  reference-video window without adding an `ffprobe` runtime dependency.
- Create an AI video chat, obtain a fresh Banti `jt`, submit the SSE generation
  request, parse the final artifact URL, and optionally download the MP4.
- Classify authentication, quota, content, risk-control, and temporary upstream
  failures for the shared account-pool retry policy.
- Route browser and submit traffic through the configured global proxy without
  exposing proxy credentials to Chromium or the destination website.

The package intentionally does not implement reference audio, motion controls,
or models outside the confirmed Seedance set. Account persistence, scheduling,
billing, API authentication, and event storage belong to the service and
repository layers.

## Dependencies

- Go standard-library HTTP and SSE primitives.
- `chromedp` and CDP network events for the official browser-side signer.
- A Chromium runtime selected by `OREATE_CHROME` in production.
- The gateway's global `proxy.url` setting and Oreate account pool.

The Docker runtime also supplies a dedicated unprivileged `chrome` user and a
pinned GlobalSign intermediate in both the system and Chromium NSS certificate
stores. See [DESIGN.md](DESIGN.md) for the security boundary.

## Account Lifecycle

The service layer permanently removes an Oreate account when a successful
balance response contains an integer `remaining` value below 60. A balance of
exactly 60 is retained. Missing values, malformed responses, timeouts, proxy
failures, and other inconclusive probes never trigger deletion. This policy is
applied after import validation, an administrator quota refresh, and successful
or quota-exhausted generation reconciliation.

Before dispatch, the service resolves the request's official point cost from
its `aiType` and excludes every account with a known cached balance below that
cost. This is separate from account retirement: an 80-point account remains in
the pool and can run a 30- or 60-point request, but cannot be selected for a
100-point request. Unknown cached balances remain eligible until an
authoritative balance probe fills them, preserving compatibility with legacy
rows without treating missing data as zero.

The selected account's exact request cost is atomically reserved immediately
before submit and refunded on generation failure. This closes the queueing race
where two requests could otherwise both observe the same stale balance. An
upstream quota response only changes the account to the global quota state when
balance reconciliation cannot establish that the account still has enough
points to remain useful for cheaper requests.

## Internal Usage

```go
client := oreate.NewClient(globalProxy)
account := oreate.Account{
    Cookie:    storedCookie,
    UserAgent: storedUserAgent,
}

video, result, err := client.GenerateVideo(ctx, account, oreate.VideoOptions{
    ModelID:        "seedance-2.5",
    Prompt:         "A paper boat on a calm pond",
    Ratio:          "16:9",
    Resolution:     "480p",
    Duration:       20,
    Audio:          true,
    DownloadResult: true,
})
```

Production callers use the provider through `service.V1Service`; they should
not construct accounts from untrusted request data. `GenerateVideo` returns
either downloaded bytes or a result map containing the upstream artifact URL.

## Tests

```powershell
go test ./internal/provider/oreate
```

`TestLiveSignerProbe` and `TestLiveAccountProbe` are deployment-only. They skip
unless `OREATE_LIVE_TEST=1` and read their credential JSON exclusively from
standard input. They never create a video or print credentials or token bodies.

## Files

- `client.go`: account, profile, balance, headers, and error classification.
- `models.go`: strict Seedance capability, `aiType`, and point-cost mapping.
- `media_duration.go`: bounded ISO BMFF `mvhd` duration parser.
- `upload.go`: Oreate upload-token exchange and fixed-host GCS resumable upload.
- `video.go`: chat creation, SSE generation, parsing, and artifact download.
- `signer.go`: Chromium signer, Banti report barrier, and proxy configuration.
- `proxy_bridge.go`: loopback authenticated HTTP proxy bridge for Chromium.
- `browser_isolation_*.go`: Linux child-process privilege isolation.
- `*_test.go`: deterministic protocol, proxy, model, and deployment probes.

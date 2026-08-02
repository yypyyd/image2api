# Design Notes

### 2026-07-26 - OpenAI-compatible text reverse proxy

**Change**: Added `POST /v1/chat/completions` for both the existing ChatGPT Web account pool and custom OpenAI-compatible upstream accounts. Custom upstreams support ordinary JSON and live SSE pass-through; ChatGPT Web responses are normalized into OpenAI JSON or a compatible SSE sequence after the web turn completes. Managed models now accept `type: "text"` and use `prices.request` / `prices_agent.request` as a fixed per-request charge. The admin catalog exposes ChatGPT's real model slugs `gpt-5-5-mini` and `gpt-5-5-thinking`.

**Reason**: API clients need to use the same gateway, API keys, account routing, concurrency controls, and credit balance for text models as they already use for image and video generation.

**Impact**: For custom upstreams the gateway rewrites only the upstream `model` field and preserves other Chat Completions request fields. ChatGPT Web receives a role-labelled transcript because its new-conversation endpoint does not accept the OpenAI message history shape directly. HTTP-200 business errors are rejected before billing is finalized; streaming calls must end with `data: [DONE]`. Invalid, interrupted, or incomplete responses are logged as failed and refunded. Streaming holds the selected account and user concurrency slots until the body completes or closes.

**Security boundary**: Clients can select only an enabled local text-model name; they cannot supply an upstream URL or credential. Upstream `base_url` and keys remain admin-managed account data, keys are sent only in the upstream Authorization header, and only a small allowlist of response headers is exposed downstream. Request bodies are capped at 10 MiB and non-stream responses at 32 MiB. This defends against credential disclosure, user-controlled SSRF, and unbounded buffering. As with the existing custom image/video connector, administrators are trusted not to configure an internal or malicious upstream URL; network-level egress filtering remains the deployment operator's responsibility.

## Account credential import

The account-management import parser accepts pasted credentials as well as CPA (`type: codex`) JSON, CPA multi-account ZIP archives, Sub2API account bundles, and grok2api `sso*` pool exports. These formats are normalized in the browser to provider-specific `{ type, value }` items, so persistence, duplicate-account updates, and asynchronous quota checks continue to use the established provider import endpoints.

ZIP processing is memory-only and never extracts paths to disk. It accepts JSON entries only, caps the archive at 1,000 JSON files, each uncompressed JSON entry at 2 MiB, and total uncompressed JSON data at 20 MiB. Duplicate credentials are removed before requests are sent. Agent Identity-only records are skipped because the image provider requires a ChatGPT `access_token`; when a file contains no supported credential, the UI reports that limitation explicitly.

## Change history

### 2026-07-30 - URL-first image relay

**Change**: `/v1/images/generations` and `/v1/images/edits` now default to `data[].url`. Ordinary API requests leave provider media upstream and skip gateway downloads, base64 encoding, and object-storage writes. Callers may still explicitly request `response_format=b64_json`.

**Reason**: The gateway is being optimized as a lightweight 2api relay. Returning large base64 payloads by default multiplies memory use and bandwidth under high concurrency without adding routing value.

**Impact**: Clients that relied on the omitted `response_format` producing `b64_json` must either consume `data[].url` or explicitly request `b64_json`. Auth-gated ChatGPT/Grok assets continue to use the gateway's streaming `/content` URL. Requests carrying `Idempotency-Key` retain private persistence for timeout recovery.

**Decision**: Make the low-copy URL path the default while retaining an explicit compatibility escape hatch; do not expose authenticated upstream URLs that downstream clients cannot fetch.

### 2026-07-30 - Recover image results after gateway timeouts

**Change**: API image requests may supply `Idempotency-Key`. Those requests store the completed image in private object storage and expose `GET /v1/images/tasks?request_id=...` for `in_progress`, `completed`, or `failed` recovery after a synchronous gateway timeout.

**Reason**: A CDN can return `524` while the detached backend generation continues. Without a lookup contract, downstream clients mark a real in-flight job failed and may submit a duplicate generation.

**Impact**: Requests without an idempotency key keep the existing no-store behavior. Recovery-enabled requests add one indexed event field and one private stored image; the original synchronous OpenAI response remains unchanged.

**Security boundary**: Task lookup authenticates the API key, scopes the query to that key owner's user ID, image kind, and API source, and uses a parameterized database query. Request IDs are limited to 191 characters, and recovered image reads are capped at 32 MiB. A caller cannot use a known request ID to retrieve another user's task or media.

**Decision**: Reuse the downstream idempotency key as the recovery identifier instead of exposing provider job IDs. This keeps provider details private and lets existing clients recover only the request they originally submitted.

### 2026-07-30 - Grok Web SSO to Build OAuth

**Change**: Added an unattended xAI Device OAuth conversion for existing Grok Web SSO accounts, renewable Build credentials stored in private account metadata, and a native `cli-chat-proxy.grok.com/v1/responses` text client for literal models such as `grok-4.5`.

**Reason**: Grok Web uses mode-based `fast/auto/expert/heavy` requests and cannot select `grok-4.5` literally. The upstream grok2api implementation proves that a valid Web SSO session can authorize a separate Build OAuth credential without asking the operator to export CPA credentials.

**Impact**: Existing Grok accounts are upgraded lazily on their first Build-model request. Web image/video and `grok-chat-*` behavior is unchanged. Access, refresh, and ID tokens are never included in account API responses. Refresh tokens are rotated before expiry; if refresh fails, the service retries the SSO conversion.

**Decision**: Keep one visible Grok account row and store its linked Build credential in private metadata instead of introducing a second account pool. This preserves current scheduling, per-account pinning, imports, and administration while routing literal Build models separately from Web modes.

### 2026-07-30 - Grok-only full-path proxy egress

**Change**: Added the `GROK_PROXY_URL` environment fallback and applied a configured Grok proxy to session/quota probes, Statsig discovery, media upload, generation submits, polling, and artifact downloads.

**Reason**: On networks where `grok.com` is blocked or DNS-poisoned, proxying only the generation submit still leaves account validation, anti-bot refresh, and result downloads unable to connect.

**Impact**: Grok traffic uses the dedicated proxy whenever configured. Other providers retain their existing egress behavior, and Grok continues to connect directly when the setting is empty.

**Decision**: Prefer a provider-specific environment variable over changing the shared `proxy.url`, preventing a Grok connectivity workaround from altering Adobe, Runway, or other provider routes.

### 2026-07-30 - grok2api account-pool imports

**Change**: Added structured parsing for grok2api JSON exports whose Grok SSO tokens are stored under `sso*` account pools.

**Reason**: The existing importer recognized pasted Grok SSO JWTs but did not inspect the `ssoBasic[].token` wrapper used by grok2api exports.

**Impact**: Frontend import parsing and account-import guidance only; backend routes and database schema are unchanged.

**Decision**: Accept future `sso*` pool names while validating every entry by the Grok-specific `session_id` JWT claim before classifying it as a Grok credential.

### 2026-07-29 - Strict OpenAI model discovery by default

**Change**: `GET /v1/models` now returns only the four standard OpenAI model fields (`id`, `object`, `created`, and `owned_by`) by default. The existing gateway-specific capability fields remain available through `GET /v1/models?extended=true`.

**Reason**: Some downstream importers validate model objects with a strict OpenAI schema and reject the entire list when an otherwise valid model contains extension fields.

**Impact**: Standard SDKs and strict model importers receive a minimal compatible response. Clients that use `kind`, supported ratios, resolutions, video durations, or reference-image capabilities (`max_reference_images` and `reference_mode`) must opt into the extended response.

**Decision**: Compatibility is the default contract on the OpenAI endpoint; gateway-specific discovery remains explicit and backward-accessible instead of being removed.

All model-discovery surfaces canonicalize numeric aspect ratios to `W:H` (for example, `16x9` becomes `16:9`). Generation inputs continue to accept either separator and normalize internally, so this output-only consistency rule does not alter provider payload behavior or persisted model data.

### 2026-07-26 - Restore the OpenAI image response contract

**Change**: `/v1/images/generations` and `/v1/images/edits` now default to `data[].b64_json`, honor an explicit `response_format` of `b64_json` or `url`, and pass `quality` into the existing resolution mapping.

**Reason**: The API documentation promised base64 responses, but a URL-only optimization caused successful generations to return a small URL JSON body. Downstream clients expecting the documented OpenAI-compatible base64 field treated those successes as missing images.

**Impact**: API-key image requests download the generated asset before responding when base64 is requested/defaulted, increasing response size and adding the asset-download time. Playground/admin generation and persisted media are unchanged.

**Decision**: Preserve `url` as an explicit bandwidth-saving option while making the documented `b64_json` response the default. Invalid response formats fail before charging.

### 2026-07-25 - CPA and Sub2API account imports

**Change**: Added JSON/ZIP file selection and structured parsing for CPA and Sub2API exports in account management, plus parser tests and format documentation.

**Reason**: Accounts exported by the registration service should be importable without manually extracting and pasting each JWT.

**Impact**: Frontend import parsing and account-import UI only; backend routes and database schema are unchanged.

**Decision**: Normalize into the current ChatGPT import flow so existing deduplication and background validation remain the single source of truth.

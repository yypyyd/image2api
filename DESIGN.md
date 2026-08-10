# Design Notes

### 2026-08-08 - API-key image URL passthrough

**Change**: API-key image requests using `response_format=url` now return the provider artifact URL directly when the provider supplies one, even when an idempotency key also causes a private recovery copy to be stored. The session-gated `/images/...` URL is only a fallback when no provider URL exists; ChatGPT/Grok account-gated assets continue using the authenticated `/v1/images/{id}/content` proxy.

**Reason**: `/images/<owner>/<file>` is protected by a browser session cookie and cannot be fetched by ordinary downstream API clients. Returning it as an API-key URL made a successful generation appear broken with “需要登录后访问”.

**Impact**: Existing private gallery access and idempotency recovery remain unchanged. Downstream clients receive the upstream URL for ordinary public provider assets; upstream URLs that expire or require provider login remain subject to provider-side availability and use the dedicated proxy path when supported.

### 2026-08-07 - Adobe points-account concurrency safety

**Change**: Adobe points accounts now default to four simultaneous generations per account. Existing points accounts are normalized to an explicit concurrency of four during deployment; newly imported accounts inherit the same effective limit when no override is stored.

**Reason**: High same-account concurrency increases provider throttling and account-risk exposure. A bounded per-account limit of four balances throughput and account stability; pool-level throughput still scales by adding accounts.

**Impact**: Concurrent Adobe jobs are distributed across distinct eligible accounts or queued until an account slot is released. Other provider pools and user concurrency groups are unchanged.

### 2026-08-08 - Face-only thin red-silk reference veil

**Change**: Seedance 2 and Seedance 2 Fast reference images now apply a light, medium-weight red mesh with extreme density only inside an inward-trimmed Pigo face rectangle. The previous hair-inclusive expansion and dense near-black mesh were removed; no 3x3 grid, external face, or heavy strands are added.

**Reason**: The previous face veil was too sparse in its reduced version and black lines were harder to distinguish from dark hair. Thin red strands improve visibility while preserving the face-only, non-solid treatment.

**Impact**: Hair, shoulders, clothing, and background remain unchanged. Images without a reliable face remain unchanged, and other models still receive original reference bytes.

### 2026-08-06 - Optional reference-image face swapping

**Change**: Seedance 2 and Seedance 2 Fast reference images run a local Pigo face transform on the gateway for `/generate`, `/v1/images/edits`, and `/v1/videos` when an individual image contains a reliable face. This entry records the earlier experimental 3x3 grid version; it was superseded by the face-only veil above.

**Reason**: Pigo provides a small pure-Go face detector, allowing the gateway to break the original face identity before an upstream reference-image check while keeping the target composition.

**Impact**: Only images with a reliable detected face are transformed; all other references pass through unchanged. Processing is bounded by the existing 20 MiB reference limit and a 100-megapixel decode guard; invalid or oversized transformed images fail before charging.

### 2026-08-06 - Dedicated full-path ChatGPT egress

**Change**: `CHATGPT_PROXY_URL` now pins every ChatGPT Web phase—bootstrap, quota, requirements, reference uploads, prepare/submit, polling, URL resolution, and protected asset downloads—to a provider-specific proxy. An HTML/edge 403, and any 403 on the unauthenticated bootstrap page, is classified as a temporary upstream failure rather than account authentication failure.

**Reason**: The Hong Kong application host receives a Cloudflare HTML 403 from ChatGPT while the former host remains a viable egress. The previous split path proxied only generation submit, and interpreted the account-neutral bootstrap 403 as invalid credentials; one failover loop consequently disabled the entire ChatGPT pool.

**Impact**: User authentication, API keys, billing, logs, account scheduling, and all non-ChatGPT providers remain on the primary application/database. Only encrypted ChatGPT upstream traffic exits through the dedicated host. A true 401 or authenticated non-HTML 403 still disables the affected credential.

**Security boundary**: The egress proxy must require authentication and/or allowlist only the application server IP. Its URL is deployment configuration, never returned by APIs or written to event logs. The proxy sees CONNECT destinations but ChatGPT payloads and bearer tokens remain inside end-to-end TLS.

**Known risk**: The dedicated egress host is now a single point of failure for ChatGPT only. Its outage produces temporary upstream errors without poisoning account health; Adobe and other providers are unaffected.

### 2026-08-05 - Retry randomly unsafe Adobe image outputs

**Change**: Adobe moderation responses now retain their upstream error code. Image generation retries `image_unsafe` on the same account up to two additional times (three total attempts), rebuilding the payload with a unique seed each time. `prompt_unsafe`, `reference_image_privacy_error`, and generic `legal_error` responses are not retried.

**Reason**: Identical short prompts can succeed and fail across calls because Adobe evaluates both the request and each randomly generated output. Treating the first unsafe output as a permanently blocked prompt caused downstream clients to see avoidable `content_policy_violation` failures.

**Impact**: Recoverable output moderation failures are absorbed inside one provider call and one gateway billing event, so they neither charge downstream users multiple times nor trigger account failover/health penalties. Three consecutive unsafe outputs still return a content-policy error with guidance to specify an adult subject, clothing, and scene.

**Decision**: Retry only the explicit `image_unsafe` code. Prompt and reference privacy refusals remain deterministic request errors, while region/legal errors remain upstream failures.

### 2026-08-04 - Seedance shared reference limit and Adobe privacy refusals

**Change**: Seedance 2 and Seedance 2 Fast now advertise 9 reference images, 3 reference videos, and 3 reference audios, with `max_reference_media: 9` enforcing Adobe's shared `referenceBlobs` limit. Their images use the upstream `style` asset mode and all validated image blob IDs are forwarded. Adobe's `reference_image_privacy_error` is classified as a request-level content rejection with a user-facing Chinese explanation.

**Reason**: Adobe's resolved discovery schema declares both per-media limits and a lower shared total. Advertising only the old 2/1/1 limits hid supported inputs, while advertising 9/3/3 without the shared cap would incorrectly imply that 15 files can be combined. Privacy refusals are caused by a real face in the reference image, not by account health.

**Impact**: Model discovery, admin catalog/model views, the playground, and admin test modal expose and enforce the shared total before charging. Content/privacy refusals fail immediately without account failover or failure penalties; generic Adobe 451 legal errors retain their existing upstream-failure classification.

**Decision**: Keep per-type and aggregate limits as separate model fields. A zero aggregate limit means no additional shared cap, preserving existing custom and non-Seedance model behavior.

### 2026-08-04 - Typed video reference media and generated audio

**Change**: Video model capabilities now independently declare image, video, and audio reference limits plus generated-audio support. Session generation, admin tests, and `POST /v1/videos` accept multipart `reference_images`, `reference_videos`, and `reference_audios` fields and a `generate_audio` flag. Adobe media uploads use the matching `/v2/storage/{image|video|audio}` endpoint, and model-specific payloads map those blob IDs into Seedance multimodal, Kling/Luma video-modify, Firefly structure-reference, and Veo/Kling/Seedance audio-output requests.

**Reason**: Treating every reference as a Base64 image made Adobe's video/audio inputs unusable and inflated large media by roughly one third. It also hid the upstream audio-output capability from clients.

**Impact**: Multipart requests are buffered into owned byte slices before asynchronous jobs start; per-file limits are 20 MiB for images, 200 MiB for video, and 50 MiB for audio. MP4/MOV and a conservative audio MIME allowlist are enforced before charging. The reverse-proxy and request upload limit increases to 320 MiB. Existing JSON image-reference requests remain compatible; JSON video/audio references are accepted as raw Base64 or data URIs.

**Decision**: Capabilities are model data and validation is fail-closed. Models whose video, audio, or generated-audio support is not confirmed retain zero/false fields and reject those inputs before debit. Known built-in Adobe rows are backfilled during startup migration.

**Security boundary**: Media endpoints require the existing session or API-key authentication. The gateway bounds the whole request and every file before provider selection, ignores client filenames for filesystem access, and sends accepted bytes only to the configured Adobe storage endpoint. Model capability checks and MIME allowlists run before debit and upload.

**Known risk**: MIME headers and filename extensions are format hints, not a full media decoder; Adobe remains responsible for parsing the actual container. A request near the 320 MiB ceiling is held in memory because asynchronous jobs cannot retain temporary multipart handles, so deployments should keep the existing user-concurrency controls and add edge rate limits when exposing large uploads publicly.

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

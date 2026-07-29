# Design Notes

### 2026-07-26 - OpenAI-compatible text reverse proxy

**Change**: Added `POST /v1/chat/completions` for both the existing ChatGPT Web account pool and custom OpenAI-compatible upstream accounts. Custom upstreams support ordinary JSON and live SSE pass-through; ChatGPT Web responses are normalized into OpenAI JSON or a compatible SSE sequence after the web turn completes. Managed models now accept `type: "text"` and use `prices.request` / `prices_agent.request` as a fixed per-request charge. The admin catalog exposes ChatGPT's real model slugs `gpt-5-5-mini` and `gpt-5-5-thinking`.

**Reason**: API clients need to use the same gateway, API keys, account routing, concurrency controls, and credit balance for text models as they already use for image and video generation.

**Impact**: For custom upstreams the gateway rewrites only the upstream `model` field and preserves other Chat Completions request fields. ChatGPT Web receives a role-labelled transcript because its new-conversation endpoint does not accept the OpenAI message history shape directly. HTTP-200 business errors are rejected before billing is finalized; streaming calls must end with `data: [DONE]`. Invalid, interrupted, or incomplete responses are logged as failed and refunded. Streaming holds the selected account and user concurrency slots until the body completes or closes.

**Security boundary**: Clients can select only an enabled local text-model name; they cannot supply an upstream URL or credential. Upstream `base_url` and keys remain admin-managed account data, keys are sent only in the upstream Authorization header, and only a small allowlist of response headers is exposed downstream. Request bodies are capped at 10 MiB and non-stream responses at 32 MiB. This defends against credential disclosure, user-controlled SSRF, and unbounded buffering. As with the existing custom image/video connector, administrators are trusted not to configure an internal or malicious upstream URL; network-level egress filtering remains the deployment operator's responsibility.

## Account credential import

The account-management import parser accepts pasted credentials as well as CPA (`type: codex`) JSON, CPA multi-account ZIP archives, and Sub2API account bundles. These formats are normalized in the browser to the existing `{ type: "openai", value: accessToken }` representation, so persistence, duplicate-account updates, and asynchronous quota checks continue to use the established ChatGPT token import endpoint.

ZIP processing is memory-only and never extracts paths to disk. It accepts JSON entries only, caps the archive at 1,000 JSON files, each uncompressed JSON entry at 2 MiB, and total uncompressed JSON data at 20 MiB. Duplicate credentials are removed before requests are sent. Agent Identity-only records are skipped because the image provider requires a ChatGPT `access_token`; when a file contains no usable access token, the UI reports that limitation explicitly.

## Change history

### 2026-07-29 - Strict OpenAI model discovery by default

**Change**: `GET /v1/models` now returns only the four standard OpenAI model fields (`id`, `object`, `created`, and `owned_by`) by default. The existing gateway-specific capability fields remain available through `GET /v1/models?extended=true`.

**Reason**: Some downstream importers validate model objects with a strict OpenAI schema and reject the entire list when an otherwise valid model contains extension fields.

**Impact**: Standard SDKs and strict model importers receive a minimal compatible response. Clients that use `kind`, supported ratios, resolutions, or video durations must opt into the extended response.

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

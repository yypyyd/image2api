# Design Notes

## Account credential import

The account-management import parser accepts pasted credentials as well as CPA (`type: codex`) JSON, CPA multi-account ZIP archives, and Sub2API account bundles. These formats are normalized in the browser to the existing `{ type: "openai", value: accessToken }` representation, so persistence, duplicate-account updates, and asynchronous quota checks continue to use the established ChatGPT token import endpoint.

ZIP processing is memory-only and never extracts paths to disk. It accepts JSON entries only, caps the archive at 1,000 JSON files, each uncompressed JSON entry at 2 MiB, and total uncompressed JSON data at 20 MiB. Duplicate credentials are removed before requests are sent. Agent Identity-only records are skipped because the image provider requires a ChatGPT `access_token`; when a file contains no usable access token, the UI reports that limitation explicitly.

## Change history

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

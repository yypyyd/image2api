package imagine

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"strconv"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

// GenerateImage runs the full Imagine.art pipeline: submit the txt2img job, poll
// the org objects feed until the batch finishes, then download the produced
// image. styleID picks the model (41001 = 1.5 / 2K, 41004 = 1.5pro / 4K).
// HTTP 402 → ErrQuotaExhausted. These models are pure text2img (no refs).
func (c *Client) GenerateImage(ctx context.Context, cred string, styleID int, resolution, aspectRatio, prompt string, downloadResult bool) ([]byte, map[string]any, error) {
	cr, ok := parseCred(cred)
	if !ok {
		return nil, nil, ErrAuth
	}
	userID := userIDFromToken(cr.Token)

	metadata, _ := json.Marshal(map[string]any{
		"placeholderUuid":           uuid4(),
		"promptWithoutManipulation": prompt,
		"modeId":                    0,
	})
	// parent_id MUST be a canvas node this account owns — the server rejects a
	// foreign id ("user does not have access to parent asset") and silently
	// orphans a parent-less generation (charged but never produced). It's supplied
	// at import (credential.parentId).
	fields := map[string]string{
		"style_id":      strconv.Itoa(styleID),
		"aspect_ratio":  aspectRatio,
		"resolution":    resolution,
		"variation":     "txt2img",
		"prompt":        prompt,
		"is_enhance":    "0",
		"count":         "1",
		"clientVersion": "1",
		"org_id":        userID,
		"use_plugin":    "false",
		"metadata":      string(metadata),
	}
	if pid := strings.TrimSpace(cr.ParentID); pid != "" {
		fields["parent_id"] = pid
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		_ = w.WriteField(k, v)
	}
	_ = w.Close()

	body, status, err := c.submitGeneration(ctx, cr.Token, apiBase+"/v1/image/generations/upload", w.FormDataContentType(), buf.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("%w: submit: %s", ErrTemporaryUpstream, err.Error())
	}
	if status == 401 || status == 403 {
		return nil, nil, ErrAuth
	}
	if status == 402 {
		return nil, nil, ErrQuotaExhausted
	}
	if status != 200 && status != 201 {
		return nil, nil, fmt.Errorf("%w: submit http %d: %s", ErrTemporaryUpstream, status, clip(body, 200))
	}
	var jobs []struct {
		BatchID string `json:"batchId"`
		ID      string `json:"id"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(body, &jobs); err != nil || len(jobs) == 0 || jobs[0].BatchID == "" {
		return nil, nil, fmt.Errorf("%w: no batch id: %s", ErrTemporaryUpstream, clip(body, 200))
	}
	batchID := jobs[0].BatchID

	imageURL, err := c.pollImage(ctx, cr.Token, userID, batchID)
	if err != nil {
		return nil, nil, err
	}
	meta := map[string]any{"batch_id": batchID, "image_url": imageURL, "org_id": userID}
	if !downloadResult {
		return nil, meta, nil
	}
	data, err := c.download(ctx, imageURL)
	if err != nil {
		return nil, nil, err
	}
	return data, meta, nil
}

// pollImage polls the org objects feed until the entry for our batch finishes,
// then extracts its asset URL (image_url is a JSON-encoded array string).
func (c *Client) pollImage(ctx context.Context, token, userID, batchID string) (string, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	// Poll for the full generation budget (caller's genCtx), leaving headroom for
	// the download, instead of a shorter hardcoded cap that killed slow jobs early.
	deadline := time.Now().Add(4 * time.Minute)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl.Add(-60 * time.Second)
	}

	url := teamsBase + "/v1/org/" + userID + "/objects?batch=true&limit=50&service=image,chat-image"
	for {
		body, status, err := c.apiGetP(ctx, token, url, false)
		if err == nil && status == 200 {
			var resp struct {
				Data []struct {
					BatchID string `json:"batch_id"`
					Status  string `json:"status"`
					Code    int    `json:"code"`
					// Current shape: the produced asset lives at url.generation[0].
					URL struct {
						Generation []string `json:"generation"`
					} `json:"url"`
					ImageURL string `json:"image_url"` // legacy fallback
				} `json:"data"`
			}
			if json.Unmarshal(body, &resp) == nil {
				for _, o := range resp.Data {
					if o.BatchID != batchID {
						continue
					}
					st := strings.ToLower(strings.TrimSpace(o.Status))
					switch {
					case st == "finished" || o.Code == 2:
						if u := firstNonEmpty(o.URL.Generation); u != "" {
							return u, nil
						}
						if u := firstImageURL(o.ImageURL); u != "" {
							return u, nil
						}
					case st == "failed" || st == "error":
						return "", fmt.Errorf("%w: job %s", ErrTemporaryUpstream, st)
					}
				}
			}
		} else if status == 401 || status == 403 {
			return "", ErrAuth
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("%w: generation timed out", ErrTemporaryUpstream)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-ticker.C:
		}
	}
}

// firstNonEmpty returns the first non-blank string in a slice.
func firstNonEmpty(ss []string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// firstImageURL parses the image_url field — a JSON-encoded array of URLs — and
// returns the first one. Tolerates a bare string too.
func firstImageURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var urls []string
	if json.Unmarshal([]byte(raw), &urls) == nil {
		for _, u := range urls {
			if strings.TrimSpace(u) != "" {
				return strings.TrimSpace(u)
			}
		}
		return ""
	}
	if strings.HasPrefix(raw, "http") {
		return raw
	}
	return ""
}

func (c *Client) download(ctx context.Context, url string) ([]byte, error) {
	client, err := c.newDirectTLSClient()
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req = req.WithContext(ctx)
	req.Header = http.Header{
		"accept":     {"image/avif,image/webp,image/png,image/*,*/*;q=0.8"},
		"user-agent": {userAgent},
		"referer":    {webOrigin + "/"},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%w: image download http %d", ErrTemporaryUpstream, resp.StatusCode)
	}
	return b, nil
}

// submitGeneration issues the one POST that creates a generation job.
func (c *Client) submitGeneration(ctx context.Context, token, url, contentType string, body []byte) ([]byte, int, error) {
	client, err := c.newSubmitTLSClient()
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req = req.WithContext(ctx)
	req.Header = http.Header{
		"accept":        {"application/json, text/plain, */*"},
		"authorization": {"Bearer " + token},
		"content-type":  {contentType},
		"origin":        {webOrigin},
		"referer":       {webOrigin + "/"},
		"user-agent":    {userAgent},
		http.HeaderOrderKey: {
			"accept", "authorization", "content-type", "origin", "referer", "user-agent",
		},
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return b, resp.StatusCode, err
}

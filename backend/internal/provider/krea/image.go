package krea

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
)

const (
	genModel    = "flux2-klein4b" // the single exposed Krea model
	genEndpoint = "/api/jobs/v2/new/fluxKlein4b"
	refStrength = 0.4
)

// ensureProject returns a flux project id for the account: the first existing
// project, or a freshly created one. Generation requires a project.
func (c *Client) ensureProject(ctx context.Context, cookie string) (string, error) {
	body, status, err := c.apiGetP(ctx, cookie, "/api/flux-projects", false)
	if err != nil {
		return "", fmt.Errorf("%w: list projects: %s", ErrTemporaryUpstream, err.Error())
	}
	if status == 401 || status == 403 {
		return "", ErrAuth
	}
	if status == 200 {
		var projs []struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(body, &projs) == nil {
			for _, p := range projs {
				if strings.TrimSpace(p.ID) != "" {
					return p.ID, nil
				}
			}
		}
	}
	// No project on this account (e.g. brand-new). Try to create one; if that
	// fails, fall back to generating WITHOUT a project (Krea assigns a default).
	cb, cs, cerr := c.apiPostJSON(ctx, cookie, "/api/flux-projects", map[string]any{"title": "vivid"})
	if cerr == nil && (cs == 200 || cs == 201) {
		var pr struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(cb, &pr) == nil && pr.ID != "" {
			return pr.ID, nil
		}
	}
	return "", nil // generate without an explicit project
}

// uploadImage uploads a reference image (i2i) and returns its app-uploads URL.
func (c *Client) uploadImage(ctx context.Context, cookie string, img []byte) (string, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "image.png")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(img); err != nil {
		return "", err
	}
	_ = w.Close()
	body, status, err := c.apiPostP(ctx, cookie, "/api/upload?", w.FormDataContentType(), buf.Bytes(), false)
	if err != nil {
		return "", fmt.Errorf("%w: upload: %s", ErrTemporaryUpstream, err.Error())
	}
	if status == 401 || status == 403 {
		return "", ErrAuth
	}
	if status != 200 {
		return "", fmt.Errorf("%w: upload http %d: %s", ErrTemporaryUpstream, status, clip(body, 160))
	}
	var ur struct {
		ImageURL string `json:"imageUrl"`
	}
	if json.Unmarshal(body, &ur) != nil || ur.ImageURL == "" {
		return "", fmt.Errorf("%w: no imageUrl", ErrTemporaryUpstream)
	}
	return ur.ImageURL, nil
}

// GenerateImage runs the full Krea image pipeline: ensure a project, (for i2i)
// upload reference images, submit the job, poll until done, then resolve and
// download the produced image. 402 INSUFFICIENT_BALANCE → ErrQuotaExhausted.
func (c *Client) GenerateImage(ctx context.Context, cookie, prompt string, width, height int, refImages [][]byte, downloadResult bool) ([]byte, map[string]any, error) {
	// Ensure the daily free balance is granted (load /app) before generating, so a
	// not-yet-activated account doesn't 402 INSUFFICIENT_BALANCE. Lock-guarded and
	// once-per-daily-reset — concurrent gens wait for the first activation, already
	// activated ones skip straight through.
	c.Activate(ctx, cookie)

	projectID, err := c.ensureProject(ctx, cookie)
	if err != nil {
		return nil, nil, err
	}

	var styleImages []map[string]any
	for _, img := range refImages {
		if len(img) == 0 {
			continue
		}
		url, upErr := c.uploadImage(ctx, cookie, img)
		if upErr != nil {
			return nil, nil, upErr
		}
		styleImages = append(styleImages, map[string]any{"url": url, "strength": refStrength, "source": "upload"})
	}

	payload := map[string]any{
		"provider":            genModel,
		"prompt":              prompt,
		"width":               width,
		"height":              height,
		"strength":            1,
		"steps":               28,
		"guidance_scale_flux": 3.5,
		"presetStyles":        []any{},
		"batchSize":           2,
		"guidance":            3.5,
	}
	if projectID != "" {
		payload["project"] = projectID
	}
	if len(styleImages) > 0 {
		payload["styleImages"] = styleImages
	}
	payloadJSON, _ := json.Marshal(payload)

	// Submit (multipart with a single "payload" field).
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	_ = w.WriteField("payload", string(payloadJSON))
	_ = w.Close()
	body, status, err := c.apiPost(ctx, cookie, genEndpoint, w.FormDataContentType(), buf.Bytes())
	if err != nil {
		return nil, nil, fmt.Errorf("%w: submit: %s", ErrTemporaryUpstream, err.Error())
	}
	if status == 401 || status == 403 {
		return nil, nil, ErrAuth
	}
	if status == 402 || strings.Contains(string(body), "INSUFFICIENT_BALANCE") {
		return nil, nil, ErrQuotaExhausted
	}
	if status != 200 && status != 201 {
		return nil, nil, fmt.Errorf("%w: submit http %d: %s", ErrTemporaryUpstream, status, clip(body, 200))
	}
	var jobs []struct {
		JobID string `json:"job_id"`
	}
	if err := json.Unmarshal(body, &jobs); err != nil || len(jobs) == 0 || jobs[0].JobID == "" {
		return nil, nil, fmt.Errorf("%w: no job id: %s", ErrTemporaryUpstream, clip(body, 200))
	}
	// batchSize=2 returns two jobs; keep the SECOND image and discard the first
	// (per spec). Fall back to the first if only one came back.
	jobID := jobs[0].JobID
	if len(jobs) >= 2 && jobs[1].JobID != "" {
		jobID = jobs[1].JobID
	}

	// Poll until terminal, then resolve the produced image.
	imageURL, err := c.pollImage(ctx, cookie, jobID)
	if err != nil {
		return nil, nil, err
	}
	meta := map[string]any{"job_id": jobID, "image_url": imageURL, "project": projectID}
	if !downloadResult {
		return nil, meta, nil
	}
	data, err := c.download(ctx, imageURL)
	if err != nil {
		return nil, nil, err
	}
	return data, meta, nil
}

// pollImage polls job-status until the job leaves the queue, then matches the
// produced asset by generation_job_id and returns its image URL.
func (c *Client) pollImage(ctx context.Context, cookie, jobID string) (string, error) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	// Poll for the full generation budget (caller's genCtx), leaving headroom for
	// the download, instead of a shorter hardcoded cap that killed slow jobs early.
	deadline := time.Now().Add(4 * time.Minute)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl.Add(-60 * time.Second)
	}

	for {
		body, status, err := c.apiGetP(ctx, cookie, "/api/job-status?id="+jobID, false)
		if err == nil && status == 200 {
			var js struct {
				Status string `json:"status"`
			}
			if json.Unmarshal(body, &js) == nil {
				switch strings.ToLower(js.Status) {
				case "complete", "completed", "succeeded", "success", "done", "finished":
					if url, e := c.assetForJob(ctx, cookie, jobID); e == nil && url != "" {
						return url, nil
					}
				case "failed", "error", "cancelled", "canceled":
					return "", fmt.Errorf("%w: job %s", ErrTemporaryUpstream, js.Status)
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

// assetForJob finds the generated asset produced by a job and returns its URL.
func (c *Client) assetForJob(ctx context.Context, cookie, jobID string) (string, error) {
	body, status, err := c.apiGetP(ctx, cookie, "/api/assets?filter=generated&offset=0", false)
	if err != nil || status != 200 {
		return "", fmt.Errorf("assets http %d", status)
	}
	var assets []struct {
		ImageURL string `json:"image_url"`
		Metadata struct {
			GenerationJobID string `json:"generation_job_id"`
		} `json:"metadata"`
	}
	if json.Unmarshal(body, &assets) != nil {
		return "", fmt.Errorf("assets non-json")
	}
	for _, a := range assets {
		if a.Metadata.GenerationJobID == jobID && strings.TrimSpace(a.ImageURL) != "" {
			return a.ImageURL, nil
		}
	}
	return "", fmt.Errorf("asset not found yet")
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
		"referer":    {apiBase + "/"},
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

// apiPost issues a POST with a raw body + content-type, carrying the cookie.
func (c *Client) apiPost(ctx context.Context, cookie, path, contentType string, body []byte) ([]byte, int, error) {
	return c.apiPostP(ctx, cookie, path, contentType, body, true)
}

// apiPostP keeps the historical useProxy argument for call-site clarity; every
// request follows the global proxy when configured.
func (c *Client) apiPostP(ctx context.Context, cookie, path, contentType string, body []byte, useProxy bool) ([]byte, int, error) {
	client, err := c.newTLSClientP(useProxy)
	if err != nil {
		return nil, 0, err
	}
	req, err := http.NewRequest(http.MethodPost, apiBase+path, bytes.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	req = req.WithContext(ctx)
	req.Header = http.Header{
		"accept":          {"*/*"},
		"accept-language": {"en-US,en;q=0.9"},
		"content-type":    {contentType},
		"cookie":          {cookie},
		"origin":          {apiBase},
		"referer":         {apiBase + "/"},
		"user-agent":      {userAgent},
		"sec-fetch-dest":  {"empty"},
		"sec-fetch-mode":  {"cors"},
		"sec-fetch-site":  {"same-origin"},
		http.HeaderOrderKey: {
			"accept", "accept-language", "content-type", "cookie", "origin",
			"referer", "user-agent", "sec-fetch-dest", "sec-fetch-mode", "sec-fetch-site",
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

func (c *Client) apiPostJSON(ctx context.Context, cookie, path string, payload any) ([]byte, int, error) {
	// Project bootstrap and generation both follow the global proxy setting.
	b, _ := json.Marshal(payload)
	return c.apiPostP(ctx, cookie, path, "application/json", b, false)
}

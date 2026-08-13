package leonardo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// qGenerationVideos polls one generation's status AND its produced motion clips
// in a single round-trip (where: id _in [genId]). Video outputs land on the same
// generated_images rows with the mp4 in motionMP4URL.
const qGenerationVideos = `query GenerationVideos($where: generations_bool_exp = {}) {
  generations(where: $where) {
    id
    status
    generated_images {
      id
      url
      motionMP4URL
      __typename
    }
    __typename
  }
}`

// GenerateVideo runs the Leonardo text-to-video pipeline against one account
// cookie: mint a JWT, submit the Generate mutation with a video model, poll
// until COMPLETE, then download the produced mp4. Returns the video bytes, an
// info map, and a classified error. durationSeconds <= 0 uses the upstream
// default clip length for the model.
func (c *Client) GenerateVideo(ctx context.Context, cookie, model, prompt string, width, height, durationSeconds int, downloadResult bool) ([]byte, map[string]any, error) {
	sess, err := c.GetSession(ctx, cookie)
	if err != nil {
		return nil, nil, err
	}
	if strings.TrimSpace(model) == "" {
		return nil, nil, fmt.Errorf("%w: video model required", ErrTemporaryUpstream)
	}

	parameters := map[string]any{
		"width":    width,
		"height":   height,
		"prompt":   prompt,
		"quantity": 1,
	}
	if durationSeconds > 0 {
		parameters["duration"] = durationSeconds
	}

	genReq := map[string]any{
		"operationName": "Generate",
		"query":         mGenerate,
		"variables": map[string]any{
			"request": map[string]any{
				"model":      model,
				"public":     true,
				"parameters": parameters,
			},
		},
	}
	payload, _ := json.Marshal(genReq)
	body, status, err := c.graphql(ctx, sess.AccessToken, payload)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %s", ErrTemporaryUpstream, err.Error())
	}
	if status == 401 || status == 403 {
		return nil, nil, ErrAuth
	}
	if status != 200 {
		return nil, nil, fmt.Errorf("%w: generate http %d: %s", ErrTemporaryUpstream, status, clip(body, 200))
	}
	if e := graphqlError(body); e != nil {
		return nil, nil, e
	}
	var genResp struct {
		Data struct {
			Generate struct {
				GenerationID string `json:"generationId"`
			} `json:"generate"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &genResp); err != nil {
		return nil, nil, fmt.Errorf("%w: generate non-json", ErrTemporaryUpstream)
	}
	genID := strings.TrimSpace(genResp.Data.Generate.GenerationID)
	if genID == "" {
		return nil, nil, fmt.Errorf("%w: no generationId: %s", ErrTemporaryUpstream, clip(body, 200))
	}

	videoURL, err := c.pollVideo(ctx, sess.AccessToken, genID)
	if err != nil {
		return nil, nil, err
	}

	info := map[string]any{
		"generation_id": genID,
		"video_url":     videoURL,
		"user_id":       sess.UserID,
	}
	if !downloadResult {
		return nil, info, nil
	}
	data, err := c.downloadImage(ctx, videoURL)
	if err != nil {
		return nil, nil, err
	}
	return data, info, nil
}

// pollVideo polls one generation until it reports COMPLETE (returning the first
// clip's mp4 url) or FAILED (error). Honors ctx cancellation / deadline. Video
// jobs run longer than images, so the fallback budget is wider.
func (c *Client) pollVideo(ctx context.Context, accessToken, genID string) (string, error) {
	payload, _ := json.Marshal(map[string]any{
		"operationName": "GenerationVideos",
		"query":         qGenerationVideos,
		"variables": map[string]any{
			"where": map[string]any{"id": map[string]any{"_in": []string{genID}}},
		},
	})

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	deadline := time.Now().Add(12 * time.Minute)
	if dl, ok := ctx.Deadline(); ok {
		deadline = dl.Add(-60 * time.Second)
	}

	for {
		body, status, err := c.graphqlP(ctx, accessToken, payload, true)
		if err != nil {
			return "", fmt.Errorf("%w: poll: %s", ErrTemporaryUpstream, err.Error())
		}
		if status == 401 || status == 403 {
			return "", ErrAuth
		}
		if status == 200 {
			var pr struct {
				Data struct {
					Generations []struct {
						Status          string `json:"status"`
						GeneratedImages []struct {
							URL          string `json:"url"`
							MotionMP4URL string `json:"motionMP4URL"`
						} `json:"generated_images"`
					} `json:"generations"`
				} `json:"data"`
			}
			if err := json.Unmarshal(body, &pr); err == nil && len(pr.Data.Generations) > 0 {
				g := pr.Data.Generations[0]
				switch strings.ToUpper(g.Status) {
				case "COMPLETE":
					for _, img := range g.GeneratedImages {
						if u := strings.TrimSpace(img.MotionMP4URL); u != "" {
							return u, nil
						}
					}
					for _, img := range g.GeneratedImages {
						if u := strings.TrimSpace(img.URL); u != "" && strings.Contains(strings.ToLower(u), ".mp4") {
							return u, nil
						}
					}
					return "", fmt.Errorf("%w: complete but no video url", ErrTemporaryUpstream)
				case "FAILED":
					return "", fmt.Errorf("%w: generation failed", ErrTemporaryUpstream)
				}
			}
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

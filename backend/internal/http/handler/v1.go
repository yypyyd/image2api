package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

type V1Handler struct {
	v1 *service.V1Service
}

func NewV1Handler(v1 *service.V1Service) *V1Handler {
	return &V1Handler{v1: v1}
}

func (h *V1Handler) Models(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	_ = principal

	// Keep the default response strictly OpenAI-compatible. Some downstream
	// model importers reject otherwise valid entries when they contain extension
	// fields. Capability metadata remains available as an explicit opt-in.
	extended := strings.EqualFold(strings.TrimSpace(c.Query("extended")), "true") || c.Query("extended") == "1"
	items, err := h.v1.ListModels(c.Request.Context(), extended)
	if err != nil {
		openaiError(c, http.StatusInternalServerError, "server_error", "", "failed to load models")
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"object": "list",
		"data":   items,
	})
}

// ChatCompletions — OpenAI POST /v1/chat/completions. The JSON request is
// forwarded to a configured custom OpenAI-compatible upstream; both ordinary
// JSON responses and SSE streaming are relayed without buffering.
func (h *V1Handler) ChatCompletions(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 10<<20+1))
	if err != nil || len(body) > 10<<20 {
		openaiError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "", "request body too large")
		return
	}
	resp, err := h.v1.PrepareChatCompletion(c.Request.Context(), principal, body)
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	defer resp.Body.Close()
	contentType := strings.TrimSpace(resp.Header.Get("Content-Type"))
	if resp.Stream {
		contentType = "text/event-stream"
	} else if contentType == "" {
		contentType = "application/json"
	}
	c.Header("Content-Type", contentType)
	if resp.Stream {
		c.Header("Cache-Control", "no-cache")
		c.Header("Connection", "keep-alive")
		c.Header("X-Accel-Buffering", "no")
	}
	for _, name := range []string{"OpenAI-Request-ID", "X-Request-ID", "OpenAI-Processing-Ms"} {
		if value := resp.Header.Get(name); value != "" {
			c.Header(name, value)
		}
	}
	c.Status(http.StatusOK)
	if resp.Stream {
		buf := make([]byte, 32<<10)
		for {
			n, readErr := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
					return
				}
				c.Writer.Flush()
			}
			if readErr != nil {
				return
			}
		}
	}
	_, _ = io.Copy(c.Writer, resp.Body)
}

// ImageGenerations — OpenAI POST /v1/images/generations (text-to-image only).
// Accepts exactly OpenAI's fields; size→aspect ratio and quality→resolution tier
// are mapped server-side. Returns {created, data:[{url}]} by default.
func (h *V1Handler) ImageGenerations(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}

	var body struct {
		Model          string `json:"model"`
		Prompt         string `json:"prompt"`
		N              int    `json:"n"`
		Size           string `json:"size"`
		Quality        string `json:"quality"`
		ResponseFormat string `json:"response_format"`
		Background     string `json:"background"`
		OutputFormat   string `json:"output_format"`
		User           string `json:"user"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "invalid request body")
		return
	}

	resp, err := h.v1.PrepareImageRequest(c.Request.Context(), principal, service.V1ImageRequest{
		Model:          body.Model,
		Prompt:         body.Prompt,
		RequestID:      imageRequestID(c),
		N:              body.N,
		Size:           body.Size,
		Quality:        body.Quality,
		ResponseFormat: body.ResponseFormat,
		BaseURL:        requestBaseURL(c),
	})
	if err != nil {
		h.writeV1Error(c, err, resp)
		return
	}
	c.JSON(http.StatusOK, openaiImageResponse(resp))
}

// ImageEdits — OpenAI POST /v1/images/edits (image-to-image). multipart/form-data
// only: image / image[] file uploads (+ optional mask), prompt, model, n, size,
// quality. Files become reference images. Returns {created, data:[{url}]} by default.
func (h *V1Handler) ImageEdits(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	if !strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "images/edits requires multipart/form-data")
		return
	}
	if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
		openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "invalid multipart form")
		return
	}
	refs := readMultipartImages(c, "image", "image[]")
	if len(refs) == 0 {
		openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "images/edits requires at least one image file")
		return
	}
	n, _ := strconv.Atoi(strings.TrimSpace(c.PostForm("n")))
	resp, err := h.v1.PrepareImageRequest(c.Request.Context(), principal, service.V1ImageRequest{
		Model:           c.PostForm("model"),
		Prompt:          c.PostForm("prompt"),
		RequestID:       imageRequestID(c),
		N:               n,
		Size:            c.PostForm("size"),
		Quality:         c.PostForm("quality"),
		ResponseFormat:  c.PostForm("response_format"),
		ReferenceImages: refs,
		BaseURL:         requestBaseURL(c),
	})
	if err != nil {
		h.writeV1Error(c, err, resp)
		return
	}
	c.JSON(http.StatusOK, openaiImageResponse(resp))
}

func (h *V1Handler) GetImageTask(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	resp, err := h.v1.ImageTask(c.Request.Context(), principal, c.Query("request_id"))
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func imageRequestID(c *gin.Context) string {
	if value := strings.TrimSpace(c.GetHeader("Idempotency-Key")); value != "" {
		return value
	}
	return strings.TrimSpace(c.GetHeader("X-Request-ID"))
}

// CreateVideo — OpenAI POST /v1/videos. Creates an async job and returns the
// video object immediately ({id, status:"queued"}). Accepts JSON {model, prompt,
// seconds, size} or multipart (with an input_reference file). size→ratio+
// resolution, seconds→duration.
func (h *V1Handler) CreateVideo(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	var modelID, prompt, seconds, size string
	var refs []string
	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(64 << 20); err != nil {
			openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "invalid multipart form")
			return
		}
		modelID = c.PostForm("model")
		prompt = c.PostForm("prompt")
		seconds = c.PostForm("seconds")
		size = c.PostForm("size")
		refs = readMultipartImages(c, "input_reference", "input_reference[]")
	} else {
		var body struct {
			Model   string          `json:"model"`
			Prompt  string          `json:"prompt"`
			Seconds json.RawMessage `json:"seconds"`
			Size    string          `json:"size"`
			// Reference frames (image-to-video / first-last frames) as base64 or
			// data-URI strings — the JSON equivalent of multipart input_reference.
			InputReference  []string `json:"input_reference"`
			ReferenceImages []string `json:"reference_images"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "invalid request body")
			return
		}
		modelID, prompt, size = body.Model, body.Prompt, body.Size
		seconds = rawToString(body.Seconds)
		refs = append(body.InputReference, body.ReferenceImages...)
	}
	duration := strings.TrimSpace(seconds)
	if duration != "" && !strings.HasSuffix(duration, "s") {
		duration += "s"
	}
	aspect, resolution := videoSizeToInternal(size)
	resp, err := h.v1.StartVideoJob(c.Request.Context(), principal, service.V1VideoRequest{
		Model:           modelID,
		Prompt:          prompt,
		Duration:        duration,
		AspectRatio:     aspect,
		Resolution:      resolution,
		ReferenceImages: refs,
		BaseURL:         requestBaseURL(c),
	})
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetVideo — OpenAI GET /v1/videos/{id}. Returns the job status object.
func (h *V1Handler) GetVideo(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	resp, err := h.v1.VideoJob(c.Request.Context(), principal, c.Param("id"))
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// GetVideoContent — OpenAI GET /v1/videos/{id}/content. Streams the rendered mp4
// by proxying the stored upstream URL (downloaded on demand, never persisted).
func (h *V1Handler) GetVideoContent(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	body, contentType, err := h.v1.OpenVideoContent(c.Request.Context(), principal, c.Param("id"))
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	defer body.Close()
	c.Header("Content-Type", contentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, body)
}

// GetImageContent — GET /v1/images/{id}/content. Streams a no-store image by
// proxying its stored (possibly auth-gated) upstream URL. Never persisted.
func (h *V1Handler) GetImageContent(c *gin.Context) {
	principal, err := h.v1.Authenticate(c.Request.Context(), c.GetHeader("Authorization"))
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	body, contentType, err := h.v1.OpenImageContent(c.Request.Context(), principal, c.Param("id"))
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	defer body.Close()
	c.Header("Content-Type", contentType)
	c.Status(http.StatusOK)
	_, _ = io.Copy(c.Writer, body)
}

// readMultipartImages reads the given file fields and returns each as base64.
func readMultipartImages(c *gin.Context, keys ...string) []string {
	var out []string
	form := c.Request.MultipartForm
	if form == nil {
		return out
	}
	for _, key := range keys {
		for _, fh := range form.File[key] {
			f, e := fh.Open()
			if e != nil {
				continue
			}
			b, _ := io.ReadAll(io.LimitReader(f, 20<<20+1))
			f.Close()
			if len(b) > 0 {
				out = append(out, base64.StdEncoding.EncodeToString(b))
			}
		}
	}
	return out
}

// rawToString accepts OpenAI's `seconds` whether sent as a JSON string or number.
func rawToString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(raw, &n) == nil {
		return n.String()
	}
	return strings.Trim(string(raw), `"`)
}

// videoSizeToInternal maps OpenAI's "WxH" size to our aspect ratio + resolution
// tier (height ≥1080 → 1080p, else 720p).
func videoSizeToInternal(size string) (ratio, resolution string) {
	var w, h int
	if s := strings.TrimSpace(strings.ToLower(size)); s != "" {
		_, _ = fmt.Sscanf(s, "%dx%d", &w, &h)
	}
	if w == 0 || h == 0 {
		return "16:9", "720p"
	}
	// The "p" resolution is the SHORT edge (720p = 1280×720, 1080p = 1920×1080),
	// so a standard 1280×720 must read as 720p — not 1080p off the long edge.
	resolution = "720p"
	if min(w, h) >= 1080 {
		resolution = "1080p"
	}
	return guessRatioWH(w, h), resolution
}

func guessRatioWH(w, h int) string {
	if w == h {
		return "1:1"
	}
	r := float64(w) / float64(h)
	cands := []struct {
		name string
		v    float64
	}{{"16:9", 16.0 / 9}, {"9:16", 9.0 / 16}, {"4:3", 4.0 / 3}, {"3:4", 3.0 / 4}, {"1:1", 1}}
	best, bestD := "16:9", 1e9
	for _, cd := range cands {
		d := r - cd.v
		if d < 0 {
			d = -d
		}
		if d < bestD {
			best, bestD = cd.name, d
		}
	}
	return best
}

// openaiImageResponse strips our rich internal map down to OpenAI's image shape.
func openaiImageResponse(m map[string]any) gin.H {
	out := gin.H{"created": m["created"]}
	if d, ok := m["data"]; ok && d != nil {
		out["data"] = d
	} else {
		out["data"] = []any{}
	}
	return out
}

// openaiError writes an OpenAI-format error body:
// {"error": {"message", "type", "param", "code"}}.
func openaiError(c *gin.Context, status int, errType, code, message string) {
	body := gin.H{"message": message, "type": errType, "param": nil, "code": nil}
	if code != "" {
		body["code"] = code
	}
	c.JSON(status, gin.H{"error": body})
}

func (h *V1Handler) writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrMissingAPIKey):
		openaiError(c, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", err.Error())
	case errors.Is(err, service.ErrInvalidAPIKey):
		openaiError(c, http.StatusUnauthorized, "invalid_request_error", "invalid_api_key", err.Error())
	default:
		openaiError(c, http.StatusInternalServerError, "server_error", "", "failed to validate api key")
	}
}

func (h *V1Handler) writeV1Error(c *gin.Context, err error, payload map[string]any) {
	switch {
	case errors.Is(err, service.ErrUnknownModel):
		openaiError(c, http.StatusNotFound, "invalid_request_error", "model_not_found", err.Error())
	case errors.Is(err, service.ErrUnsupportedParams), errors.Is(err, service.ErrBannedPrompt):
		openaiError(c, http.StatusBadRequest, "invalid_request_error", "", err.Error())
	case errors.Is(err, service.ErrInsufficientFunds):
		openaiError(c, http.StatusPaymentRequired, "insufficient_quota", "insufficient_quota", err.Error())
	case errors.Is(err, service.ErrReferenceTooLarge):
		openaiError(c, http.StatusRequestEntityTooLarge, "invalid_request_error", "", err.Error())
	case errors.Is(err, service.ErrNoProviderAccount):
		openaiError(c, http.StatusServiceUnavailable, "server_error", "", err.Error())
	case errors.Is(err, service.ErrProviderAuth):
		openaiError(c, http.StatusServiceUnavailable, "server_error", "", err.Error())
	case errors.Is(err, service.ErrProviderQuota):
		// Match the Python contract: provider quota exhaustion maps to 401
		// (QuotaExhaustedError is handled alongside AuthError in routes.py).
		openaiError(c, http.StatusUnauthorized, "insufficient_quota", "insufficient_quota", err.Error())
	case errors.Is(err, service.ErrProviderTemporary):
		openaiError(c, http.StatusServiceUnavailable, "server_error", "", err.Error())
	case errors.Is(err, service.ErrImageTaskNotFound):
		openaiError(c, http.StatusNotFound, "invalid_request_error", "not_found", err.Error())
	case errors.Is(err, service.ErrConcurrencyFull), errors.Is(err, service.ErrUserConcurrencyFull):
		openaiError(c, http.StatusTooManyRequests, "rate_limit_error", "rate_limit_exceeded", err.Error())
	case errors.Is(err, service.ErrVideoJobNotFound):
		openaiError(c, http.StatusNotFound, "invalid_request_error", "not_found", err.Error())
	case errors.Is(err, service.ErrVideoNotReady):
		openaiError(c, http.StatusConflict, "invalid_request_error", "", err.Error())
	case errors.Is(err, service.ErrProviderUnsupported):
		openaiError(c, http.StatusNotImplemented, "server_error", "", err.Error())
	case errors.Is(err, service.ErrProviderExecution):
		openaiError(c, http.StatusBadGateway, "server_error", "", err.Error())
	case errors.Is(err, service.ErrGenerationPending):
		c.JSON(http.StatusNotImplemented, payload)
	default:
		openaiError(c, http.StatusBadRequest, "invalid_request_error", "", err.Error())
	}
}

// requestBaseURL derives the scheme+host of the inbound request so the service
// layer can build absolute, directly-downloadable output URLs. Honors
// X-Forwarded-Proto (reverse-proxy / TLS termination) before falling back to
// the connection's TLS state. Returns "" when the host is unknown, which makes
// the service fall back to a relative path.
func requestBaseURL(c *gin.Context) string {
	host := c.Request.Host
	if host == "" {
		return ""
	}
	scheme := "http"
	if proto := strings.TrimSpace(c.GetHeader("X-Forwarded-Proto")); proto != "" {
		scheme = strings.ToLower(strings.Split(proto, ",")[0])
	} else if c.Request.TLS != nil {
		scheme = "https"
	}
	return scheme + "://" + host
}

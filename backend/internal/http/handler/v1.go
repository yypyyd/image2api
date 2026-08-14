package handler

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"backend/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type V1Handler struct {
	v1 *service.V1Service
}

const (
	imageSyncKeepaliveDelay    = 10 * time.Second
	imageSyncKeepaliveInterval = 10 * time.Second
)

type imageSyncResult struct {
	response map[string]any
	err      error
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

	// Capability metadata is part of the default catalog so downstream gateways
	// can discover image/video ratios without knowing our extension parameter.
	// `?extended=false` remains available for strict OpenAI importers that reject
	// otherwise-valid extension fields.
	extended := true
	if raw := strings.TrimSpace(c.Query("extended")); raw != "" {
		extended = strings.EqualFold(raw, "true") || raw == "1"
	}
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

	h.respondImageRequest(c, principal, service.V1ImageRequest{
		Model:          body.Model,
		Prompt:         body.Prompt,
		RequestID:      imageRequestID(c),
		N:              body.N,
		Size:           body.Size,
		Quality:        body.Quality,
		ResponseFormat: body.ResponseFormat,
		BaseURL:        requestBaseURL(c),
	})
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
	h.respondImageRequest(c, principal, service.V1ImageRequest{
		Model:           c.PostForm("model"),
		Prompt:          c.PostForm("prompt"),
		RequestID:       imageRequestID(c),
		N:               n,
		Size:            c.PostForm("size"),
		Quality:         c.PostForm("quality"),
		ResponseFormat:  c.PostForm("response_format"),
		ReferenceImages: refs,
		ReferenceGrid:   parseFormBool(c.PostForm("reference_grid")),
		BaseURL:         requestBaseURL(c),
	})
}

func (h *V1Handler) respondImageRequest(c *gin.Context, principal *service.APIPrincipal, request service.V1ImageRequest) {
	if !imageAsyncRequested(c) {
		results := make(chan imageSyncResult, 1)
		go func() {
			resp, err := h.v1.PrepareImageRequest(c.Request.Context(), principal, request)
			results <- imageSyncResult{response: resp, err: err}
		}()
		h.writeSynchronousImageResponse(c, results, imageSyncKeepaliveDelay, imageSyncKeepaliveInterval)
		return
	}

	request.RequestID = ensureImageRequestID(c, request.RequestID)
	resp, pending, err := h.v1.StartImageRequest(c.Request.Context(), principal, request)
	if err != nil {
		h.writeV1Error(c, err, resp)
		return
	}
	c.Header("Idempotency-Key", request.RequestID)
	if pollURL, _ := resp["poll_url"].(string); pollURL != "" {
		c.Header("Location", pollURL)
	}
	if pending {
		c.Header("Preference-Applied", "respond-async")
		c.Header("Retry-After", "3")
		c.JSON(http.StatusAccepted, resp)
		return
	}
	c.JSON(http.StatusOK, resp)
}

// writeSynchronousImageResponse preserves the original OpenAI JSON contract
// while preventing response-header and idle proxy timeouts during slow image
// generations. JSON permits leading whitespace, so each flushed heartbeat is
// still followed by one valid success or error object.
func (h *V1Handler) writeSynchronousImageResponse(c *gin.Context, results <-chan imageSyncResult, initialDelay, interval time.Duration) {
	timer := time.NewTimer(initialDelay)
	defer timer.Stop()

	select {
	case result := <-results:
		h.finishSynchronousImageResponse(c, result, false)
		return
	case <-timer.C:
	case <-c.Request.Context().Done():
		return
	}

	c.Header("Content-Type", "application/json; charset=utf-8")
	c.Header("Cache-Control", "no-store, no-transform")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	if _, err := c.Writer.Write([]byte(" \n")); err != nil {
		return
	}
	c.Writer.Flush()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case result := <-results:
			h.finishSynchronousImageResponse(c, result, true)
			c.Writer.Flush()
			return
		case <-ticker.C:
			if _, err := c.Writer.Write([]byte(" \n")); err != nil {
				return
			}
			c.Writer.Flush()
		case <-c.Request.Context().Done():
			return
		}
	}
}

func (h *V1Handler) finishSynchronousImageResponse(c *gin.Context, result imageSyncResult, responseStarted bool) {
	if result.err == nil {
		if responseStarted {
			_ = json.NewEncoder(c.Writer).Encode(openaiImageResponse(result.response))
			return
		}
		c.JSON(http.StatusOK, openaiImageResponse(result.response))
		return
	}

	status, body := v1ErrorResponse(result.err, result.response)
	if responseStarted {
		_ = json.NewEncoder(c.Writer).Encode(body)
		return
	}
	c.JSON(status, body)
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

func ensureImageRequestID(c *gin.Context, requestID string) string {
	if value := strings.TrimSpace(requestID); value != "" {
		return value
	}
	if value := strings.TrimSpace(c.Writer.Header().Get("X-Request-Id")); value != "" {
		return value
	}
	return uuid.NewString()
}

func imageAsyncRequested(c *gin.Context) bool {
	prefer := strings.ToLower(c.GetHeader("Prefer"))
	for _, item := range strings.Split(prefer, ",") {
		if strings.TrimSpace(strings.SplitN(item, ";", 2)[0]) == "respond-async" {
			return true
		}
	}
	switch strings.ToLower(strings.TrimSpace(c.Query("async"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
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
	var modelID, prompt, seconds, size, resolutionOverride string
	var refs []string
	var videos, audios []service.MediaReference
	var generateAudio bool
	var referenceGrid bool
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxGenerateMultipartBytes)
	if strings.HasPrefix(c.GetHeader("Content-Type"), "multipart/form-data") {
		if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
			openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "invalid multipart form")
			return
		}
		defer c.Request.MultipartForm.RemoveAll()
		modelID = c.PostForm("model")
		prompt = c.PostForm("prompt")
		seconds = c.PostForm("seconds")
		size = c.PostForm("size")
		resolutionOverride = c.PostForm("resolution")
		var readErr error
		refs, readErr = readMultipartImagesStrict(c, "input_reference", "input_reference[]", "reference_images", "reference_images[]")
		if readErr == nil {
			videos, readErr = readMultipartMedia(c, 200<<20, "reference_videos", "reference_videos[]", "input_video")
		}
		if readErr == nil {
			audios, readErr = readMultipartMedia(c, 50<<20, "reference_audios", "reference_audios[]", "input_audio")
		}
		if readErr != nil {
			openaiError(c, http.StatusBadRequest, "invalid_request_error", "", readErr.Error())
			return
		}
		generateAudio = parseFormBool(c.PostForm("generate_audio"))
		referenceGrid = parseFormBool(c.PostForm("reference_grid"))
	} else {
		var body struct {
			Model   string          `json:"model"`
			Prompt  string          `json:"prompt"`
			Seconds json.RawMessage `json:"seconds"`
			Size    string          `json:"size"`
			// Resolution is a gateway extension for provider tiers that OpenAI's
			// standard 720p/1080p size mapping cannot express (for example 480p).
			Resolution string `json:"resolution"`
			// Reference frames (image-to-video / first-last frames) as base64 or
			// data-URI strings — the JSON equivalent of multipart input_reference.
			InputReference  []string `json:"input_reference"`
			ReferenceImages []string `json:"reference_images"`
			ReferenceVideos []string `json:"reference_videos"`
			ReferenceAudios []string `json:"reference_audios"`
			GenerateAudio   bool     `json:"generate_audio"`
			ReferenceGrid   bool     `json:"reference_grid"`
		}
		if err := c.ShouldBindJSON(&body); err != nil {
			openaiError(c, http.StatusBadRequest, "invalid_request_error", "", "invalid request body")
			return
		}
		modelID, prompt, size = body.Model, body.Prompt, body.Size
		resolutionOverride = body.Resolution
		seconds = rawToString(body.Seconds)
		refs = append(body.InputReference, body.ReferenceImages...)
		videos, err = decodeJSONMedia(body.ReferenceVideos, "video/mp4")
		if err == nil {
			audios, err = decodeJSONMedia(body.ReferenceAudios, "audio/mpeg")
		}
		if err != nil {
			openaiError(c, http.StatusBadRequest, "invalid_request_error", "", err.Error())
			return
		}
		generateAudio = body.GenerateAudio
		referenceGrid = body.ReferenceGrid
	}
	duration := strings.TrimSpace(seconds)
	if duration != "" && !strings.HasSuffix(duration, "s") {
		duration += "s"
	}
	aspect, resolution := videoSizeToInternal(size)
	if override := normalizeVideoResolutionOverride(resolutionOverride); override != "" {
		resolution = override
	}
	resp, err := h.v1.StartVideoJob(c.Request.Context(), principal, service.V1VideoRequest{
		Model:           modelID,
		Prompt:          prompt,
		Duration:        duration,
		AspectRatio:     aspect,
		Resolution:      resolution,
		ReferenceImages: refs,
		ReferenceVideos: videos,
		ReferenceAudios: audios,
		ReferenceGrid:   referenceGrid,
		GenerateAudio:   generateAudio,
		BaseURL:         requestBaseURL(c),
	})
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	c.JSON(http.StatusOK, resp)
}

func normalizeVideoResolutionOverride(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}
	if strings.HasSuffix(value, "p") {
		return value
	}
	if _, err := strconv.Atoi(value); err == nil {
		return value + "p"
	}
	return value
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
// proxying its stored upstream URL. This endpoint is intentionally public so
// browser/desktop API clients can fetch the returned URL without forwarding an
// Authorization header; event IDs are random and the upstream object remains
// hidden behind this proxy.
func (h *V1Handler) GetImageContent(c *gin.Context) {
	body, contentType, err := h.v1.OpenImageContent(c.Request.Context(), nil, c.Param("id"))
	if err != nil {
		h.writeV1Error(c, err, nil)
		return
	}
	defer body.Close()
	c.Header("Access-Control-Allow-Origin", "*")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
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
	c.JSON(status, openaiErrorResponse(errType, code, message))
}

func openaiErrorResponse(errType, code, message string) gin.H {
	body := gin.H{"message": message, "type": errType, "param": nil, "code": nil}
	if code != "" {
		body["code"] = code
	}
	return gin.H{"error": body}
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
	status, body := v1ErrorResponse(err, payload)
	c.JSON(status, body)
}

func v1ErrorResponse(err error, payload map[string]any) (int, any) {
	switch {
	case errors.Is(err, service.ErrUnknownModel):
		return http.StatusNotFound, openaiErrorResponse("invalid_request_error", "model_not_found", err.Error())
	case errors.Is(err, service.ErrUnsupportedParams), errors.Is(err, service.ErrBannedPrompt):
		return http.StatusBadRequest, openaiErrorResponse("invalid_request_error", "", err.Error())
	case errors.Is(err, service.ErrContentRejected):
		return http.StatusBadRequest, openaiErrorResponse("invalid_request_error", "content_policy_violation", err.Error())
	case errors.Is(err, service.ErrInsufficientFunds):
		return http.StatusPaymentRequired, openaiErrorResponse("insufficient_quota", "insufficient_quota", err.Error())
	case errors.Is(err, service.ErrReferenceTooLarge):
		return http.StatusRequestEntityTooLarge, openaiErrorResponse("invalid_request_error", "", err.Error())
	case errors.Is(err, service.ErrNoProviderAccount):
		return http.StatusServiceUnavailable, openaiErrorResponse("server_error", "", err.Error())
	case errors.Is(err, service.ErrProviderAuth):
		return http.StatusServiceUnavailable, openaiErrorResponse("server_error", "", err.Error())
	case errors.Is(err, service.ErrProviderQuota):
		// Match the Python contract: provider quota exhaustion maps to 401
		// (QuotaExhaustedError is handled alongside AuthError in routes.py).
		return http.StatusUnauthorized, openaiErrorResponse("insufficient_quota", "insufficient_quota", err.Error())
	case errors.Is(err, service.ErrProviderTemporary):
		return http.StatusServiceUnavailable, openaiErrorResponse("server_error", "", err.Error())
	case errors.Is(err, service.ErrImageTaskNotFound):
		return http.StatusNotFound, openaiErrorResponse("invalid_request_error", "not_found", err.Error())
	case errors.Is(err, service.ErrConcurrencyFull), errors.Is(err, service.ErrUserConcurrencyFull):
		return http.StatusTooManyRequests, openaiErrorResponse("rate_limit_error", "rate_limit_exceeded", err.Error())
	case errors.Is(err, service.ErrVideoJobNotFound):
		return http.StatusNotFound, openaiErrorResponse("invalid_request_error", "not_found", err.Error())
	case errors.Is(err, service.ErrVideoNotReady):
		return http.StatusConflict, openaiErrorResponse("invalid_request_error", "", err.Error())
	case errors.Is(err, service.ErrProviderUnsupported):
		return http.StatusNotImplemented, openaiErrorResponse("server_error", "", err.Error())
	case errors.Is(err, service.ErrProviderExecution):
		return http.StatusBadGateway, openaiErrorResponse("server_error", "", err.Error())
	case errors.Is(err, service.ErrGenerationPending):
		return http.StatusNotImplemented, payload
	default:
		return http.StatusBadRequest, openaiErrorResponse("invalid_request_error", "", err.Error())
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
	if port := strings.TrimSpace(strings.Split(c.GetHeader("X-Forwarded-Port"), ",")[0]); port != "" && !hostHasPort(host) {
		if (scheme == "http" && port != "80") || (scheme == "https" && port != "443") {
			host = net.JoinHostPort(strings.Trim(host, "[]"), port)
		}
	}
	return scheme + "://" + host
}

func hostHasPort(host string) bool {
	_, port, err := net.SplitHostPort(host)
	return err == nil && port != ""
}

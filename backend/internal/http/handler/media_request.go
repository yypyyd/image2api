package handler

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"backend/internal/service"
	"github.com/gin-gonic/gin"
)

const maxGenerateMultipartBytes = 320 * 1024 * 1024

func bindGenerateRequest(c *gin.Context) (service.UserGenerateRequest, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxGenerateMultipartBytes)
	if !strings.HasPrefix(strings.ToLower(c.GetHeader("Content-Type")), "multipart/form-data") {
		return bindJSONGenerateRequest(c)
	}
	if err := c.Request.ParseMultipartForm(32 << 20); err != nil {
		return service.UserGenerateRequest{}, err
	}
	defer c.Request.MultipartForm.RemoveAll()
	images, err := readMultipartImagesStrict(c, "reference_images", "reference_images[]")
	if err != nil {
		return service.UserGenerateRequest{}, err
	}
	videos, err := readMultipartMedia(c, 200<<20, "reference_videos", "reference_videos[]")
	if err != nil {
		return service.UserGenerateRequest{}, err
	}
	audios, err := readMultipartMedia(c, 50<<20, "reference_audios", "reference_audios[]")
	if err != nil {
		return service.UserGenerateRequest{}, err
	}
	return service.UserGenerateRequest{
		Model: c.PostForm("model"), Prompt: c.PostForm("prompt"), Ratio: c.PostForm("ratio"),
		Resolution: c.PostForm("resolution"), Duration: c.PostForm("duration"),
		ReferenceImages: images, ReferenceVideos: videos, ReferenceAudios: audios,
		GenerateAudio: parseFormBool(c.PostForm("generate_audio")),
		DeAI:          parseFormBool(c.PostForm("deai")), AccountID: c.PostForm("account_id"),
	}, nil
}

func bindJSONGenerateRequest(c *gin.Context) (service.UserGenerateRequest, error) {
	var body struct {
		Model, Prompt, Ratio, Resolution, Duration string
		ReferenceImages                            []string `json:"reference_images"`
		ReferenceVideos                            []string `json:"reference_videos"`
		ReferenceAudios                            []string `json:"reference_audios"`
		GenerateAudio                              bool     `json:"generate_audio"`
		DeAI                                       bool     `json:"deai"`
		AccountID                                  string   `json:"account_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		return service.UserGenerateRequest{}, err
	}
	videos, err := decodeJSONMedia(body.ReferenceVideos, "video/mp4")
	if err != nil {
		return service.UserGenerateRequest{}, err
	}
	audios, err := decodeJSONMedia(body.ReferenceAudios, "audio/mpeg")
	if err != nil {
		return service.UserGenerateRequest{}, err
	}
	return service.UserGenerateRequest{
		Model: body.Model, Prompt: body.Prompt, Ratio: body.Ratio,
		Resolution: body.Resolution, Duration: body.Duration,
		ReferenceImages: body.ReferenceImages, ReferenceVideos: videos,
		ReferenceAudios: audios, GenerateAudio: body.GenerateAudio,
		DeAI: body.DeAI, AccountID: body.AccountID,
	}, nil
}

func parseFormBool(v string) bool {
	b, _ := strconv.ParseBool(strings.TrimSpace(v))
	return b
}

func decodeJSONMedia(inputs []string, defaultContentType string) ([]service.MediaReference, error) {
	out := make([]service.MediaReference, 0, len(inputs))
	for _, raw := range inputs {
		value := strings.TrimSpace(raw)
		if value == "" {
			continue
		}
		contentType := defaultContentType
		if strings.HasPrefix(value, "data:") {
			parts := strings.SplitN(value, ",", 2)
			if len(parts) != 2 || !strings.Contains(parts[0], ";base64") {
				return nil, errors.New("invalid media data URI")
			}
			contentType = strings.TrimPrefix(strings.SplitN(parts[0], ";", 2)[0], "data:")
			value = parts[1]
		}
		maxBytes := 50 << 20
		if strings.HasPrefix(strings.ToLower(contentType), "video/") {
			maxBytes = 200 << 20
		}
		if len(value) > ((maxBytes+2)/3)*4+4 {
			return nil, errors.New("reference media is too large")
		}
		data, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			data, err = base64.RawStdEncoding.DecodeString(value)
		}
		if err != nil || len(data) == 0 {
			return nil, errors.New("invalid media encoding")
		}
		out = append(out, service.MediaReference{Data: data, ContentType: contentType})
	}
	return out, nil
}

func readMultipartImagesStrict(c *gin.Context, keys ...string) ([]string, error) {
	refs, err := readMultipartMedia(c, 20<<20, keys...)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if !strings.HasPrefix(strings.ToLower(ref.ContentType), "image/") {
			return nil, errors.New("reference image must be an image")
		}
		out = append(out, base64.StdEncoding.EncodeToString(ref.Data))
	}
	return out, nil
}

func readMultipartMedia(c *gin.Context, maxBytes int64, keys ...string) ([]service.MediaReference, error) {
	form := c.Request.MultipartForm
	if form == nil {
		return nil, nil
	}
	var out []service.MediaReference
	for _, key := range keys {
		for _, fh := range form.File[key] {
			f, err := fh.Open()
			if err != nil {
				return nil, err
			}
			data, readErr := io.ReadAll(io.LimitReader(f, maxBytes+1))
			_ = f.Close()
			if readErr != nil {
				return nil, readErr
			}
			if len(data) == 0 || int64(len(data)) > maxBytes {
				return nil, errors.New("reference media is empty or too large")
			}
			contentType := strings.TrimSpace(fh.Header.Get("Content-Type"))
			if contentType == "" || contentType == "application/octet-stream" {
				contentType = mediaTypeFromName(fh.Filename, data)
			}
			out = append(out, service.MediaReference{Data: data, ContentType: contentType, Filename: fh.Filename})
		}
	}
	return out, nil
}

func mediaTypeFromName(filename string, data []byte) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".mov":
		return "video/mov"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".m4a":
		return "audio/mp4"
	default:
		return http.DetectContentType(data[:min(len(data), 512)])
	}
}

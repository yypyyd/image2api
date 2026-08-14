package oreate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

const (
	uploadTokenPath = "/oreate/convert/getuploadbostoken"
	gcsUploadBase   = "https://storage.googleapis.com/upload/storage/v1/b/"
)

var storageBucketPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

type MediaReference struct {
	Data        []byte
	ContentType string
	Filename    string
	DurationSec float64
}

type uploadFileRequest struct {
	Filename string `json:"filename"`
	FileExt  string `json:"fileExt"`
	Size     int    `json:"size"`
}

type uploadTokenRequest struct {
	Files  []uploadFileRequest `json:"mFileList"`
	Source string              `json:"source"`
}

type uploadCredential struct {
	Bucket     string `json:"bucket"`
	ObjectPath string `json:"objectPath"`
	SessionKey string `json:"sessionkey"`
}

type preparedUpload struct {
	Reference MediaReference
	Filename  string
	Title     string
	Extension string
	Kind      string
}

type uploadedMedia struct {
	ObjectPath string
	Attachment videoAttachment
}

func (c *Client) uploadReferences(ctx context.Context, account Account, images, videos []MediaReference) ([]uploadedMedia, []uploadedMedia, error) {
	prepared := make([]preparedUpload, 0, len(images)+len(videos))
	for _, group := range []struct {
		kind string
		refs []MediaReference
	}{{"image", images}, {"video", videos}} {
		for i, ref := range group.refs {
			ext, contentType, err := mediaExtension(group.kind, ref.ContentType, ref.Data)
			if err != nil {
				return nil, nil, err
			}
			ref.ContentType = contentType
			base := fmt.Sprintf("reference-%s-%d-%s", group.kind, i+1, uuid.NewString())
			prepared = append(prepared, preparedUpload{
				Reference: ref, Filename: base + "." + ext,
				Title: fmt.Sprintf("reference-%s-%d.%s", group.kind, i+1, ext), Extension: ext, Kind: group.kind,
			})
		}
	}
	if len(prepared) == 0 {
		return nil, nil, nil
	}
	credentials, err := c.fetchUploadCredentials(ctx, account, prepared)
	if err != nil {
		return nil, nil, err
	}
	var uploadedImages, uploadedVideos []uploadedMedia
	for _, item := range prepared {
		credential, ok := credentials[item.Filename]
		if !ok {
			return nil, nil, fmt.Errorf("%w: upload token missing file assignment", ErrTemporaryUpstream)
		}
		if err := c.uploadObject(ctx, credential, item.Reference); err != nil {
			return nil, nil, err
		}
		attachment := videoAttachment{
			BOSURL: credential.ObjectPath, DocID: "", DocTitle: item.Title,
			DocType: item.Extension, Size: len(item.Reference.Data), BOSURLAlias: credential.ObjectPath,
			Flag: "upload", Type: "file", Status: 1,
		}
		if item.Kind == "video" {
			attachment.VideoDurationSec = item.Reference.DurationSec
		}
		media := uploadedMedia{ObjectPath: credential.ObjectPath, Attachment: attachment}
		if item.Kind == "image" {
			uploadedImages = append(uploadedImages, media)
		} else {
			uploadedVideos = append(uploadedVideos, media)
		}
	}
	return uploadedImages, uploadedVideos, nil
}

func (c *Client) fetchUploadCredentials(ctx context.Context, account Account, files []preparedUpload) (map[string]uploadCredential, error) {
	payload := uploadTokenRequest{Source: "aiImage", Files: make([]uploadFileRequest, 0, len(files))}
	for _, item := range files {
		payload.Files = append(payload.Files, uploadFileRequest{
			Filename: strings.TrimSuffix(item.Filename, "."+item.Extension), FileExt: item.Extension, Size: len(item.Reference.Data),
		})
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint(uploadTokenPath), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	setHeaders(req, account, "application/json")
	resp, err := c.httpClient(true).Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: upload token request: %v", ErrTemporaryUpstream, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrAuth
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return nil, classifyUpstreamError(resp.StatusCode, string(body))
	}
	responseBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	var env statusEnvelope
	if err := json.Unmarshal(responseBody, &env); err != nil {
		return nil, fmt.Errorf("%w: invalid upload token response", ErrTemporaryUpstream)
	}
	if env.Status.Code != 0 {
		return nil, classifyUpstreamError(env.Status.Code, env.Status.Msg)
	}
	var data struct {
		KeyList map[string]uploadCredential `json:"KeyList"`
	}
	if err := json.Unmarshal(env.Data, &data); err != nil || len(data.KeyList) == 0 {
		return nil, fmt.Errorf("%w: upload token response missing assignments", ErrTemporaryUpstream)
	}
	for filename, credential := range data.KeyList {
		if strings.TrimSpace(filename) == "" || !storageBucketPattern.MatchString(credential.Bucket) || strings.TrimSpace(credential.ObjectPath) == "" || strings.TrimSpace(credential.SessionKey) == "" {
			return nil, fmt.Errorf("%w: invalid upload token assignment", ErrTemporaryUpstream)
		}
	}
	return data.KeyList, nil
}

func (c *Client) uploadObject(ctx context.Context, credential uploadCredential, ref MediaReference) error {
	uploadClient := c.httpClient(false)
	clientCopy := *uploadClient
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}
	values := url.Values{"uploadType": {"resumable"}, "name": {credential.ObjectPath}}
	initURL := gcsUploadBase + url.PathEscape(credential.Bucket) + "/o?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, initURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+credential.SessionKey)
	req.Header.Set("X-Upload-Content-Type", ref.ContentType)
	req.Header.Set("X-Upload-Content-Length", strconv.Itoa(len(ref.Data)))
	resp, err := clientCopy.Do(req)
	if err != nil {
		return fmt.Errorf("%w: initialize media upload: %v", ErrTemporaryUpstream, err)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%w: initialize media upload http %d", ErrTemporaryUpstream, resp.StatusCode)
	}
	resumeURL, err := validateResumeURL(resp.Header.Get("Location"))
	if err != nil {
		return err
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, resumeURL, bytes.NewReader(ref.Data))
	if err != nil {
		return err
	}
	put.Header.Set("Authorization", "Bearer "+credential.SessionKey)
	put.Header.Set("Content-Type", ref.ContentType)
	put.ContentLength = int64(len(ref.Data))
	putResp, err := clientCopy.Do(put)
	if err != nil {
		return fmt.Errorf("%w: upload media: %v", ErrTemporaryUpstream, err)
	}
	defer putResp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(putResp.Body, 64<<10))
	if putResp.StatusCode < 200 || putResp.StatusCode >= 300 {
		return fmt.Errorf("%w: upload media http %d", ErrTemporaryUpstream, putResp.StatusCode)
	}
	return nil
}

func validateResumeURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "https" || parsed.Host != "storage.googleapis.com" || parsed.User != nil {
		return "", fmt.Errorf("%w: invalid media upload location", ErrTemporaryUpstream)
	}
	return parsed.String(), nil
}

func mediaExtension(kind, contentType string, data []byte) (string, string, error) {
	contentType = strings.ToLower(strings.TrimSpace(strings.Split(contentType, ";")[0]))
	if kind == "image" && contentType == "" {
		contentType = strings.ToLower(strings.TrimSpace(strings.Split(http.DetectContentType(data), ";")[0]))
	}
	switch contentType {
	case "image/jpeg":
		return "jpg", contentType, nil
	case "image/png":
		return "png", contentType, nil
	case "image/webp":
		return "webp", contentType, nil
	case "video/mp4":
		return "mp4", contentType, nil
	case "video/quicktime", "video/mov":
		return "mov", "video/quicktime", nil
	default:
		return "", "", fmt.Errorf("oreate: unsupported reference %s format", kind)
	}
}

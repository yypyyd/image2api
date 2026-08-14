package oreate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func TestFetchUploadCredentials(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != uploadTokenPath {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Cookie") != "ouss=session" {
			t.Errorf("Cookie header missing")
		}
		var request uploadTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("decode upload request: %v", err)
		}
		if request.Source != "aiImage" || len(request.Files) != 1 || request.Files[0].Filename != "reference-1" || request.Files[0].FileExt != "png" || request.Files[0].Size != 4 {
			t.Errorf("upload request = %#v", request)
		}
		_, _ = io.WriteString(w, `{"status":{"code":0},"data":{"KeyList":{"reference-1.png":{"bucket":"ot-pt","objectPath":"aiimage/upload/object.png","sessionkey":"temporary-token"}}}}`)
	}))
	defer server.Close()

	client := NewClient("")
	client.baseURL = server.URL
	got, err := client.fetchUploadCredentials(context.Background(), Account{Cookie: "ouss=session"}, []preparedUpload{{
		Reference: MediaReference{Data: []byte("data"), ContentType: "image/png"}, Filename: "reference-1.png", Extension: "png", Kind: "image",
	}})
	if err != nil || got["reference-1.png"].ObjectPath != "aiimage/upload/object.png" {
		t.Fatalf("fetchUploadCredentials() = %#v, %v", got, err)
	}
}

func TestFetchUploadCredentialsRejectsInvalidResponses(t *testing.T) {
	responses := []string{
		`not-json`,
		`{"status":{"code":0},"data":{}}`,
		`{"status":{"code":0},"data":{"KeyList":{"reference-1.png":{"bucket":"bad/bucket","objectPath":"x","sessionkey":"token"}}}}`,
		`{"status":{"code":0},"data":{"KeyList":{"reference-1.png":{"bucket":"ot-pt","objectPath":"","sessionkey":"token"}}}}`,
		`{"status":{"code":0},"data":{"KeyList":{"reference-1.png":{"bucket":"ot-pt","objectPath":"x","sessionkey":""}}}}`,
	}
	for _, response := range responses {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(w, response)
		}))
		client := NewClient("")
		client.baseURL = server.URL
		_, err := client.fetchUploadCredentials(context.Background(), Account{Cookie: "ouss=session"}, []preparedUpload{{
			Reference: MediaReference{Data: []byte("data")}, Filename: "reference-1.png", Extension: "png",
		}})
		server.Close()
		if !errors.Is(err, ErrTemporaryUpstream) {
			t.Fatalf("response %q error = %v", response, err)
		}
	}
}

func TestUploadObjectUsesValidatedGoogleResumableSession(t *testing.T) {
	testBearer := strings.Repeat("t", 24)
	data := []byte("media-bytes")
	calls := 0
	client := NewClient("")
	client.directClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if req.URL.Host != "storage.googleapis.com" || req.Header.Get("Authorization") != "Bearer "+testBearer {
			t.Errorf("upload request host/auth mismatch")
		}
		switch req.Method {
		case http.MethodPost:
			if req.URL.Query().Get("uploadType") != "resumable" || req.URL.Query().Get("name") != "aiimage/upload/object.mp4" {
				t.Errorf("init query = %q", req.URL.RawQuery)
			}
			if req.Header.Get("X-Upload-Content-Type") != "video/mp4" || req.Header.Get("X-Upload-Content-Length") != "11" {
				t.Errorf("init content headers = %#v", req.Header)
			}
			return testHTTPResponse(http.StatusOK, http.Header{"Location": {"https://storage.googleapis.com/resumable/session-id"}}, nil), nil
		case http.MethodPut:
			body, _ := io.ReadAll(req.Body)
			if !bytes.Equal(body, data) || req.Header.Get("Content-Type") != "video/mp4" {
				t.Errorf("PUT body/content type mismatch")
			}
			return testHTTPResponse(http.StatusOK, nil, []byte(`{}`)), nil
		default:
			t.Fatalf("unexpected method %s", req.Method)
			return nil, nil
		}
	})}
	err := client.uploadObject(context.Background(), uploadCredential{
		Bucket: "ot-pt", ObjectPath: "aiimage/upload/object.mp4", SessionKey: testBearer,
	}, MediaReference{Data: data, ContentType: "video/mp4"})
	if err != nil || calls != 2 {
		t.Fatalf("uploadObject() error/calls = %v/%d", err, calls)
	}
}

func TestUploadObjectDoesNotLeakBearerToken(t *testing.T) {
	testBearer := strings.Repeat("n", 24)
	client := NewClient("")
	client.directClient = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network unavailable")
	})}
	err := client.uploadObject(context.Background(), uploadCredential{Bucket: "ot-pt", ObjectPath: "object", SessionKey: testBearer}, MediaReference{Data: []byte("x"), ContentType: "image/png"})
	if err == nil || strings.Contains(err.Error(), testBearer) {
		t.Fatalf("uploadObject() error leaked credential: %v", err)
	}
}

func TestUploadObjectRejectsRedirects(t *testing.T) {
	redirectTargetCalled := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectTargetCalled = true
	}))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", target.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer source.Close()

	client := NewClient("")
	client.directClient = source.Client()
	client.directClient.Transport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(source.URL, "http://")
		return http.DefaultTransport.RoundTrip(req)
	})
	err := client.uploadObject(context.Background(), uploadCredential{
		Bucket: "ot-pt", ObjectPath: "object", SessionKey: strings.Repeat("r", 24),
	}, MediaReference{Data: []byte("x"), ContentType: "image/png"})
	if err == nil || redirectTargetCalled {
		t.Fatalf("uploadObject() redirect error/called = %v/%v", err, redirectTargetCalled)
	}
}

func TestValidateResumeURL(t *testing.T) {
	valid := "https://storage.googleapis.com/upload/storage/v1/b/ot-pt/o?upload_id=abc"
	if got, err := validateResumeURL(valid); err != nil || got != valid {
		t.Fatalf("validateResumeURL(valid) = %q, %v", got, err)
	}
	for _, raw := range []string{
		"", "http://storage.googleapis.com/upload", "https://evil.example/upload",
		"https://storage.googleapis.com.evil.example/upload", "https://user@storage.googleapis.com/upload",
		"https://storage.googleapis.com:443/upload",
	} {
		if _, err := validateResumeURL(raw); !errors.Is(err, ErrTemporaryUpstream) {
			t.Fatalf("validateResumeURL(%q) error = %v", raw, err)
		}
	}
}

func testHTTPResponse(status int, header http.Header, body []byte) *http.Response {
	if header == nil {
		header = make(http.Header)
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(bytes.NewReader(body))}
}

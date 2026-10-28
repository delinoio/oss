// SPDX-License-Identifier: Apache-2.0
package artifacts

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
)

// VideoFetch must use the service's guarded physical-send transport. It must
// validate current authorization, share the original send budget, resolve and
// pin approved IPs, reject redirects, and honor request cancellation. It must
// not attach provider credentials, cookies, or proxy environment configuration.
// There is deliberately no default transport for indirect provider URLs.
type VideoFetch func(*http.Request) (*http.Response, error)

// DownloadVideoResponse fetches untrusted indirect URLs without credentials and
// validates the entire batch before reserving or publishing any object. It only
// accepts HTTPS MP4 responses. URL strings and response content are never logged.
// The caller uses SignedURL on returned IDs, never the original provider URL.
func (s *Service) DownloadVideoResponse(ctx context.Context, owner Owner, urls []string, fetch VideoFetch) ([]Artifact, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, UploadTimeout)
	defer cancel()
	if !validOwner(owner) || fetch == nil || len(urls) == 0 {
		return nil, ErrInvalid
	}
	if len(urls) > MaxFiles {
		return nil, ErrLimit
	}
	// Validate the complete set before the first send, including credential-bearing URLs.
	for _, raw := range urls {
		u, err := url.Parse(raw)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
			return nil, ErrInvalid
		}
	}
	videos := make([]Video, 0, len(urls))
	var total int64
	for _, raw := range urls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		remaining := int64(MaxVideoResponseBytes) - total
		if remaining <= 0 {
			return nil, ErrLimit
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
		if err != nil {
			return nil, ErrInvalid
		}
		req.Header.Set("Accept", "video/mp4")
		response, err := fetch(req)
		if err != nil {
			if response != nil && response.Body != nil {
				response.Body.Close()
			}
			return nil, err
		}
		if response == nil || response.Body == nil {
			return nil, ErrInvalid
		}
		data, err := readVideo(ctx, response, min(int64(MaxVideoBytes), remaining))
		if err != nil {
			return nil, err
		}
		total += int64(len(data))
		videos = append(videos, Video{Data: data, ContentType: "video/mp4"})
	}
	return s.StoreVideoResponse(ctx, owner, videos)
}
func readVideo(ctx context.Context, response *http.Response, limit int64) ([]byte, error) {
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, ErrInvalid
	}
	contentType, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || contentType != "video/mp4" {
		return nil, ErrInvalid
	}
	if response.ContentLength > limit {
		return nil, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(contextReader{ctx, response.Body}, limit+1))
	if err != nil {
		return nil, err
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrLimit
	}
	if len(data) == 0 {
		return nil, ErrInvalid
	}
	if response.ContentLength >= 0 && response.ContentLength != int64(len(data)) {
		return nil, errors.New("incomplete video download")
	}
	return data, nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

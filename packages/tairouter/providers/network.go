// SPDX-License-Identifier: Apache-2.0
package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/delinoio/oss/packages/tairouter/core"
	"io"
	"net/http"
	"net/url"
	"time"
	"unicode/utf8"
)

func (a *Auth) requestBytes(ctx context.Context, s core.Scope, method, endpoint string, body any, headers http.Header, limit int64) ([]byte, int, http.Header, error) {
	if !validScope(s) {
		return nil, 0, nil, ErrScope
	}
	if a.executor == nil {
		return nil, 0, nil, ErrNetwork
	}
	if e := ctx.Err(); e != nil {
		return nil, 0, nil, e
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var data []byte
	binaryResponse := false
	ct := "application/json"
	switch b := body.(type) {
	case []byte:
		binaryResponse = true
		data = b
		ct = "application/proto"
	case url.Values:
		data = []byte(b.Encode())
		ct = "application/x-www-form-urlencoded"
	case nil:
	default:
		var e error
		data, e = json.Marshal(body)
		if e != nil {
			return nil, 0, nil, ErrResponse
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(data))
	if e != nil {
		return nil, 0, nil, ErrResponse
	}
	req.Header = headers.Clone()
	if req.Header == nil {
		req.Header = make(http.Header)
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "TaiRouter")
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", ct)
	}
	res, e := a.executor.Do(ctx, s, req)
	if e != nil {
		if ctx.Err() != nil {
			return nil, 0, nil, ctx.Err()
		}
		return nil, 0, nil, ErrNetwork
	}
	if res == nil || res.Body == nil {
		return nil, 0, nil, ErrResponse
	}
	defer res.Body.Close()
	stop := context.AfterFunc(ctx, func() { _ = res.Body.Close() })
	defer stop()
	if res.StatusCode >= 300 && res.StatusCode < 400 {
		return nil, res.StatusCode, nil, ErrResponse
	}
	if res.ContentLength > limit {
		return nil, res.StatusCode, nil, ErrResponse
	}
	b, e := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if ctx.Err() != nil {
		return nil, res.StatusCode, nil, ctx.Err()
	}
	if e != nil || int64(len(b)) > limit || (!binaryResponse && !utf8.Valid(b)) {
		return nil, res.StatusCode, nil, ErrResponse
	}
	return b, res.StatusCode, res.Header.Clone(), nil
}

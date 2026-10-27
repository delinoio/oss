// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/artifacts"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/hosted"
)

func hostedString(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func hostedMediaBody(args, decl map[string]any, model string, video bool) (map[string]any, string, error) {
	prompt := hostedString(args, "prompt")
	if strings.TrimSpace(prompt) == "" {
		return nil, "", inference.ErrUnsupported
	}
	body := map[string]any{"model": model, "prompt": prompt}
	path := "/images/generations"
	if video {
		path = "/videos/generations"
		if d, ok := args["duration"].(float64); ok {
			if d < 1 || d > 15 || math.Trunc(d) != d {
				return nil, "", inference.ErrUnsupported
			}
			body["duration"] = d
		}
		if r := hostedString(args, "resolution"); r != "" {
			if r != "480p" && r != "720p" {
				return nil, "", inference.ErrUnsupported
			}
			body["resolution"] = r
		}
		if a := hostedString(args, "aspect_ratio"); a != "" {
			switch a {
			case "16:9", "9:16", "1:1", "4:3", "3:4", "3:2", "2:3":
				body["aspect_ratio"] = a
			default:
				return nil, "", inference.ErrUnsupported
			}
		}
		return body, path, nil
	}
	n := float64(1)
	if v, ok := args["n"].(float64); ok {
		n = v
	}
	if n < 1 || n > 4 || math.Trunc(n) != n {
		return nil, "", inference.ErrUnsupported
	}
	body["n"] = n
	value := func(k string) string {
		if v, ok := args[k].(string); ok {
			return v
		}
		return hostedString(decl, k)
	}
	ratio := value("aspect_ratio")
	if strings.TrimSpace(ratio) == "" {
		size := strings.Split(value("size"), "x")
		if len(size) == 2 {
			w, e := strconv.ParseFloat(size[0], 64)
			h, f := strconv.ParseFloat(size[1], 64)
			if e == nil && f == nil && w > 0 && h > 0 {
				best := math.Inf(1)
				for _, p := range []struct {
					name string
					v    float64
				}{{"1:1", 1}, {"3:4", .75}, {"4:3", 4.0 / 3}, {"9:16", 9.0 / 16}, {"16:9", 16.0 / 9}} {
					d := math.Abs(math.Log(w / h / p.v))
					if d < best {
						best = d
						ratio = p.name
					}
				}
			}
		}
	}
	switch strings.TrimSpace(ratio) {
	case "1:1", "3:4", "4:3", "9:16", "16:9":
		body["aspect_ratio"] = strings.TrimSpace(ratio)
	}
	switch strings.ToLower(value("quality")) {
	case "hd", "high":
		body["resolution"] = "2k"
	case "standard", "low", "auto":
		body["resolution"] = "1k"
	}
	image := hostedString(args, "image_url")
	if image == "" {
		image = hostedString(args, "image")
	}
	if image != "" {
		path = "/images/edits"
		body["image"] = map[string]any{"url": image, "type": "image_url"}
	}
	return body, path, nil
}

const (
	hostedDownloadConnectTimeout = 10 * time.Second
	hostedDownloadIdleTimeout    = 60 * time.Second
)

func (o *hostedOperation) download(ctx context.Context, raw string, limit int64) (artifacts.Image, error) {
	return o.downloadWithTimeouts(ctx, raw, limit, hostedDownloadConnectTimeout, hostedDownloadIdleTimeout)
}

// The pinned artifacts transport bounds connection setup and inactivity, not the
// total download duration. Keep the caller's image/video deadline and cancellation;
// the guarded transport still owns destination checks and the physical-send gate.
func (o *hostedOperation) downloadWithTimeouts(ctx context.Context, raw string, limit int64, connect, idle time.Duration) (artifacts.Image, error) {
	ctx = inference.WithSupportTimeouts(ctx, inference.SupportTimeouts{Connect: connect, Idle: idle})
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Fragment != "" {
		return artifacts.Image{}, inference.ErrDestination
	}
	// Indirect media origins receive no provider headers or credentials.
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if e != nil {
		return artifacts.Image{}, e
	}
	b, h, e := o.transport.exchange(ctx, req, limit)
	if e != nil {
		return artifacts.Image{}, e
	}
	typ, _, _ := mime.ParseMediaType(h.Get("Content-Type"))
	if typ == "" || typ == "application/octet-stream" {
		typ = http.DetectContentType(b)
	}
	return artifacts.Image{Data: b, ContentType: typ}, nil
}
func (o *hostedOperation) publish(ctx context.Context, items []artifacts.Image, video bool) (hosted.OperationResult, error) {
	var size int64
	for _, item := range items {
		size += int64(len(item.Data))
	}
	o.resolver.mu.Lock()
	total := &o.resolver.imageBytes
	limit := int64(100 << 20)
	if video {
		total = &o.resolver.videoBytes
		limit = 600 << 20
	}
	if *total+size > limit {
		o.resolver.mu.Unlock()
		return hosted.OperationResult{}, artifacts.ErrLimit
	}
	*total += size
	o.resolver.mu.Unlock()
	var rows []artifacts.Artifact
	var e error
	if o.resolver.artifacts == nil {
		return hosted.OperationResult{}, inference.ErrUnavailable
	}
	if e = o.resolver.auth.BeforeSend(ctx, o.request.Principal); e != nil {
		return hosted.OperationResult{}, e
	}
	if video {
		videos := make([]artifacts.Video, len(items))
		for i, item := range items {
			videos[i] = artifacts.Video{Data: item.Data, ContentType: item.ContentType}
		}
		rows, e = o.resolver.artifacts.StoreVideoResponse(ctx, artifactOwner(o.request.Principal), videos)
	} else {
		rows, e = o.resolver.artifacts.StoreResponse(ctx, artifactOwner(o.request.Principal), items)
	}
	if e != nil {
		return hosted.OperationResult{}, e
	}
	result := hosted.OperationResult{Output: "Generated media is available in the attached artifacts."}
	for _, row := range rows {
		if e = o.resolver.auth.BeforeSend(ctx, o.request.Principal); e != nil {
			return hosted.OperationResult{}, e
		}
		signed, e := o.resolver.artifacts.SignedURL(ctx, artifactOwner(o.request.Principal), row.ID)
		if e != nil {
			return hosted.OperationResult{}, e
		}
		u, e := url.Parse(signed)
		if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return hosted.OperationResult{}, inference.ErrDestination
		}
		result.Artifacts = append(result.Artifacts, hosted.Artifact{ID: row.ID, URL: signed, ContentType: row.ContentType})
	}
	return result, nil
}
func (o *hostedOperation) image(ctx context.Context, args map[string]any) (hosted.OperationResult, error) {
	timeout := 300 * time.Second
	if v := o.settings.Images.TimeoutMs; v != nil {
		timeout = time.Duration(*v) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var decl map[string]any
	_ = json.Unmarshal(o.request.Declaration, &decl)
	body, path, e := hostedMediaBody(args, decl, o.target.Model, false)
	if e != nil {
		return hosted.OperationResult{}, e
	}
	call, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	b, e := o.transport.json(call, http.MethodPost, path, o.credential.Secret, body, 200<<20)
	cancelCall()
	if e != nil {
		return hosted.OperationResult{}, e
	}
	var response struct {
		Data []struct {
			Base64 string `json:"b64_json"`
			URL    string `json:"url"`
		}
	}
	if json.Unmarshal(b, &response) != nil || len(response.Data) == 0 || len(response.Data) > 4 {
		return hosted.OperationResult{}, inference.ErrUnavailable
	}
	var items []artifacts.Image
	total := 0
	for _, entry := range response.Data {
		var item artifacts.Image
		if entry.Base64 != "" {
			if base64.StdEncoding.DecodedLen(len(entry.Base64)) > 50<<20 {
				return hosted.OperationResult{}, artifacts.ErrLimit
			}
			item.Data, e = base64.StdEncoding.DecodeString(entry.Base64)
			item.ContentType = http.DetectContentType(item.Data)
		} else {
			item, e = o.download(ctx, entry.URL, 50<<20)
		}
		if e != nil {
			return hosted.OperationResult{}, e
		}
		total += len(item.Data)
		if total > 100<<20 {
			return hosted.OperationResult{}, artifacts.ErrLimit
		}
		items = append(items, item)
	}
	return o.publish(ctx, items, false)
}
func (o *hostedOperation) video(ctx context.Context, args map[string]any) (hosted.OperationResult, error) {
	timeout := 300 * time.Second
	if v := o.settings.Images.VideoTimeoutMs; v != nil {
		timeout = time.Duration(*v) * time.Millisecond
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	body, path, e := hostedMediaBody(args, nil, o.target.Model, true)
	if e != nil {
		return hosted.OperationResult{}, e
	}
	call, cancelCall := context.WithTimeout(ctx, 60*time.Second)
	b, e := o.transport.json(call, http.MethodPost, path, o.credential.Secret, body, 1<<20)
	cancelCall()
	if e != nil {
		return hosted.OperationResult{}, e
	}
	var submitted struct {
		RequestID string `json:"request_id"`
		ID        string `json:"id"`
	}
	if json.Unmarshal(b, &submitted) != nil {
		return hosted.OperationResult{}, inference.ErrUnavailable
	}
	id := submitted.RequestID
	if id == "" {
		id = submitted.ID
	}
	if id == "" || len(id) > 1024 {
		return hosted.OperationResult{}, inference.ErrUnavailable
	}
	delay := 5 * time.Second
	for {
		if e = o.wait(ctx, delay); e != nil {
			return hosted.OperationResult{}, e
		}
		call, cancelCall = context.WithTimeout(ctx, 30*time.Second)
		b, e = o.transport.json(call, http.MethodGet, "/videos/"+url.PathEscape(id), o.credential.Secret, nil, 1<<20)
		cancelCall()
		if e != nil {
			return hosted.OperationResult{}, e
		}
		var state struct {
			Status string `json:"status"`
			State  string `json:"state"`
			Video  struct {
				URL string `json:"url"`
			} `json:"video"`
			Videos []struct {
				URL string `json:"url"`
			} `json:"videos"`
		}
		if json.Unmarshal(b, &state) != nil {
			return hosted.OperationResult{}, inference.ErrUnavailable
		}
		status := state.Status
		if status == "" {
			status = state.State
		}
		switch strings.ToLower(status) {
		case "done", "completed", "succeeded":
			raw := state.Video.URL
			if raw == "" && len(state.Videos) > 0 {
				raw = state.Videos[0].URL
			}
			if raw == "" {
				return hosted.OperationResult{}, inference.ErrUnavailable
			}
			item, e := o.download(ctx, raw, 200<<20)
			if e != nil {
				return hosted.OperationResult{}, e
			}
			return o.publish(ctx, []artifacts.Image{item}, true)
		case "failed", "error", "expired", "timeout":
			return hosted.OperationResult{Failed: true, Output: "Video generation did not complete."}, nil
		}
		delay = delay * 3 / 2
		if delay > 15*time.Second {
			delay = 15 * time.Second
		}
	}
}

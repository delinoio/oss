// SPDX-License-Identifier: Apache-2.0
package cursor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

// VisionPreparer is supplied by the host for models known to lack image input.
// The host must authorize and account for sidecar sends through its central
// executor. It returns the complete request with active images replaced by
// observations. It must preserve the principal, target and tool authorization.
type VisionPreparer func(context.Context, inference.AdapterInput, inference.AdapterExecutor) (json.RawMessage, error)

func NewWithVision(prepare VisionPreparer) *Adapter { return &Adapter{vision: prepare} }

func noNativeVision(model string) bool {
	model = strings.TrimPrefix(model, "cursor/")
	base := parseModel(model).base
	switch base {
	case "auto", "default", "auto-cost", "auto-balance", "auto-intelligence", "composer-1", "composer-2.5", "composer-2.5-fast", "glm-5.2", "glm-5.3":
		return true
	}
	return false
}
func imageBody(in inference.AdapterInput) (map[string]any, error) {
	if len(in.Body) > MaxRequestBytes {
		return nil, &inference.Error{Status: 400, Code: "cursor_request_too_large"}
	}
	var body map[string]any
	dec := json.NewDecoder(bytes.NewReader(in.Body))
	dec.UseNumber()
	if !json.Valid(in.Body) || dec.Decode(&body) != nil || body == nil {
		return nil, &inference.Error{Status: 400, Code: "cursor_invalid_request"}
	}
	return body, nil
}
func (a *Adapter) Prepare(ctx context.Context, in inference.AdapterInput, x inference.AdapterExecutor) (json.RawMessage, error) {
	body, err := imageBody(in)
	if err != nil {
		return nil, err
	}
	if len(activeImages(body, in.Wire)) > 12 {
		return nil, &inference.Error{Status: 400, Code: "cursor_image_count_limit"}
	}
	if !noNativeVision(in.Target.Model) || len(activeImages(body, in.Wire)) == 0 {
		return in.Body, nil
	}
	if a.vision == nil {
		return nil, &inference.Error{Status: 501, Code: "cursor_vision_preparer_required"}
	}
	prepared, err := a.vision(ctx, in, x)
	if err != nil {
		return nil, err
	}
	verify := in
	verify.Body = prepared
	body, err = imageBody(verify)
	if err != nil {
		return nil, err
	}
	if len(activeImages(body, in.Wire)) != 0 {
		return nil, &inference.Error{Status: 501, Code: "cursor_vision_preparation_incomplete"}
	}
	return prepared, nil
}

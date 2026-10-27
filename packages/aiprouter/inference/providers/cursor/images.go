// SPDX-License-Identifier: Apache-2.0
// Image bounds and wire fields adapted from the pinned MIT source; see LICENSE.upstream.
package cursor

import (
	"bytes"
	"context"
	"encoding/base64"
	"image"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"
	"math"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const imageAttached = "[image attached]"
const imageOmitted = "[image omitted: undecodable or unsupported type]"

type imageInput struct{ url, detail string }

func contentImages(content any, out *[]imageInput) {
	for _, entry := range array(content) {
		part := object(entry)
		switch str(part["type"]) {
		case "image_url", "input_image":
			url := str(part["image_url"])
			detail := str(part["detail"])
			if value := object(part["image_url"]); value != nil {
				url = str(value["url"])
				detail = str(value["detail"])
			}
			*out = append(*out, imageInput{url, detail})
		case "image":
			source := object(part["source"])
			url := str(source["url"])
			if str(source["type"]) == "base64" {
				url = "data:" + str(source["media_type"]) + ";base64," + str(source["data"])
			}
			*out = append(*out, imageInput{url, str(part["detail"])})
		case "tool_result":
			contentImages(part["content"], out)
		}
	}
}
func activeImages(body map[string]any, wire inference.Wire) []imageInput {
	messages := array(body["messages"])
	if wire == inference.Responses {
		messages = array(body["input"])
	}
	var out []imageInput
	if len(messages) == 0 {
		return out
	}
	last := object(messages[len(messages)-1])
	// Tool-result image attachment is explicit. Historical images remain markers.
	toolImages, _ := body["cursor_tool_result_images"].(bool)
	if str(last["role"]) == "tool" || str(last["type"]) == "function_call_output" {
		if !toolImages {
			return nil
		}
		start := len(messages) - 1
		for start > 0 {
			previous := object(messages[start-1])
			if str(previous["role"]) != "tool" && str(previous["type"]) != "function_call_output" {
				break
			}
			start--
		}
		for _, entry := range messages[start:] {
			m := object(entry)
			if str(m["type"]) == "function_call_output" {
				contentImages(m["output"], &out)
			} else {
				contentImages(m["content"], &out)
			}
		}
		return out
	}
	if role := str(last["role"]); role == "user" || role == "developer" {
		contentImages(last["content"], &out)
	}
	return out
}
func prepareImage(ctx context.Context, in imageInput) ([]byte, int, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, 0, 0, err
	}
	// The pin deliberately omits remote URLs. Never fetch arbitrary caller URLs.
	if !strings.HasPrefix(in.url, "data:") {
		return nil, 0, 0, nil
	}
	header, payload, ok := strings.Cut(in.url, ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		return nil, 0, 0, nil
	}
	mime := strings.TrimSuffix(strings.TrimPrefix(header, "data:"), ";base64")
	switch mime {
	case "image/jpeg", "image/jpg", "image/png", "image/gif", "image/webp":
	default:
		return nil, 0, 0, nil
	}
	if base64.StdEncoding.DecodedLen(len(payload)) > 16<<20 {
		return nil, 0, 0, nil
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		return nil, 0, 0, nil
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 8192 || cfg.Height > 8192 || int64(cfg.Width)*int64(cfg.Height) > 25000000 {
		return nil, 0, 0, nil
	}
	decoded, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, 0, 0, nil
	}
	if err = ctx.Err(); err != nil {
		return nil, 0, 0, err
	}
	target := 100 << 10
	qualities := []int{85, 70, 55, 40}
	if in.detail == "high" || in.detail == "original" {
		target = 256 << 10
		qualities = []int{90, 80, 65, 50}
	}
	if (mime == "image/jpeg" || mime == "image/jpg") && format == "jpeg" && len(data) <= target {
		return data, cfg.Width, cfg.Height, nil
	}
	w, h := cfg.Width, cfg.Height
	if edge := max(w, h); edge > 2000 {
		scale := 2000 / float64(edge)
		w = max(1, int(math.Round(float64(w)*scale)))
		h = max(1, int(math.Round(float64(h)*scale)))
	}
	var best []byte
	bestW, bestH := w, h
	encode := func(w, h, q int) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var src image.Image = decoded
		if w != cfg.Width || h != cfg.Height {
			scaled := image.NewRGBA(image.Rect(0, 0, w, h))
			draw.CatmullRom.Scale(scaled, scaled.Bounds(), decoded, decoded.Bounds(), draw.Src, nil)
			src = scaled
		}
		var out bytes.Buffer
		if err := jpeg.Encode(&out, src, &jpeg.Options{Quality: q}); err != nil {
			return nil, err
		}
		return out.Bytes(), ctx.Err()
	}
	for _, q := range qualities {
		data, err = encode(w, h, q)
		if err != nil {
			return nil, 0, 0, err
		}
		if best == nil || len(data) < len(best) {
			best, bestW, bestH = data, w, h
		}
		if len(data) <= target {
			return data, w, h, nil
		}
	}
	for len(best) > target && max(w, h) > 256 {
		scale := 0.85
		if float64(max(w, h))*scale < 256 {
			scale = 256 / float64(max(w, h))
		}
		w = max(1, int(math.Round(float64(w)*scale)))
		h = max(1, int(math.Round(float64(h)*scale)))
		data, err = encode(w, h, qualities[len(qualities)-1])
		if err != nil {
			return nil, 0, 0, err
		}
		if len(data) < len(best) {
			best, bestW, bestH = data, w, h
		}
	}
	if len(best) > 1<<20 {
		return nil, 0, 0, nil
	}
	return best, bestW, bestH, nil
}
func (s *Session) selectedImages(ctx context.Context, body map[string]any, wire inference.Wire) ([]byte, string, error) {
	inputs := activeImages(body, wire)
	if len(inputs) > 12 {
		return nil, "", &inference.Error{Status: 400, Code: "cursor_image_count_limit"}
	}
	var selected []byte
	var notes []string
	for _, input := range inputs {
		data, w, h, err := prepareImage(ctx, input)
		if err != nil {
			return nil, "", err
		}
		if len(data) == 0 {
			notes = append(notes, imageOmitted)
			continue
		}
		blob, err := s.replayBlob(data)
		if err != nil {
			return nil, "", err
		}
		id, err := newID()
		if err != nil {
			return nil, "", err
		}
		item := join(ps(2, id), ps(3, "attachment-"+id+".jpg"), pb(4, join(pv(1, uint64(w)), pv(2, uint64(h)))), ps(7, "image/jpeg"), pb(9, join(pb(1, blob), pb(2, data))))
		selected = append(selected, pb(1, item)...)
	}
	return selected, strings.Join(notes, "\n"), nil
}

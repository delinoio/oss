// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"math"
	"net/http"
	"strings"
	"unicode/utf16"
)

// TokenProjection describes reasoning actually serialized by a routed provider.
// Unknown custom adapters retain all fields conservatively; adapters may refine it.
type TokenProjection struct{ Text, Signature, Redacted bool }
type TokenProjector interface{ TokenProjection(Target) TokenProjection }

func estimatedTextTokens(text, model string) int64 {
	ratio := 4.0
	model = strings.ToLower(model)
	if strings.HasPrefix(model, "kiro") {
		ratio = 2.8
	} else {
		for _, p := range []string{"claude", "deepseek", "minimax", "glm", "qwen"} {
			if strings.HasPrefix(model, p) {
				ratio = 3.5
				break
			}
		}
	}
	units := utf16.Encode([]rune(text))
	cjk := 0
	for _, c := range units {
		if c >= 0xac00 && c <= 0xd7a3 || c >= 0x1100 && c <= 0x11ff || c >= 0x3130 && c <= 0x318f || c >= 0x4e00 && c <= 0x9fff || c >= 0x3400 && c <= 0x4dbf || c >= 0x3040 && c <= 0x30ff {
			cjk++
		}
	}
	return int64(math.Ceil(float64(len(units)-cjk)/ratio + float64(cjk)/1.5))
}
func estimateMessageTokens(b object, model string, projection TokenProjection) int64 {
	attachments := int64(0)
	var blocks func([]any, bool) []any
	blocks = func(in []any, project bool) []any {
		out := make([]any, 0, len(in))
		for _, v := range in {
			m, ok := v.(map[string]any)
			if !ok {
				out = append(out, v)
				continue
			}
			m = clone(m)
			switch str(m["type"]) {
			case "thinking":
				if project && !projection.Text {
					continue
				}
				if project && !projection.Signature {
					delete(m, "signature")
				}
			case "redacted_thinking":
				if project && !projection.Redacted {
					continue
				}
			case "image", "document":
				src := obj(m["source"])
				if data, ok := src["data"].(string); ok && src["type"] == "base64" {
					count := int64(math.Ceil(float64(len(strings.TrimRight(data, "="))*3/4) / 512))
					if count < 256 {
						count = 256
					}
					raw, e := base64.StdEncoding.DecodeString(data)
					if e == nil {
						if dims, err := attachmentDimensions(raw); err == nil && dims.Width > 0 && dims.Height > 0 {
							count = int64(math.Ceil(float64(dims.Width) * float64(dims.Height) / 750))
							if count < 256 {
								count = 256
							}
						}
					}
					attachments += count
					src = clone(src)
					src["data"] = ""
					m["source"] = src
				}
			case "tool_result":
				if nested, ok := m["content"].([]any); ok {
					m["content"] = blocks(nested, false)
				}
			}
			out = append(out, m)
		}
		return out
	}
	parts := []string{}
	if system, ok := b["system"]; ok {
		if s, ok := system.(string); ok {
			parts = append(parts, s)
		} else {
			data, _ := json.Marshal(system)
			parts = append(parts, string(data))
		}
	}
	if messages, ok := b["messages"]; ok {
		projected := []any{}
		for _, v := range arr(messages) {
			m := clone(obj(v))
			if content, ok := m["content"].([]any); ok {
				m["content"] = blocks(content, true)
			}
			projected = append(projected, m)
		}
		data, _ := json.Marshal(projected)
		parts = append(parts, string(data))
	}
	if tools, ok := b["tools"]; ok {
		data, _ := json.Marshal(tools)
		parts = append(parts, string(data))
	}
	n := estimatedTextTokens(strings.Join(parts, "\n"), model) + attachments
	if n < 1 {
		n = 1
	}
	return n
}
func writeTokenEstimate(w http.ResponseWriter, b object, t Target, a Adapter) error {
	projection := TokenProjection{true, true, true}
	if t.Wire == Chat {
		projection = TokenProjection{}
	}
	if p, ok := a.(TokenProjector); ok {
		projection = p.TokenProjection(t)
	}
	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(object{"input_tokens": estimateMessageTokens(b, t.Model, projection)})
}

func attachmentDimensions(raw []byte) (image.Config, error) {
	if len(raw) >= 30 && string(raw[:4]) == "RIFF" && string(raw[8:12]) == "WEBP" {
		width, height := 0, 0
		switch string(raw[12:16]) {
		case "VP8X":
			width = 1 + int(raw[24]) + int(raw[25])<<8 + int(raw[26])<<16
			height = 1 + int(raw[27]) + int(raw[28])<<8 + int(raw[29])<<16
		case "VP8 ":
			if bytes.Equal(raw[23:26], []byte{0x9d, 1, 0x2a}) {
				width = int(binary.LittleEndian.Uint16(raw[26:28]) & 0x3fff)
				height = int(binary.LittleEndian.Uint16(raw[28:30]) & 0x3fff)
			}
		case "VP8L":
			if raw[20] == 0x2f {
				packed := binary.LittleEndian.Uint32(raw[21:25])
				width = int(packed&0x3fff) + 1
				height = int((packed>>14)&0x3fff) + 1
			}
		}
		if width > 0 && height > 0 {
			return image.Config{Width: width, Height: height}, nil
		}
	}
	config, _, e := image.DecodeConfig(bytes.NewReader(raw))
	return config, e
}

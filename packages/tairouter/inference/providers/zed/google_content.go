// SPDX-License-Identifier: Apache-2.0
package zed

import "strings"

func googleContent(v any) ([]any, error) {
	if s, ok := v.(string); ok {
		if s == "" {
			return nil, nil
		}
		return []any{object{"text": s}}, nil
	}
	out := []any{}
	for _, v := range arrayOf(v) {
		p := objectOf(v)
		switch p["type"] {
		case "text":
			out = append(out, object{"text": p["text"]})
		case "image_url":
			u := stringOf(objectOf(p["image_url"])["url"])
			if strings.HasPrefix(u, "data:") {
				head, data, ok := strings.Cut(u, ",")
				if !ok || !strings.HasSuffix(head, ";base64") {
					return nil, requestError("invalid_image_data")
				}
				out = append(out, object{"inlineData": object{"mimeType": strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), "data": data}})
			} else {
				out = append(out, object{"fileData": object{"fileUri": u, "mimeType": "image/jpeg"}})
			}
		case "file", "input_file":
			f := objectOf(p["file"])
			if f == nil {
				f = p
			}
			if native := objectOf(f["anthropic"]); native != nil {
				s := objectOf(native["source"])
				if s["type"] == "base64" {
					out = append(out, object{"inlineData": object{"mimeType": s["media_type"], "data": s["data"]}})
				} else if s["type"] == "text" {
					out = append(out, object{"text": s["data"]})
				} else {
					out = append(out, object{"fileData": object{"fileUri": s["url"], "mimeType": "application/pdf"}})
				}
			} else if u := stringOf(f["file_data"]); u != "" {
				head, data, ok := strings.Cut(u, ",")
				if !ok {
					return nil, requestError("invalid_document_data")
				}
				out = append(out, object{"inlineData": object{"mimeType": strings.TrimSuffix(strings.TrimPrefix(head, "data:"), ";base64"), "data": data}})
			} else {
				out = append(out, object{"fileData": object{"fileUri": f["file_url"], "mimeType": "application/pdf"}})
			}
		}
	}
	return out, nil
}

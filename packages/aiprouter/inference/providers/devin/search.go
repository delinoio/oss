// SPDX-License-Identifier: Apache-2.0
// Search mappings adapted from opencodex; see LICENSE-MIT.
package devin

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type searchResult struct {
	url, title, text, summary string
	chunks                    []string
}

func cleanText(b []byte) string {
	if !utf8.Valid(b) {
		return ""
	}
	return string(b)
}
func controls(s string) bool {
	for _, r := range s {
		if r <= 31 || r >= 127 && r <= 159 {
			return true
		}
	}
	return false
}

func searchResponse(b []byte) (object, error) {
	var parsed []searchResult
	summary := ""
	err := walk(b, func(f field) error {
		if f.wire != 2 {
			return nil
		}
		if f.num == 3 {
			summary = truncateUnits(strings.TrimSpace(cleanText(f.b)), 2000)
		}
		if f.num != 1 {
			return nil
		}
		if len(parsed) >= 1024 {
			return errLimit
		}
		var r searchResult
		err := walk(f.b, func(p field) error {
			if p.wire != 2 {
				return nil
			}
			switch p.num {
			case 2:
				r.text = cleanText(p.b)
			case 3:
				r.url = cleanText(p.b)
			case 4:
				r.title = cleanText(p.b)
			case 7:
				r.summary = cleanText(p.b)
			case 6:
				return walk(p.b, func(c field) error {
					if c.wire != 2 {
						return nil
					}
					if c.num == 1 {
						r.chunks = append(r.chunks, cleanText(c.b))
					}
					if c.num == 3 {
						return walk(c.b, func(md field) error {
							if md.num == 2 && md.wire == 2 {
								r.chunks = append(r.chunks, cleanText(md.b))
							}
							return nil
						})
					}
					return nil
				})
			}
			return nil
		})
		if err != nil {
			return err
		}
		parsed = append(parsed, r)
		return nil
	})
	if err != nil {
		return nil, err
	}
	results := []any{}
	lines := []string{}
	seen := map[string]bool{}
	used := 0
	// The pinned mapper uses the last excerpt for repeated URLs, but the first
	// safe citation title. Keep that behavior without trusting model-written URLs.
	last := map[string]searchResult{}
	for _, r := range parsed {
		last[r.url] = r
	}
	for _, r := range parsed {
		if len(results) >= 20 {
			break
		}
		u, err := url.Parse(r.url)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || len(r.url) > 2048 || r.url != strings.TrimSpace(r.url) || controls(r.url) || seen[r.url] {
			continue
		}
		title := r.title
		if strings.TrimSpace(title) == "" || len(title) > 256 || controls(title) {
			title = ""
		}
		source := object{"url": r.url}
		if title != "" {
			source["title"] = title
		}
		encoded, _ := json.Marshal(source)
		if used+len(encoded) > 16384 {
			continue
		}
		used += len(encoded)
		seen[r.url] = true
		if title == "" {
			title = r.url
		}
		excerpt := last[r.url].summary
		if excerpt == "" {
			excerpt = last[r.url].text
		}
		if excerpt == "" {
			excerpt = strings.Join(last[r.url].chunks, "\n")
		}
		excerpt = truncateUnits(strings.TrimSpace(excerpt), 2000)
		if excerpt == "" {
			excerpt = "(no excerpt)"
		}
		results = append(results, object{"url": r.url, "title": title})
		lines = append(lines, "- "+title+": "+excerpt+" ["+r.url+"]")
	}
	if len(results) == 0 {
		return nil, &inference.Error{Status: 502, Code: "devin_search_no_results"}
	}
	output := "Search results:\n" + strings.Join(lines, "\n")
	if summary != "" {
		output = summary + "\n\n" + output
	}
	return object{"encrypted_output": nil, "output": output, "results": results}, nil
}

func searchQuery(input object) (string, error) {
	var queries []string
	push := func(v any) {
		q := strings.TrimSpace(str(v))
		if q == "" {
			return
		}
		for _, old := range queries {
			if old == q {
				return
			}
		}
		queries = append(queries, truncateUnits(q, 1000))
	}
	for _, raw := range arr(obj(input["commands"])["search_query"]) {
		push(obj(raw)["q"])
	}
	if len(queries) == 0 {
		for _, key := range []string{"query", "q", "search_query"} {
			push(input[key])
			if len(queries) > 0 {
				break
			}
		}
	}
	if len(queries) == 0 {
		return "", badRequest()
	}
	if len(queries) > 1 {
		return "", &inference.Error{Status: 501, Code: "devin_search_batch_requires_executor"}
	}
	return queries[0], nil
}

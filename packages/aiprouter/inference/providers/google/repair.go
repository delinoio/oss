// SPDX-License-Identifier: Apache-2.0
// Repair rules adapt google-wire-compiler.ts and google-http.ts; see UPSTREAM-LICENSE.
package google

import (
	"bytes"
	"context"
	"encoding/json"
	"regexp"
	"strconv"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

var _ inference.RequestRepairer = (*Adapter)(nil)

var schemaRejection = regexp.MustCompile(`(?i)(?:input[_ ]schema|json schema|function[_ ]declarations?|x-mcp-header)`)
var thinkingRejection = regexp.MustCompile(`(?i)thinking[_ ]?(?:config|level)`)
var customSchemaIndex = regexp.MustCompile(`(?i)tools(?:\.|\[)(\d+)(?:\])?\.custom\.input_schema`)
var declarationIndex = regexp.MustCompile(`(?i)function[_]?declarations(?:\.|\[)(\d+)`)

// RepairRequest proposes one changed-body compatibility replay for CCA and Vertex.
// It never sends: the core must authorize and count the proposed request before
// dispatch, retain this attempt context, and forbid repair after client output.
func (a *Adapter) RepairRequest(ctx context.Context, in inference.AdapterInput, request inference.AdapterRequest, f inference.HTTPFailure) (inference.AdapterRequest, bool, error) {
	if err := ctx.Err(); err != nil {
		return request, false, err
	}
	// AI Studio intentionally has single-shot 400 behavior in the pinned source.
	if (!a.cca && !a.vertex) || f.Status != 400 || f.Repairs != 0 || len(f.Body) > 1<<20 || len(request.Body) > a.options.MaxBodyBytes {
		return request, false, nil
	}
	s, _ := ctx.Value(requestKey{}).(*requestState)
	if s == nil {
		return request, false, failure(500, "google_request_context_required")
	}
	s.mu.Lock()
	var observation *SchemaLossReport
	defer func() {
		s.mu.Unlock()
		if observation != nil && a.options.SchemaObserver != nil {
			a.options.SchemaObserver(ctx, observation.clone())
		}
	}()
	if !s.built || s.binding != replayBinding(in) || s.repaired {
		return request, false, nil
	}
	schema, thinking := schemaRejection.Match(f.Body), thinkingRejection.Match(f.Body)
	if !schema && !thinking {
		return request, false, nil
	}
	body, err := decodeJSON(request.Body)
	if err != nil {
		return request, false, nil
	}
	root := obj(body["request"])
	if root == nil {
		root = body
	}
	changed := false
	if thinking {
		if gc := obj(root["generationConfig"]); gc != nil {
			if _, ok := gc["thinkingConfig"]; ok {
				delete(gc, "thinkingConfig")
				if len(gc) == 0 {
					delete(root, "generationConfig")
				}
				changed = true
			}
		}
	}
	if schema {
		var declarations []object
		for _, tool := range array(root["tools"]) {
			for _, d := range array(obj(tool)["functionDeclarations"]) {
				if decl := obj(d); decl != nil {
					declarations = append(declarations, decl)
				}
			}
		}
		if len(declarations) > 0 {
			match := customSchemaIndex.FindSubmatch(f.Body)
			if match == nil {
				match = declarationIndex.FindSubmatch(f.Body)
			}
			if len(match) > 1 {
				if index, e := strconv.Atoi(string(match[1])); e == nil && index >= 0 && index < len(declarations) {
					declarations = declarations[index : index+1]
				}
			}
			report := SchemaLossReport{Version: 1, EndpointClass: "cloud-code-assist", Categories: map[string]int{}}
			if a.vertex {
				report.EndpointClass = "vertex"
			}
			report.add("repair-opened-schema", len(declarations))
			s.report.merge(report)
			observation = &report
			if a.options.SchemaPolicy == SchemaRejectLossy {
				return request, false, nil
			}
			for _, d := range declarations {
				d["parameters"] = object{"type": "object", "properties": object{}}
				delete(d, "parametersJsonSchema")
			}
			changed = true
		}
	}
	if !changed {
		return request, false, nil
	}
	data, err := json.Marshal(body)
	if err != nil {
		return request, false, invalid()
	}
	if len(data) > a.options.MaxBodyBytes || bytes.Equal(data, request.Body) {
		return request, false, nil
	}
	s.repaired = true
	request.Body = data
	return request, true, nil
}

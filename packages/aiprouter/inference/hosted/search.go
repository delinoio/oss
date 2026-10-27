// SPDX-License-Identifier: Apache-2.0
package hosted

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

// search splits a public multi-query request only when the selected adapter owns
// search. It keeps that exact credential and never switches to another sidecar.
func (a *Adapter) search(ctx context.Context, in inference.AdapterInput, body object, e inference.AdapterExecutor, s inference.AdapterStateStore) (*http.Response, inference.Wire, error) {
	qs, err := queries(body)
	if err != nil {
		return nil, "", err
	}
	if len(qs) > a.config.MaxSearches {
		return nil, "", fault(400, "hosted_search_limit")
	}
	if len(qs) == 1 {
		delete(body, "queries")
		body["query"] = qs[0]
		in.Body, _ = json.Marshal(body)
		return a.model(ctx, in, e, s)
	}
	outputs := []string{}
	results := []any{}
	for i, q := range qs {
		if err = ctx.Err(); err != nil {
			return nil, "", err
		}
		nextBody := object{}
		for k, v := range body {
			if k != "queries" {
				nextBody[k] = v
			}
		}
		nextBody["query"] = q
		next := in
		next.Body, _ = json.Marshal(nextBody)
		r, w, sendErr := a.model(ctx, next, e, s)
		if sendErr != nil {
			return nil, "", sendErr
		}
		if r == nil || r.Body == nil {
			return nil, "", fault(502, "hosted_invalid_response")
		}
		_, needsRecovery := r.Body.(*recoveryBody)
		if needsRecovery || r.StatusCode < 200 || r.StatusCode >= 300 {
			if i == 0 {
				return r, w, nil
			}
			r.Body.Close()
			return nil, "", fault(502, "hosted_search_batch_failed")
		}
		stop := context.AfterFunc(ctx, func() { r.Body.Close() })
		b, readErr := io.ReadAll(io.LimitReader(r.Body, a.config.MaxBytes+1))
		stop()
		r.Body.Close()
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if readErr != nil {
			return nil, "", readErr
		}
		if int64(len(b)) > a.config.MaxBytes {
			return nil, "", fault(502, "hosted_buffer_limit")
		}
		value, parseErr := decode(b)
		if parseErr != nil {
			return nil, "", fault(502, "hosted_invalid_search_result")
		}
		outputs = append(outputs, str(value["output"]))
		results = append(results, arr(value["results"])...)
		if x, ok := e.(inference.AttemptUsage); ok {
			x.RecordUsage(readUsage(obj(value["usage"])))
		}
		current, _ := json.Marshal(results)
		if int64(len(current)+len(strings.Join(outputs, "\n\n"))) > a.config.MaxBytes {
			return nil, "", fault(502, "hosted_buffer_limit")
		}
	}
	b, _ := json.Marshal(object{"encrypted_output": nil, "output": strings.Join(outputs, "\n\n"), "results": results})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(bytes.NewReader(b))}, inference.Chat, nil
}

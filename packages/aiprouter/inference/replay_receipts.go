// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"time"
)

type replayScopeKey struct{}

// signedReplayTokens traverses protocol fields, never tool arguments, schemas or
// ordinary text. Envelopes are unpacked so a wire translation cannot confer trust.
func signedReplayTokens(v any) ([]string, error) {
	tokens := map[string]bool{}
	var walk func(any, int, string) error
	add := func(s, binding string) error {
		if s == "" {
			return nil
		}
		if len(s) > 2<<20 || len(tokens) >= 256 {
			return &Error{400, "reasoning_receipt_limit", 0}
		}
		sum := sha256.Sum256([]byte(s + "\x00" + binding))
		tokens[hex.EncodeToString(sum[:])] = true
		return nil
	}
	walk = func(v any, depth int, binding string) error {
		if depth > 64 {
			return &Error{400, "reasoning_nesting_limit", 0}
		}
		switch x := v.(type) {
		case []any:
			for _, item := range x {
				if e := walk(item, depth+1, binding); e != nil {
					return e
				}
			}
		case map[string]any:
			switch str(x["type"]) {
			case "thinking":
				binding = str(x["thinking"])
			case "reasoning":
				binding = reasoningText(x)
			case "reasoning.text":
				binding = str(x["text"])
			case "redacted_thinking", "reasoning.encrypted":
				binding = ""
			}
			if text, ok := x["reasoning_content"].(string); ok {
				binding = text
			}
			if x["thought"] == true {
				binding = str(x["text"])
			}
			if str(x["type"]) == "tool_use" || str(x["type"]) == "function_call" || x["functionCall"] != nil || x["function"] != nil {
				identity, err := canonicalReplayCall(x)
				if err != nil {
					return err
				}
				data, _ := json.Marshal(identity)
				binding = string(data)
			}
			for k, value := range x {
				if k == "arguments" || k == "parameters" || k == "input_schema" || k == "tools" || k == "input" && x["type"] == "tool_use" {
					continue
				}
				if s, ok := value.(string); ok {
					if (k == "encrypted_content" || k == "signature" || k == "reasoning_signature") && strings.HasPrefix(s, reasoningPrefix) {
						envelope, e := decodeReasoning(s)
						if e != nil {
							return e
						}
						signedText := binding
						if envelope.Text != nil {
							signedText = *envelope.Text
						}
						for _, token := range []string{envelope.Signature, envelope.Kiro} {
							if e = add(token, signedText); e != nil {
								return e
							}
						}
						for _, token := range envelope.Redacted {
							if e = add(token, ""); e != nil {
								return e
							}
						}
						if enc := str(envelope.Native["enc"]); enc != "" {
							if e = add(enc, signedText); e != nil {
								return e
							}
						}
						if e = walk(envelope.Native, depth+1, signedText); e != nil {
							return e
						}
						continue
					}
					switch k {
					case "signature", "thoughtSignature", "thought_signature", "reasoning_signature", "kiro_reasoning", "encrypted_content":
						if e := add(s, binding); e != nil {
							return e
						}
					case "data":
						if x["type"] == "redacted_thinking" || x["type"] == "reasoning.encrypted" {
							if e := add(s, binding); e != nil {
								return e
							}
						}
					}
				} else if e := walk(value, depth+1, binding); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if e := walk(v, 0, ""); e != nil {
		return nil, e
	}
	out := make([]string, 0, len(tokens))
	for t := range tokens {
		out = append(out, t)
	}
	return out, nil
}
func (h *Handler) validateReplay(ctx context.Context, b object, scope scopedAdapterState) error {
	tokens, e := signedReplayTokens(b)
	if e != nil {
		return e
	}
	for _, token := range tokens {
		receipt, err := scope.Load(ctx, "signed-replay-v1:"+token)
		if err != nil || string(receipt) != token {
			return &Error{400, "unbound_signed_reasoning", 0}
		}
	}
	return nil
}

// CaptureReplayReceipts is called with the successful public response before terminal
// output. The context must be the prepared execution context supplied by Handler.
// Provider snapshots still validate canonical tool arguments and provider ordering.
func (h *Handler) CaptureReplayReceipts(ctx context.Context, out any) error {
	scope, ok := ctx.Value(replayScopeKey{}).(scopedAdapterState)
	if !ok {
		return nil
	}
	tokens, e := signedReplayTokens(out)
	if e != nil {
		return e
	}
	for _, token := range tokens {
		if e = scope.Save(ctx, "signed-replay-v1:"+token, []byte(token), time.Hour); e != nil {
			return e
		}
	}
	return nil
}

// CaptureReplayJSON is a convenience hook for bounded finalized stream JSON.
func (h *Handler) CaptureReplayJSON(ctx context.Context, raw json.RawMessage) error {
	var out any
	if e := json.Unmarshal(raw, &out); e != nil {
		return e
	}
	return h.CaptureReplayReceipts(ctx, out)
}

func canonicalReplayCall(x object) (object, error) {
	id := str(x["call_id"])
	if id == "" {
		id = str(x["id"])
	}
	name := str(x["name"])
	ns := str(x["namespace"])
	args := x["arguments"]
	if x["type"] == "tool_use" {
		args = x["input"]
	}
	if f := obj(x["function"]); f != nil {
		name = str(f["name"])
		args = f["arguments"]
	}
	if f := obj(x["functionCall"]); f != nil {
		name = str(f["name"])
		args = f["args"]
	}
	metadata := obj(x["provider_metadata"])
	if bare := str(metadata["tool_name"]); bare != "" {
		name = bare
	}
	if namespace := str(metadata["tool_namespace"]); namespace != "" {
		ns = namespace
	}
	if ns == "" {
		if parts := strings.SplitN(name, "__", 2); len(parts) == 2 {
			ns, name = parts[0], parts[1]
		}
	}
	if ns == "functions" {
		ns = ""
	}
	if raw, ok := args.(string); ok {
		d := json.NewDecoder(strings.NewReader(raw))
		d.UseNumber()
		if err := d.Decode(&args); err != nil {
			return nil, &Error{400, "invalid_signed_tool_arguments", 0}
		}
	}
	return object{"call_id": id, "name": name, "namespace": ns, "args": args}, nil
}
func issuerBoundHistory(v any) bool {
	switch x := v.(type) {
	case []any:
		for _, item := range x {
			if issuerBoundHistory(item) {
				return true
			}
		}
	case map[string]any:
		if str(x["file_id"]) != "" || str(x["upload_id"]) != "" {
			return true
		}
		if str(x["encrypted_content"]) != "" || str(x["signature"]) != "" || str(x["thought_signature"]) != "" || str(x["reasoning_signature"]) != "" || str(x["kiro_reasoning"]) != "" {
			return true
		}
		for k, item := range x {
			if k == "arguments" || k == "input_schema" || k == "parameters" || k == "tools" || k == "input" && x["type"] == "tool_use" {
				continue
			}
			if issuerBoundHistory(item) {
				return true
			}
		}
	}
	return false
}

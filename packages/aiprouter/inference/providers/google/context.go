// SPDX-License-Identifier: Apache-2.0
package google

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type requestKey struct{}
type requestState struct {
	mu        sync.Mutex
	built     bool
	repaired  bool
	aliases   map[string]string
	model     string
	wire      inference.Wire
	report    SchemaLossReport
	binding   string
	sessionID string
	observed  []replayEntry
	replay    []replayEntry
	complete  bool
}

// PrepareContext allocates state for one Build/Decode attempt. The executor must
// retain this context through Decode and allocate a fresh context before retry.
func (*Adapter) PrepareContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, requestKey{}, &requestState{})
}

// RequestSchemaReport returns content-free schema compilation diagnostics.
func RequestSchemaReport(ctx context.Context) (SchemaLossReport, bool) {
	s, _ := ctx.Value(requestKey{}).(*requestState)
	if s == nil {
		return SchemaLossReport{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.report.clone(), s.report.Version != 0
}

// DecodeWithInput supports executors that supply immutable attempt input instead
// of PrepareContext. Rebuilding only compiles JSON; it never dispatches a send.
func (a *Adapter) DecodeWithInput(ctx context.Context, in inference.AdapterInput, response *http.Response) (*http.Response, error) {
	if s, _ := ctx.Value(requestKey{}).(*requestState); s == nil {
		ctx = a.PrepareContext(ctx)
		if _, err := a.Build(ctx, in); err != nil {
			if response != nil && response.Body != nil {
				_ = response.Body.Close()
			}
			return nil, err
		}
	}
	return a.Decode(ctx, response)
}

func stateForResponse(ctx context.Context) *responseState {
	r := newResponseState()
	if s, _ := ctx.Value(requestKey{}).(*requestState); s != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		r.request = s
		r.model = s.model
		r.wire = s.wire
		r.aliases = make(map[string]string, len(s.aliases))
		for k, v := range s.aliases {
			r.aliases[k] = v
		}
	}
	return r
}

var wireToolName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
var unsafeToolCharacter = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// compileToolNames follows the pin's declaration-first allocation order. A hash
// suffix disambiguates names; the inverse map belongs to this request only.
func compileToolNames(body object) map[string]string {
	var names []string
	for _, t := range array(body["tools"]) {
		for _, d := range array(obj(t)["functionDeclarations"]) {
			names = append(names, text(obj(d)["name"]))
		}
	}
	for _, c := range array(body["contents"]) {
		for _, p := range array(obj(c)["parts"]) {
			for _, key := range []string{"functionCall", "functionResponse"} {
				if call := obj(obj(p)[key]); call != nil {
					names = append(names, text(call["name"]))
				}
			}
		}
	}
	to, from := map[string]string{}, map[string]string{}
	for _, name := range names {
		if _, ok := to[name]; ok {
			continue
		}
		if wireToolName.MatchString(name) && from[name] == "" {
			to[name] = name
			from[name] = name
			continue
		}
		clean := unsafeToolCharacter.ReplaceAllString(name, "_")
		if clean == "" || !(clean[0] >= 'A' && clean[0] <= 'Z' || clean[0] >= 'a' && clean[0] <= 'z' || clean[0] == '_') {
			clean = "_" + clean
		}
		if len(clean) > 55 {
			clean = clean[:55]
		}
		for salt := 0; ; salt++ {
			seed := name
			if salt > 0 {
				seed = fmt.Sprintf("%s#%d", name, salt)
			}
			h := sha256.Sum256([]byte(seed))
			alias := clean + "_" + hex.EncodeToString(h[:4])
			if from[alias] != "" {
				continue
			}
			to[name] = alias
			from[alias] = name
			break
		}
	}
	for _, t := range array(body["tools"]) {
		for _, d := range array(obj(t)["functionDeclarations"]) {
			decl := obj(d)
			decl["name"] = to[text(decl["name"])]
		}
	}
	for _, c := range array(body["contents"]) {
		for _, p := range array(obj(c)["parts"]) {
			for _, key := range []string{"functionCall", "functionResponse"} {
				if call := obj(obj(p)[key]); call != nil {
					call["name"] = to[text(call["name"])]
				}
			}
		}
	}
	config := obj(obj(body["toolConfig"])["functionCallingConfig"])
	for i, n := range array(config["allowedFunctionNames"]) {
		if alias, ok := to[text(n)]; ok {
			array(config["allowedFunctionNames"])[i] = alias
		}
	}
	return from
}

func validSourceToolName(s string) bool { return strings.TrimSpace(s) != "" && len(s) <= 4096 }

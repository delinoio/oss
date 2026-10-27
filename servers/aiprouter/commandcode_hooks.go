// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/inference/providers/commandcode"
	"github.com/delinoio/oss/packages/aiprouter/providers"
)

const commandCodeEffortsNamespace = "command-code-efforts"

type commandCodeEvidence struct {
	Accepted []string `json:"accepted"`
	Rejected []string `json:"rejected"`
}

func commandCodeState(in inference.AdapterInput) (inference.Principal, string, error) {
	p := in.Principal
	if in.Target.Wire != commandcode.Wire || in.Credential.AccountID == "" || in.Credential.CredentialVersion == "" {
		return p, "", inference.ErrUnauthorized
	}
	p.AccountID, p.CredentialVersion = in.Credential.AccountID, in.Credential.CredentialVersion
	raw, _ := json.Marshal([]string{p.OrganizationID, p.UserID, p.KeyID, p.AccountID, p.CredentialVersion, in.Target.Provider, in.Target.BaseURL, in.Target.Endpoint, string(in.Target.Wire), strings.ToLower(commandcode.CanonicalModel(in.Target.Model))})
	digest := sha256.Sum256(raw)
	return p, hex.EncodeToString(digest[:]), nil
}

func commandCodeAdapter(a *inferenceAdapter, destinations inference.DestinationPolicy) *commandcode.Adapter {
	return commandcode.NewWithOptions(commandcode.Options{
		Policy:       a.commandCodePolicy,
		LearnEfforts: a.learnCommandCodeEfforts,
		ProfileTransport: func(ctx context.Context, in inference.AdapterInput, req *http.Request) (*http.Response, error) {
			if _, _, err := commandCodeState(in); err != nil {
				return nil, err
			}
			// The support origin is credential-free and fixed by the adapter. Never reuse
			// selected credential headers or accept a user-provided redirect destination.
			if req == nil || req.URL == nil || req.Method != http.MethodGet || req.URL.Scheme != "https" || req.URL.Host != "commandcode.ai" || req.URL.User != nil || req.URL.RawQuery != "" || req.URL.Fragment != "" || !strings.HasPrefix(req.URL.Path, "/models/") || strings.Contains(req.URL.Path, "..") {
				return nil, inference.ErrDestination
			}
			clean := req.Clone(ctx)
			clean.Header = http.Header{"Accept": []string{"text/html"}}
			clean.Host = ""
			clean.Body = nil
			lease, err := a.current(in.Principal)
			if err != nil {
				return nil, err
			}
			return destinations.Do(ctx, clean, func(c context.Context, start func(context.Context) error) error {
				return inferenceError(a.s.WithAccountDispatch(c, lease, in.Credential.AccountID, in.Credential.CredentialVersion, start))
			}, nil)
		},
	})
}

func (a *inferenceAdapter) commandCodePolicy(ctx context.Context, in inference.AdapterInput) (commandcode.Policy, error) {
	p, id, err := commandCodeState(in)
	if err != nil {
		return commandcode.Policy{}, err
	}
	model := commandcode.CanonicalModel(in.Target.Model)
	out := commandcode.Policy{}
	if in.Target.Policy != nil {
		m := in.Target.Policy.ModelPolicy()
		if m.MaxOutputTokens != nil {
			out.DefaultMaxOutputTokens = int(*m.MaxOutputTokens)
		}
		if m.ReasoningEfforts != nil {
			out.ReasoningEfforts = map[string][]string{model: slices.Clone(m.ReasoningEfforts)}
		}
	}
	d, version, err := a.providerVersion(ctx, scopeOf(p), p.AccountID)
	if err != nil {
		return out, inferenceError(err)
	}
	if version != p.CredentialVersion {
		return out, inference.ErrUnauthorized
	}
	// Only fresh account discovery is authoritative. Shipped preset ladders remain
	// repairable; they must not suppress observed upstream effort refusals.
	if d.Models != nil && d.Models.State == providers.ValidationAccepted && !d.ModelsDiscoveredAt.IsZero() && !d.ModelsDiscoveredAt.After(time.Now()) && time.Since(d.ModelsDiscoveredAt) <= 6*time.Hour {
		for _, m := range d.Models.Models {
			if strings.EqualFold(commandcode.CanonicalModel(m.ID), model) && m.ReasoningEfforts != nil {
				out.Authoritative = true
				out.ReasoningEfforts = map[string][]string{model: slices.Clone(m.ReasoningEfforts)}
				break
			}
		}
	}
	value, found, err := a.LookupProviderState(ctx, p, commandCodeEffortsNamespace, id)
	if err != nil {
		return out, err
	}
	if found && !out.Authoritative {
		var evidence commandCodeEvidence
		if json.Unmarshal(value.Data, &evidence) != nil {
			return out, inference.ErrUnavailable
		}
		out.RefreshedEfforts = map[string][]string{model: slices.Clone(evidence.Accepted)}
		out.RejectedEfforts = map[string][]string{model: slices.Clone(evidence.Rejected)}
	}
	return out, nil
}

func (a *inferenceAdapter) learnCommandCodeEfforts(ctx context.Context, in inference.AdapterInput, model string, accepted, rejected []string) error {
	p, id, err := commandCodeState(in)
	if err != nil {
		return err
	}
	if !strings.EqualFold(commandcode.CanonicalModel(in.Target.Model), model) || len(accepted) > 16 || len(rejected) > 16 {
		return inference.ErrUnsupported
	}
	for _, ladder := range [][]string{accepted, rejected} {
		for _, v := range ladder {
			if !slices.Contains([]string{"none", "low", "medium", "high", "xhigh", "max", "ultra"}, v) {
				return inference.ErrUnsupported
			}
		}
	}
	return a.UpdateProviderState(ctx, p, commandCodeEffortsNamespace, id, time.Now().Add(time.Hour), func(raw []byte) ([]byte, error) {
		var old commandCodeEvidence
		if len(raw) > 0 && json.Unmarshal(raw, &old) != nil {
			return nil, inference.ErrUnavailable
		}
		// Concurrent refusals must never lose an already observed rejected rung.
		for _, v := range rejected {
			if !slices.Contains(old.Rejected, v) {
				old.Rejected = append(old.Rejected, v)
			}
		}
		old.Accepted = slices.DeleteFunc(slices.Clone(accepted), func(v string) bool { return slices.Contains(old.Rejected, v) })
		return json.Marshal(old)
	})
}

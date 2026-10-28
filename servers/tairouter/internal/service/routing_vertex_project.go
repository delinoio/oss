// SPDX-License-Identifier: Apache-2.0
package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"time"
	"unicode/utf8"

	"github.com/delinoio/oss/packages/tairouter/inference"
	"github.com/delinoio/oss/packages/tairouter/inference/providers/google"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
)

const vertexProjectProbeURL = "https://aiplatform.googleapis.com/v1/publishers/google/models/ocx-project-probe:countTokens"

var vertexProjectNumber = regexp.MustCompile(`\bprojects/([0-9]{1,20})/locations/`)

func vertexAutoRequested(d providerData) bool {
	return d.Metadata != nil && d.Metadata.ProviderId == "google-vertex" && d.Metadata.AuthKind == v1.ProviderAuthKind_PROVIDER_AUTH_KIND_API_KEY && d.Metadata.Configuration != nil && d.Metadata.Configuration.GetVertexProject() == "auto"
}

// This read-only probe follows google-vertex-key-project.ts at the pinned source.
// Only a complete, valid UTF-8 404 body may identify the key's project.
func probeVertexProject(ctx context.Context, policy inference.DestinationPolicy, gate inference.SendGate, key string) (string, error) {
	if !inference.HasPhysicalSendBudget(ctx) {
		return "", inference.ErrUnavailable
	}
	bounded, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(bounded, http.MethodPost, vertexProjectProbeURL, bytes.NewBufferString(`{"contents":[{"role":"user","parts":[{"text":"x"}]}]}`))
	if err != nil {
		return "", inference.ErrUnavailable
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Goog-Api-Key", key)
	sent := false
	resp, err := policy.Do(bounded, req, gate, func() { sent = true })
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		// A received redirect is uninformative; a destination rejection before any
		// physical send must never be downgraded into permission for another send.
		if errors.Is(err, inference.ErrDestination) {
			if !sent {
				return "", err
			}
			return "", nil
		}
		var typed *inference.Error
		if errors.As(err, &typed) && !errors.Is(err, inference.ErrAmbiguous) {
			return "", err
		}
		return "", nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		return "", nil
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4097))
	if ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || len(raw) > 4096 || !utf8.Valid(raw) {
		return "", nil
	}
	match := vertexProjectNumber.FindSubmatch(raw)
	if len(match) != 2 {
		return "", nil
	}
	return string(match[1]), nil
}

func (a *inferenceAdapter) negotiateVertexProject(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, policy inference.DestinationPolicy) (inference.Target, inference.Credential, error) {
	if t.Wire != google.WireVertex {
		return t, c, nil
	}
	d, version, err := a.providerVersion(ctx, scopeOf(p), c.AccountID)
	if err != nil {
		return t, c, inferenceError(err)
	}
	if version != c.CredentialVersion {
		return t, c, inference.ErrNoCredential
	}
	if !vertexAutoRequested(d) {
		return t, c, nil
	}
	current, err := credentialFor(ctx, d, t)
	if err != nil || current.Secret != c.Secret {
		return t, c, inference.ErrNoCredential
	}
	lease, err := a.current(p)
	if err != nil {
		return t, c, err
	}
	waiting, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	var project, claim string
	for {
		project, claim, err = a.s.ClaimVertexProject(waiting, lease, c.AccountID, version)
		if !errors.Is(err, store.ErrBusy) {
			break
		}
		timer := time.NewTimer(50 * time.Millisecond)
		select {
		case <-waiting.Done():
			timer.Stop()
			return t, c, inference.ErrUnavailable
		case <-timer.C:
		}
	}
	if err != nil {
		return t, c, inferenceError(err)
	}
	if claim != "" {
		var authorityErr error
		gate := func(sendctx context.Context, start func(context.Context) error) error {
			entered := false
			err := a.s.WithAccountDispatch(sendctx, lease, c.AccountID, version, func(locked context.Context) error { entered = true; return start(locked) })
			if err != nil && !entered {
				authorityErr = err
			}
			return err
		}
		project, err = probeVertexProject(ctx, policy, gate, c.Secret)
		if authorityErr != nil {
			err = inferenceError(authorityErr)
		}
		if err != nil {
			project = ""
		}
		// The bounded HTTP operation has ended. Retire this read-only claim even
		// when its caller canceled; a failed retirement remains fenced by its ID.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
		completed := a.s.CompleteVertexProject(cleanup, lease, c.AccountID, version, claim, project)
		cancel()
		if err != nil {
			return t, c, err
		}
		if completed != nil {
			return t, c, inferenceError(completed)
		}
	}
	_, latest, err := a.providerVersion(ctx, scopeOf(p), c.AccountID)
	if err != nil {
		return t, c, inferenceError(err)
	}
	if latest != version {
		return t, c, inference.ErrNoCredential
	}
	location := ""
	base := "https://aiplatform.googleapis.com"
	if project != "" {
		location = d.Metadata.Configuration.GetVertexLocation()
		if location == "" {
			location = "global"
		}
		if !selectedVertexLocation.MatchString(location) {
			return t, c, inference.ErrUnsupported
		}
		if location != "global" {
			base = "https://" + location + "-aiplatform.googleapis.com"
		}
	}
	c.Headers = c.Headers.Clone()
	c.Headers.Set(google.ProjectHeader, project)
	c.Headers.Set(google.LocationHeader, location)
	t.BaseURL, t.Endpoint = base, ""
	if t.Policy != nil {
		detached := *t.Policy
		detached.BaseURL, detached.Endpoint = base, ""
		t.Policy = &detached
	}
	return t, c, nil
}

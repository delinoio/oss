// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"net/http"
)

// Core invokes this before repair/recovery for each physical model response.
// Support-token/profile calls do not produce model quota evidence.
func (a *inferenceAdapter) ObserveResponse(ctx context.Context, p inference.Principal, t inference.Target, c inference.Credential, status int, headers http.Header) error {
	if err := a.observeNativeHealthResponse(ctx, p, t, c, status, headers); err != nil {
		return err
	}
	if err := a.observeNativeResponse(ctx, p, t, c, status, headers); err != nil {
		return err
	}
	if err := a.captureContext(ctx, p, t, c, status); err != nil {
		return err
	}
	return a.collectNormalQuota(ctx, p, t, c)
}

var _ inference.PhysicalResponseObserver = (*inferenceAdapter)(nil)

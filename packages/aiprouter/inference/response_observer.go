// SPDX-License-Identifier: Apache-2.0
package inference

import (
	"context"
	"net/http"
)

// PhysicalResponseObserver sees each model HTTP response once, before decode,
// repair or hosted delegation. Implementations atomically fence the selected
// account/version when persisting quota, affinity or failure evidence.
type PhysicalResponseObserver interface {
	ObserveResponse(context.Context, Principal, Target, Credential, int, http.Header) error
}

func (h *Handler) observeResponse(ctx context.Context, p Principal, t Target, c Credential, r *http.Response) error {
	if h.c.ResponseObserver == nil {
		return nil
	}
	if e := h.guard(ctx, p); e != nil {
		return e
	}
	return h.c.ResponseObserver.ObserveResponse(ctx, p, t, c, r.StatusCode, r.Header.Clone())
}

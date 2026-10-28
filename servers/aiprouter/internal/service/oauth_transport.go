// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"net/http"

	"github.com/delinoio/oss/packages/aiprouter/core"
	"github.com/delinoio/oss/packages/aiprouter/inference"
	"github.com/delinoio/oss/packages/aiprouter/providers"
	"github.com/delinoio/oss/packages/aiprouter/store"
)

type providerExecutor struct {
	store  *store.Store
	scope  core.Scope
	gate   inference.SendGate
	onSend func()
	// Test executors represent a synchronous physical send. Production always uses
	// DestinationPolicy.Do, whose database gate ends before response headers arrive.
	test providers.Executor
}

func (e *providerExecutor) Do(ctx context.Context, scope core.Scope, req *http.Request) (*http.Response, error) {
	if scope != e.scope || e.gate == nil {
		return nil, providers.ErrScope
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if e.test != nil {
		var response *http.Response
		err := e.gate(ctx, func(c context.Context) error {
			return inference.PhysicalSend(c, func(c context.Context) error {
				if e.onSend != nil {
					e.onSend()
				}
				var err error
				response, err = e.test.Do(c, scope, req)
				return err
			})
		})
		return response, err
	}
	settings, err := e.store.Settings(ctx)
	if err != nil {
		return nil, err
	}
	policy := inference.DestinationPolicy{ApprovedOrigins: settings.Destinations, Rules: settings.DestinationRules}
	return policy.Do(ctx, req, e.gate, e.onSend)
}

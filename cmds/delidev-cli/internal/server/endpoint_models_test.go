// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"testing"
)

func TestSourceChangeCancelsOnlyOriginalEndpointReads(t *testing.T) {
	provider, account, other := domain.NewID(), domain.NewID(), domain.NewID()
	endpoint, stop := context.WithCancel(context.Background())
	defer stop()
	validation, stopValidation := context.WithCancel(context.Background())
	defer stopValidation()
	unrelated, stopOther := context.WithCancel(context.Background())
	defer stopOther()
	s := &Service{accountChecks: map[domain.ID]map[domain.ID]accountCheck{account: {domain.NewID(): {cancel: stop, providerID: provider, operation: endpointListing}, domain.NewID(): {cancel: stopValidation, providerID: provider, operation: validationInspection}}, other: {domain.NewID(): {cancel: stopOther, providerID: other, operation: endpointListing}}}}
	s.cancelEndpointChecks(provider, true)
	if endpoint.Err() == nil {
		t.Fatal("original read survived source change")
	}
	if validation.Err() != nil || unrelated.Err() != nil {
		t.Fatal("independent inspection cancelled")
	}
}

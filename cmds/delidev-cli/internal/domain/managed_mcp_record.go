// SPDX-License-Identifier: Apache-2.0
package domain

// This server row retains only safe metadata. Native vault records, PKCE values,
// authorization codes and tokens remain on the selected Worker.
type ManagedMCPRecord struct {
	Definition       ManagedMCPDefinition `json:"definition"`
	Accepted         bool                 `json:"accepted,omitempty"`
	Deleted          bool                 `json:"deleted,omitempty"`
	PendingRequestID ID                   `json:"pending_request_id,omitempty"`
	PendingActorID   ID                   `json:"pending_actor_id,omitempty"`
	PendingAction    ManagedMCPAction     `json:"pending_action,omitempty"`
}

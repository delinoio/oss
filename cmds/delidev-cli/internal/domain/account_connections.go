// SPDX-License-Identifier: Apache-2.0
package domain

// A generation retains only server-owned observations for original executions.
// The protected credential is shared; format changes do not copy or delete keys.
type AccountConnectionGeneration struct {
	Connection AccountConnection  `json:"connection"`
	Health     AccountHealth      `json:"health"`
	Validation *AccountValidation `json:"validation,omitempty"`
}

const MaxAccountConnectionGenerations = 128

func (c AccountConnection) CredentialReferenceID() ID {
	if c.CredentialID != "" {
		return c.CredentialID
	}
	return c.ID
}

// ForConnection projects the original connection under current account controls.
// Callers must independently prove an immutable execution/inspection assignment.
// Disconnect clears every generation and revokes all of these projections.
func (a Account) ForConnection(id ID) Account {
	if a.Connection == nil || a.Removal != nil || a.Connection.ID == id {
		return a
	}
	for _, generation := range a.RetainedConnections {
		if generation.Connection.ID != id || generation.Connection.APIFormat == nil {
			continue
		}
		a.Connection = &generation.Connection
		a.APIProtocol = generation.Connection.APIFormat.Protocol
		a.Health, a.Validation = generation.Health, generation.Validation
		// Exhaustion and quota remain current shared credential/account controls.
		a.Catalog, a.RetainedConnections = nil, nil
		return a
	}
	a.Connection, a.Validation = nil, nil
	a.Health = AccountDisconnected
	return a
}

func (a Account) validateConnectionGenerations() error {
	invalid := func() error {
		return Fail(InvalidArgument, "Invalid retained account connections.", "Preserve server-owned connection generations.")
	}
	if a.Connection != nil && a.Connection.CredentialID != "" && (a.Type != APIAccount || a.Connection.CredentialID.Validate() != nil) {
		return invalid()
	}
	if len(a.RetainedConnections) == 0 {
		if a.Connection != nil && a.Connection.CredentialID != "" && a.Connection.CredentialID != a.Connection.ID {
			return invalid()
		}
		return nil
	}
	if len(a.RetainedConnections) > MaxAccountConnectionGenerations || a.Type != APIAccount || a.Connection == nil || a.Removal != nil || a.Connection.APIFormat == nil {
		return invalid()
	}
	root := a.Connection.CredentialReferenceID()
	ids := map[ID]bool{a.Connection.ID: true}
	original := false
	for _, generation := range a.RetainedConnections {
		c := generation.Connection
		if ids[c.ID] || c.CredentialReferenceID() != root || c.APIFormat == nil {
			return invalid()
		}
		ids[c.ID] = true
		original = original || c.ID == root
		projected := a
		projected.RetainedConnections = nil
		projected.Connection = &c
		// The generation projection is not a persisted account and shares the key root.
		copied := c
		copied.CredentialID = ""
		projected.Connection = &copied
		projected.APIProtocol, projected.Health = c.APIFormat.Protocol, generation.Health
		projected.Validation, projected.Catalog = generation.Validation, nil
		if projected.Validate() != nil {
			return invalid()
		}
	}
	if !original {
		return invalid()
	}
	return nil
}

func AgentReconfigurationRequired() *Error {
	return Fail(RecoveryRequired, "This Worker needs account and model reconfiguration.", "Explicitly reconfigure its selected accounts and compatible model. Existing executions keep their original configuration.")
}

package domain

import (
	"encoding/json"
	"slices"
)

// ExecutionJobInput is a non-secret immutable Worker assignment. API authority
// arrives separately through an authenticated digest-only grant registration;
// upstream credentials and raw execution tokens never belong in this document.
type ExecutionJobInput struct {
	Version             uint32                 `json:"version"`
	SessionID           ID                     `json:"session_id"`
	MachineID           ID                     `json:"machine_id"`
	ExecutionID         ID                     `json:"execution_id"`
	InputID             ID                     `json:"input_id"`
	ThreadRequestID     ID                     `json:"thread_request_id"`
	TurnRequestID       ID                     `json:"turn_request_id"`
	Configuration       ExecutionConfiguration `json:"configuration"`
	ConfigurationDigest string                 `json:"configuration_digest"`
	AccountID           ID                     `json:"account_id"`
	ConnectionID        ID                     `json:"connection_id"`
	Input               SessionInput           `json:"input"`
	Installation        Installation           `json:"installation"`
	Preparation         json.RawMessage        `json:"preparation"`
	Manifest            json.RawMessage        `json:"manifest"`
}

func (i ExecutionJobInput) Validate() error {
	if i.Version != 1 || i.Installation.Harness != i.Configuration.Harness {
		return Fail(Unsupported, "The execution assignment profile is incompatible.", "Use a matching server and Worker native profile.")
	}
	for _, id := range []ID{i.SessionID, i.MachineID, i.ExecutionID, i.InputID, i.ThreadRequestID, i.TurnRequestID, i.AccountID, i.ConnectionID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	if err := i.Configuration.Validate(); err != nil {
		return err
	}
	if !slices.ContainsFunc(i.Configuration.Accounts, func(account WeightedAccount) bool { return account.ID == i.AccountID }) || i.Installation.State != InstallationDetected || !i.Installation.ProtocolVerified {
		return Fail(Unsupported, "The execution lacks matching account or native installation evidence.", "Revalidate the accepted configuration on its owning Worker.")
	}
	if err := i.Installation.validateProtocol(true); err != nil {
		return err
	}
	digest, err := i.Configuration.Digest()
	if err != nil || digest != i.ConfigurationDigest {
		return Fail(RecoveryRequired, "Execution configuration no longer matches its accepted digest.", "Preserve the original assignment and reconcile it before native work.")
	}
	if len(i.Preparation) == 0 || len(i.Manifest) == 0 || !json.Valid(i.Preparation) || !json.Valid(i.Manifest) {
		return Fail(InvalidArgument, "The execution assignment needs complete workspace evidence.", "Prepare and validate every owned workspace first.")
	}
	return i.Input.Validate()
}

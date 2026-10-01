// SPDX-License-Identifier: Apache-2.0
package domain

type BrowserProfileState string

const (
	BrowserProfileActive         BrowserProfileState = "active"
	BrowserProfileRemovalPending BrowserProfileState = "removal-pending"
	BrowserProfileRemoved        BrowserProfileState = "removed"
)

// BrowserProfile contains no URLs, tabs, credentials, history or native paths.
type BrowserProfile struct {
	ServerID          ID                  `json:"server_id"`
	DeviceID          ID                  `json:"device_id"`
	AccountID         ID                  `json:"account_id"`
	State             BrowserProfileState `json:"state"`
	DeletionRequestID ID                  `json:"deletion_request_id,omitempty"`
}

func (p BrowserProfile) Validate() error {
	for _, id := range []ID{p.ServerID, p.DeviceID, p.AccountID} {
		if err := id.Validate(); err != nil {
			return err
		}
	}
	switch p.State {
	case BrowserProfileActive:
		if p.DeletionRequestID == "" {
			return nil
		}
	case BrowserProfileRemovalPending, BrowserProfileRemoved:
		if p.DeletionRequestID.Validate() == nil {
			return nil
		}
	}
	return Fail(RecoveryRequired, "Browser profile ownership is inconsistent.", "Preserve the profile and inspect the original cleanup obligation.")
}

// These bounded non-secret records extend the existing paired-client document.
// They have their own monotonic revisions; device credential generations are unchanged.
type BrowserProfileRecord struct {
	ID       ID             `json:"id"`
	Revision uint64         `json:"revision"`
	Data     BrowserProfile `json:"data"`
}

func (p BrowserProfileRecord) Validate() error {
	if err := p.ID.Validate(); err != nil {
		return err
	}
	if p.Revision == 0 {
		return Fail(RecoveryRequired, "Browser profile revision is unavailable.", "Preserve the original device metadata.")
	}
	return p.Data.Validate()
}

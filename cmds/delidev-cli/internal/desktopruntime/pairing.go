// SPDX-License-Identifier: Apache-2.0
package desktopruntime

import (
	"encoding/base64"
	"errors"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const (
	pairingJournalName = "local-pairing.json"
	pairingGrantDir    = "pairing-codes"
	credentialName     = "device.json"
	grantVersion       = 1
	credentialVersion  = 1
	maxGrantBytes      = 32 << 10
)

// localPairingRecord mirrors the fixed Local Worker's retained pairing intent.
type localPairingRecord struct {
	RequestID domain.ID `json:"request_id"`
	ServerID  domain.ID `json:"server_id"`
	Endpoint  string    `json:"endpoint"`
	Name      string    `json:"name,omitempty"`
}

// retainedGrant mirrors the immutable owner-side pairing grant document.
type retainedGrant struct {
	Version   int       `json:"version"`
	PairingID domain.ID `json:"pairing_id"`
	ServerID  domain.ID `json:"server_id"`
	Endpoint  string    `json:"endpoint"`
	Code      string    `json:"code"`
}

// workerCredential mirrors worker.Credential field for field so a follow marker
// is only ever created for a credential the full loader would accept. The
// worker package imports this one, so the schema cannot be shared by import.
// Keep it in step with worker.Credential and its Validate method.
type workerCredential struct {
	Version   int               `json:"version"`
	Type      domain.DeviceType `json:"type"`
	Endpoint  string            `json:"endpoint"`
	ServerID  domain.ID         `json:"server_id"`
	DeviceID  domain.ID         `json:"device_id"`
	MachineID domain.ID         `json:"machine_id,omitempty"`
	PairingID domain.ID         `json:"pairing_id"`
	Token     string            `json:"token"`
}

// validate applies the same rules as worker.Credential.Validate for a Worker.
func (c workerCredential) validate() error {
	if c.Version != credentialVersion || c.Type != domain.WorkerDevice || !validGrantCode(c.Token) {
		return recoveryRequired()
	}
	if err := rpc.ValidateEndpoint(c.Endpoint); err != nil {
		return recoveryRequired()
	}
	for _, id := range []domain.ID{c.ServerID, c.DeviceID, c.MachineID, c.PairingID} {
		if id.Validate() != nil {
			return recoveryRequired()
		}
	}
	return nil
}

func recoveryRequired() error {
	return domain.Fail(domain.RecoveryRequired, "The original desktop recovery evidence is unavailable or changed.", "Preserve the original registration and private recovery files; do not reset the server.")
}

// unreadableEvidence classifies evidence that exists but cannot be read safely
// (oversized, unsafe permissions, symlinked or unreadable) as unverifiable.
// Callers get the isolating RecoveryRequired result instead of a raw storage
// error that would escape the follow-marker boundary.
func unreadableEvidence(phase string, err error) error {
	logUnverifiedPairing(phase, err)
	return recoveryRequired()
}

func validGrantCode(value string) bool {
	raw, err := base64.RawURLEncoding.DecodeString(value)
	return err == nil && len(raw) == 32 && base64.RawURLEncoding.EncodeToString(raw) == value
}

// VerifyOriginalPairing proves that a fixed Local Worker's saved identity is
// the one the owner originally granted: the retained local-pairing journal and
// the immutable grant at <owner>/pairing-codes/<request_id>.json must agree
// with the saved server, endpoint and pairing identity. It only reads files and
// never contacts a listener, so callers can run it before any network request.
// Missing, foreign, revoked or edited evidence yields RecoveryRequired.
func VerifyOriginalPairing(owner, root string, serverID domain.ID, endpoint string, pairingID domain.ID) error {
	raw, err := security.ReadPrivate(filepath.Join(root, pairingJournalName), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return recoveryRequired()
	}
	if err != nil {
		return unreadableEvidence("journal", err)
	}
	var attempt localPairingRecord
	if domain.Decode(raw, &attempt) != nil || attempt.RequestID.Validate() != nil || serverID.Validate() != nil || pairingID.Validate() != nil || attempt.ServerID != serverID || attempt.Endpoint != endpoint {
		return recoveryRequired()
	}
	grants := filepath.Join(owner, pairingGrantDir)
	if err := security.CheckPrivateDir(grants); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return recoveryRequired()
		}
		return unreadableEvidence("grant-directory", err)
	}
	raw, err = security.ReadPrivate(filepath.Join(grants, string(attempt.RequestID)+".json"), maxGrantBytes)
	defer clear(raw)
	if errors.Is(err, os.ErrNotExist) {
		return recoveryRequired()
	}
	if err != nil {
		return unreadableEvidence("grant", err)
	}
	var grant retainedGrant
	if domain.Decode(raw, &grant) != nil || grant.Version != grantVersion || !validGrantCode(grant.Code) || grant.PairingID.Validate() != nil || grant.ServerID.Validate() != nil {
		return recoveryRequired()
	}
	if grant.ServerID != serverID || grant.Endpoint != endpoint || grant.PairingID != pairingID {
		return recoveryRequired()
	}
	return nil
}

// verifySavedWorker applies VerifyOriginalPairing to the Worker credential
// stored at root. A missing credential reports exists=false: nothing follows
// a runtime before a Worker identity was accepted. The complete credential is
// decoded strictly and validated first, so a file the Worker loader would
// reject (bad version or token, missing device or machine identity, unknown or
// duplicate fields) can never earn a follow marker through matching identity
// fields alone.
func verifySavedWorker(owner, root string, serverID domain.ID, endpoint string) (exists bool, err error) {
	raw, err := security.ReadPrivate(filepath.Join(root, credentialName), 16<<10)
	defer clear(raw)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return true, unreadableEvidence("credential", err)
	}
	var saved workerCredential
	if domain.Decode(raw, &saved) != nil || saved.validate() != nil {
		return true, recoveryRequired()
	}
	if saved.ServerID != serverID || saved.Endpoint != endpoint {
		return true, recoveryRequired()
	}
	return true, VerifyOriginalPairing(owner, root, saved.ServerID, saved.Endpoint, saved.PairingID)
}

func logUnverifiedPairing(phase string, err error) {
	slog.Warn("desktop_runtime_follow", "phase", phase, "code", domain.SafeError(err).Code)
}

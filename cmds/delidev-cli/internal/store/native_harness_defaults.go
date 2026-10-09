// SPDX-License-Identifier: Apache-2.0
package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/providers"
	"slices"
)

func nativeDefaultKey(machine, account, project domain.ID) string {
	return "native-harness-defaults:" + string(machine) + ":" + string(account) + ":" + string(project)
}

// Profile digest includes the immutable selected API profile or subscription
// generation. It excludes credentials and does not manufacture source authority.
func (t *Tx) nativeDefaultScope(accountID domain.ID) (domain.Account, string, error) {
	_, account, err := decodeEntity[domain.Account](t, domain.AccountKind, accountID)
	if err != nil || account.Connection == nil || account.Removal != nil || !account.Enabled {
		return account, "", domain.NativeDefaultsUnavailable()
	}
	var scope any
	if account.Type == domain.APIAccount {
		_, provider, err := decodeEntity[domain.Provider](t, domain.ProviderKind, account.ProviderID)
		if err != nil {
			return account, "", err
		}
		profile, err := providers.ResolveAccountProfile(provider, account)
		if err != nil {
			return account, "", err
		}
		scope = profile
	} else {
		if account.Subscription == nil || account.Subscription.Generation == "" || account.Subscription.RecoveryRequired {
			return account, "", domain.NativeDefaultsUnavailable()
		}
		scope = struct {
			Service    domain.SubscriptionService
			Generation domain.ID
		}{account.SubscriptionService, account.Subscription.Generation}
	}
	raw, err := json.Marshal(scope)
	if err != nil {
		return account, "", err
	}
	sum := sha256.Sum256(raw)
	return account, hex.EncodeToString(sum[:]), nil
}

// Called only within the original startup Ready mutation, after its actor,
// native scope, no-input and protected grant checks. No new execution is started.
func (t *Tx) RecordNativeHarnessDefaults(input domain.ExecutionJobInput, job, device, project domain.ID, o domain.ExecutionStartupObservation) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if o.State != domain.StartupReady || o.Harness != domain.Codex || o.NativeDefaults == nil || o.Validate() != nil {
		return domain.NativeDefaultsUnavailable()
	}
	_, machine, err := decodeEntity[domain.Machine](t, domain.MachineKind, input.MachineID)
	if err != nil || !slices.Contains(machine.WorkerCapabilities, domain.NativeHarnessDefaultsV1) {
		return domain.NativeDefaultsUnavailable()
	}
	account, scope, err := t.nativeDefaultScope(input.AccountID)
	if err != nil || account.Connection.ID != input.ConnectionID {
		return domain.NativeDefaultsUnavailable()
	}
	digest, err := o.NativeDefaults.Digest()
	if err != nil {
		return err
	}
	p := domain.NativeHarnessDefaultProof{Version: 1, MachineID: input.MachineID, DeviceID: device, AccountID: input.AccountID, ConnectionID: input.ConnectionID, ProjectID: project, JobID: job, ExecutionID: input.ExecutionID, ProfileDigest: scope, ExecutableSHA256: o.ExecutableSHA256, NativeVersion: o.NativeVersion, Defaults: *o.NativeDefaults, DefaultsDigest: digest}
	if p.Validate() != nil {
		return domain.NativeDefaultsUnavailable()
	}
	if input.Configuration.NativeDefaults != nil {
		original := input.Configuration.NativeDefaults
		if original.DeviceID != device || original.AccountID != input.AccountID || original.ConnectionID != input.ConnectionID || original.MachineID != input.MachineID || original.ProjectID != project || original.ProfileDigest != scope || original.VerifyCurrent(p.Defaults, p.NativeVersion, p.ExecutableSHA256) != nil {
			return domain.NativeDefaultsUnavailable()
		}
	}
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", nativeDefaultKey(input.MachineID, input.AccountID, project), string(raw))
	return storageError(err)
}
func (t *Tx) NativeHarnessDefaults(machine, accountID, project domain.ID) (domain.NativeHarnessDefaultProof, error) {
	var p domain.NativeHarnessDefaultProof
	var raw string
	if machine.Validate() != nil {
		return p, domain.NativeDefaultsUnavailable()
	}
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", nativeDefaultKey(machine, accountID, project)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return p, domain.NativeDefaultsUnavailable()
	}
	if err != nil {
		return p, storageError(err)
	}
	if len(raw) > 8192 || domain.Decode([]byte(raw), &p) != nil || p.Validate() != nil || p.MachineID != machine || p.AccountID != accountID || p.ProjectID != project {
		return p, domain.NativeDefaultsUnavailable()
	}
	_, device, err := decodeEntity[domain.Device](t, domain.DeviceKind, p.DeviceID)
	if err != nil || device.Revoked || device.Type != domain.WorkerDevice || device.MachineID != machine {
		return p, domain.NativeDefaultsUnavailable()
	}
	_, m, err := decodeEntity[domain.Machine](t, domain.MachineKind, machine)
	if err != nil || m.Disabled || !slices.Contains(m.WorkerCapabilities, domain.NativeHarnessDefaultsV1) {
		return p, domain.NativeDefaultsUnavailable()
	}
	a, scope, err := t.nativeDefaultScope(accountID)
	if err != nil || a.Connection.ID != p.ConnectionID || scope != p.ProfileDigest {
		return p, domain.NativeDefaultsUnavailable()
	}
	// Exact binary changes are rechecked by the next actual process, before any
	// thread override/input. Installation discovery is advisory and cannot grant it.
	return p, nil
}
func (t *Tx) nativeHarnessValues(proof domain.NativeHarnessDefaultProof, anchor domain.Model) (domain.HarnessValues, error) {
	v := domain.InheritedHarnessValues()
	d := proof.Defaults
	if d.Model != nil {
		r, found, err := t.ModelBySourceNative(anchor.ProviderID, anchor.SubscriptionService, *d.Model)
		if err != nil {
			return v, err
		}
		if found {
			v.Model = domain.InheritedValue[domain.ID]{State: domain.HarnessOverride, Value: &r.ID}
		}
	}
	if d.Effort != nil {
		v.Effort = domain.InheritedValue[string]{State: domain.HarnessOverride, Value: d.Effort}
	}
	if d.ServiceTier != nil {
		v.ServiceTier = domain.InheritedValue[string]{State: domain.HarnessOverride, Value: d.ServiceTier}
	}
	if d.ApprovalPolicy != nil {
		v.ApprovalPolicy = domain.InheritedValue[string]{State: domain.HarnessOverride, Value: d.ApprovalPolicy}
	}
	if d.ApprovalsReviewer != nil {
		v.ApprovalsReviewer = domain.InheritedValue[domain.ApprovalsReviewer]{State: domain.HarnessOverride, Value: d.ApprovalsReviewer}
	}
	return v, v.Validate()
}

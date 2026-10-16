// SPDX-License-Identifier: Apache-2.0
package server

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
)

// workerRecoveryHoldKind is the closed set of managed subscription states that
// only the original Worker device of one machine can still complete. It is also
// the structured-log reason of a refused Worker removal.
type workerRecoveryHoldKind string

const (
	// holdLiveExecuteLease is an EXECUTE lease whose Worker is still running. Its
	// own Finish releases it, and removal closes the Worker's connections, which
	// fences the lease at once.
	holdLiveExecuteLease workerRecoveryHoldKind = "live_execute_lease"
	// holdFencedLease is a retained lease under the account's recovery fence that
	// WatchSubscription offers back only to the machine that owns it: a managed
	// ChatGPT or Grok EXECUTE lease, or a Claude lease of any action.
	holdFencedLease workerRecoveryHoldKind = "fenced_lease"
	// holdRecoverySettlement is a committed managed ChatGPT recovery decision whose
	// protected-reference retirement only the original request can finish.
	holdRecoverySettlement workerRecoveryHoldKind = "recovery_settlement"
)

// workerRecoveryHold is one account state that waits for one Worker device.
type workerRecoveryHold struct {
	Account domain.ID
	Machine domain.ID
	Device  domain.ID
	Kind    workerRecoveryHoldKind
}

// accountWorkerRecoveryHold classifies the account against the closed hold set.
// The predicates mirror the rows WatchSubscription offers to the owning machine
// and the checks of the Worker-only recovery operations, so a state is held here
// exactly when no other principal can complete it. A fenced lease the server
// never offers back (a ChatGPT lifecycle lease) has no Worker recovery and is not
// held.
func accountWorkerRecoveryHold(id domain.ID, a domain.Account) (workerRecoveryHold, bool) {
	state := a.Subscription
	if a.Type != domain.SubscriptionAccount || state == nil {
		return workerRecoveryHold{}, false
	}
	if settlement := state.RecoverySettlement; settlement != nil {
		return workerRecoveryHold{Account: id, Machine: settlement.Machine, Device: settlement.Device, Kind: holdRecoverySettlement}, true
	}
	lease := state.Lease
	if lease == nil {
		return workerRecoveryHold{}, false
	}
	hold := workerRecoveryHold{Account: id, Machine: lease.MachineID, Device: lease.DeviceID}
	switch {
	case state.RecoveryRequired && (lease.Action == domain.SubscriptionExecute || a.SubscriptionService == domain.SubscriptionClaude):
		hold.Kind = holdFencedLease
	case !state.RecoveryRequired && lease.Action == domain.SubscriptionExecute:
		hold.Kind = holdLiveExecuteLease
	default:
		return workerRecoveryHold{}, false
	}
	return hold, true
}

// workerRecoveryHoldsForMachine lists every account state of the machine that its
// original Worker device must still complete. A state whose bound device is
// already revoked or gone is skipped: removal cannot make it less recoverable,
// and blocking on it would keep a permanently lost Worker from ever being removed.
// The skipped states stay fenced and are returned separately for the log.
func workerRecoveryHoldsForMachine(tx *store.Tx, machine domain.ID) (held, orphaned []workerRecoveryHold, err error) {
	records, err := all(tx, domain.AccountKind)
	if err != nil {
		return nil, nil, err
	}
	for _, record := range records {
		account, err := store.Decode[domain.Account](record)
		if err != nil {
			return nil, nil, err
		}
		hold, ok := accountWorkerRecoveryHold(record.ID, account)
		if !ok || hold.Machine != machine {
			continue
		}
		usable, err := workerDeviceUsable(tx, hold.Device)
		if err != nil {
			return nil, nil, err
		}
		if usable {
			held = append(held, hold)
		} else {
			orphaned = append(orphaned, hold)
		}
	}
	return held, orphaned, nil
}

// workerDeviceUsable reports whether the device is a registered, unrevoked Worker
// device, the only principal the original-Worker operations accept.
func workerDeviceUsable(tx *store.Tx, id domain.ID) (bool, error) {
	if id.Validate() != nil {
		return false, nil
	}
	record, err := tx.Get(domain.DeviceKind, id)
	if err != nil {
		if domain.SafeError(err).Code == domain.NotFound {
			return false, nil
		}
		return false, err
	}
	device, err := store.Decode[domain.Device](record)
	if err != nil {
		return false, err
	}
	return device.Type == domain.WorkerDevice && !device.Revoked, nil
}

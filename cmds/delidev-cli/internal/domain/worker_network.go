// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"slices"
	"time"

	"filippo.io/age"
)

// Public pin belongs to the original pending pairing or paired Worker. It
// contains no proxy credential and does not itself grant dispatch authority.
type WorkerNetworkBinding struct {
	DeviceID  ID     `json:"device_id"`
	PairingID ID     `json:"pairing_id"`
	KeyID     ID     `json:"key_id"`
	Recipient string `json:"recipient"`
	Endpoint  string `json:"endpoint"`
}

func (v WorkerNetworkBinding) Validate() error {
	for _, id := range []ID{v.DeviceID, v.PairingID, v.KeyID} {
		if id.Validate() != nil {
			return Fail(InvalidArgument, "Invalid Worker network recipient binding.", "Use the original pairing/device and protected X25519 recipient.")
		}
	}
	if ValidateWorkerRecipient(v.KeyID, v.Recipient) != nil || len(v.Endpoint) > 2048 || v.Endpoint == "" {
		return Fail(InvalidArgument, "Invalid Worker network recipient binding.", "Use the original pairing/device and protected X25519 recipient.")
	}
	return nil
}

func ValidateWorkerRecipient(key ID, recipient string) error {
	r, err := age.ParseX25519Recipient(recipient)
	if key.Validate() != nil || err != nil || r.String() != recipient {
		return Fail(InvalidArgument, "Invalid Worker network recipient.", "Use the original protected X25519 key and its public identity.")
	}
	return nil
}

type WorkerNativeRouteState string

const (
	WorkerRouteUnsupported WorkerNativeRouteState = "unsupported"
	WorkerRouteNotApplied  WorkerNativeRouteState = "not-applied"
	WorkerRouteUnverified  WorkerNativeRouteState = "unverified"
	WorkerRouteObserved    WorkerNativeRouteState = "observed"
	WorkerRouteFailed      WorkerNativeRouteState = "failed"
	WorkerRouteStale       WorkerNativeRouteState = "stale"
)

func (v WorkerNativeRouteState) Valid() bool {
	return slices.Contains([]WorkerNativeRouteState{WorkerRouteUnsupported, WorkerRouteNotApplied, WorkerRouteUnverified, WorkerRouteObserved, WorkerRouteFailed, WorkerRouteStale}, v)
}

type WorkerNetworkState struct {
	InstanceID          ID                     `json:"instance_id"`
	KeyID               ID                     `json:"key_id,omitempty"`
	Recipient           string                 `json:"recipient,omitempty"`
	RouteID             ID                     `json:"route_id,omitempty"`
	EffectiveGeneration uint64                 `json:"effective_generation,string"`
	IssuedGeneration    uint64                 `json:"issued_generation,string"`
	IssuedDigest        string                 `json:"issued_digest,omitempty"`
	NativeState         WorkerNativeRouteState `json:"native_state"`
	NativeExecutionID   ID                     `json:"native_execution_id,omitempty"`
	NativeGeneration    uint64                 `json:"native_generation,string"`
	ObservedAt          time.Time              `json:"observed_at"`
}

func (v WorkerNetworkState) Validate() error {
	if v.InstanceID.Validate() != nil || v.EffectiveGeneration >= 1<<63 || v.IssuedGeneration >= 1<<63 || v.NativeGeneration >= 1<<63 || !v.NativeState.Valid() || v.ObservedAt.IsZero() || v.KeyID != "" && v.KeyID.Validate() != nil || v.RouteID != "" && v.RouteID.Validate() != nil || v.NativeExecutionID != "" && v.NativeExecutionID.Validate() != nil || v.IssuedDigest != "" && !validSHA256(v.IssuedDigest) {
		return Fail(InvalidArgument, "Invalid Worker route observation.", "Reconcile the original route and generation through authenticated Worker control.")
	}
	if v.Recipient != "" {
		r, err := age.ParseX25519Recipient(v.Recipient)
		if err != nil || r.String() != v.Recipient {
			return Fail(InvalidArgument, "Invalid Worker network recipient.", "Use the original protected X25519 key.")
		}
	}
	return nil
}

type WorkerNetworkStatus struct {
	Version             uint32                 `json:"version"`
	MachineID           ID                     `json:"machine_id"`
	DesiredGeneration   uint64                 `json:"desired_generation,string"`
	EffectiveGeneration uint64                 `json:"effective_generation,string"`
	RouteID             ID                     `json:"route_id,omitempty"`
	ControlState        WorkerNativeRouteState `json:"control_state"`
	NativeState         WorkerNativeRouteState `json:"native_state"`
	NativeGeneration    uint64                 `json:"native_generation,string"`
	NativeExecutionID   ID                     `json:"native_execution_id,omitempty"`
}

func (v WorkerNetworkStatus) Validate() error {
	if v.Version != 1 || v.MachineID.Validate() != nil || v.DesiredGeneration >= 1<<63 || v.EffectiveGeneration >= 1<<63 || v.NativeGeneration >= 1<<63 || !v.ControlState.Valid() || !v.NativeState.Valid() || v.RouteID != "" && v.RouteID.Validate() != nil || v.NativeExecutionID != "" && v.NativeExecutionID.Validate() != nil {
		return Fail(Unsupported, "Unsupported Worker routing status.", "Update the client and server before interpreting the original generation state.")
	}
	return nil
}

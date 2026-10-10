// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type ApprovalState string

const (
	ApprovalPending  ApprovalState = "pending"
	ApprovalApproved ApprovalState = "approved"
	ApprovalDenied   ApprovalState = "denied"
	ApprovalExpired  ApprovalState = "expired"
	ApprovalCanceled ApprovalState = "canceled"
)

type ApprovalInput struct {
	ObservedTargetKey  []byte            `json:"observed_target_key,omitempty"`
	WorkerServerID     domain.ID         `json:"worker_server_id,omitempty"`
	WorkerServerOrigin string            `json:"worker_server_origin,omitempty"`
	ID                 domain.ID         `json:"request_id"`
	RequesterName      string            `json:"requester_name"`
	RequesterKey       []byte            `json:"requester_key"`
	ServerID           domain.ID         `json:"server_id"`
	Origin             string            `json:"origin"`
	Role               domain.DeviceType `json:"role"`
}
type Approval struct {
	DecisionID     domain.ID     `json:"decision_id,omitempty"`
	DecisionAllow  bool          `json:"decision_allow,omitempty"`
	Input          ApprovalInput `json:"input"`
	TargetKey      []byte        `json:"target_key"`
	Code           string        `json:"code"`
	ExpiresAt      time.Time     `json:"expires_at"`
	State          ApprovalState `json:"state"`
	EncryptedGrant []byte        `json:"encrypted_grant,omitempty"`
}
type approvalIntent struct {
	WorkerControls []WorkerControl `json:"worker_controls,omitempty"`
	DeliveryDigest []byte          `json:"delivery_digest,omitempty"`
	WorkerDeviceID domain.ID       `json:"worker_device_id,omitempty"`
	DecisionActor  string          `json:"decision_actor,omitempty"`
	Approval       Approval        `json:"approval"`
	PrivateKey     []byte          `json:"private_key"`
	PairingRequest domain.ID       `json:"pairing_request"`
	GrantCode      string          `json:"grant_code"`
	DecisionID     domain.ID       `json:"decision_id,omitempty"`
	DecisionAllow  bool            `json:"decision_allow,omitempty"`
}
type approvalJournal struct {
	ServerID domain.ID        `json:"server_id"`
	Requests []approvalIntent `json:"requests"`
}
type Approvals struct {
	Root     string
	ServerID domain.ID
	Origin   string
	Now      func() time.Time
	mu       sync.Mutex
}

func (a *Approvals) SetOrigin(origin string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.Origin = origin
}

const maxApprovalRecords = 256

func (i ApprovalInput) Validate() error {
	if len(i.ObservedTargetKey) != 0 && len(i.ObservedTargetKey) != 32 {
		return invalid()
	}
	if i.Role == domain.WorkerDevice {
		if i.WorkerServerID.Validate() != nil || ValidateOrigin(i.WorkerServerOrigin) != nil {
			return invalid()
		}
	} else if i.WorkerServerID != "" || i.WorkerServerOrigin != "" {
		return invalid()
	}
	if i.ID.Validate() != nil || i.ServerID.Validate() != nil || ValidateOrigin(i.Origin) != nil || domain.Text(i.RequesterName, "requester name", 256, true) != nil || len(i.RequesterKey) != 32 || (i.Role != domain.ClientDevice && i.Role != domain.WorkerDevice) {
		return invalid()
	}
	_, err := ecdh.X25519().NewPublicKey(i.RequesterKey)
	return err
}
func (a *Approvals) now() time.Time {
	if a.Now != nil {
		return a.Now().UTC()
	}
	return time.Now().UTC()
}
func (a *Approvals) load() (approvalJournal, error) {
	if a.ServerID.Validate() != nil || ValidateOrigin(a.Origin) != nil {
		return approvalJournal{}, invalid()
	}
	raw, err := security.ReadPrivate(filepath.Join(a.Root, "tailscale-approvals.json"), 2<<20)
	if errors.Is(err, os.ErrNotExist) {
		if _, markerError := security.ReadPrivate(filepath.Join(a.Root, "tailscale-approvals.owner"), 4<<10); !errors.Is(markerError, os.ErrNotExist) {
			return approvalJournal{}, recovery()
		}
		return approvalJournal{ServerID: a.ServerID, Requests: []approvalIntent{}}, nil
	}
	if err != nil {
		return approvalJournal{}, err
	}
	marker, markerError := security.ReadPrivate(filepath.Join(a.Root, "tailscale-approvals.owner"), 4<<10)
	if markerError != nil || string(marker) != string(a.ServerID) {
		return approvalJournal{}, recovery()
	}
	var j approvalJournal
	if domain.Decode(raw, &j) != nil || j.ServerID != a.ServerID || len(j.Requests) > maxApprovalRecords {
		return approvalJournal{}, recovery()
	}
	seen := map[domain.ID]bool{}
	for _, r := range j.Requests {
		codeBytes, codeError := base64.RawURLEncoding.DecodeString(r.GrantCode)
		if codeError != nil || len(codeBytes) != 32 || base64.RawURLEncoding.EncodeToString(codeBytes) != r.GrantCode || len(r.DeliveryDigest) != 0 && len(r.DeliveryDigest) != 32 || r.WorkerDeviceID != "" && r.WorkerDeviceID.Validate() != nil {
			return approvalJournal{}, recovery()
		}
		if r.Approval.Input.Validate() != nil || r.Approval.Input.ServerID != a.ServerID || len(r.PrivateKey) != 32 || len(r.Approval.TargetKey) != 32 || r.PairingRequest.Validate() != nil || seen[r.Approval.Input.ID] || r.Approval.ExpiresAt.IsZero() {
			return approvalJournal{}, recovery()
		}
		controls := map[domain.ID]bool{}
		if len(r.WorkerControls) > 64 {
			return approvalJournal{}, recovery()
		}
		for _, c := range r.WorkerControls {
			if c.ID.Validate() != nil || controls[c.ID] || (c.Action != WorkerStart && c.Action != WorkerStop) || (c.Generation != "" && c.Generation.Validate() != nil) || domain.Text(c.Actor, "original actor", 256, true) != nil || r.Approval.Input.Role != domain.WorkerDevice || len(r.DeliveryDigest) != 32 {
				return approvalJournal{}, recovery()
			}
			controls[c.ID] = true
		}
		seen[r.Approval.Input.ID] = true
		key, err := ecdh.X25519().NewPrivateKey(r.PrivateKey)
		if err != nil || !bytes.Equal(key.PublicKey().Bytes(), r.Approval.TargetKey) {
			return approvalJournal{}, recovery()
		}
		code, err := confirmation(r.Approval.Input, r.Approval.TargetKey)
		if err != nil || code != r.Approval.Code {
			return approvalJournal{}, recovery()
		}
		switch r.Approval.State {
		case ApprovalPending, ApprovalApproved, ApprovalDenied, ApprovalExpired, ApprovalCanceled:
		default:
			return approvalJournal{}, recovery()
		}
	}
	return j, nil
}
func (a *Approvals) save(j approvalJournal) error {
	raw, err := json.Marshal(j)
	if err != nil {
		return recovery()
	}
	if len(raw) > 2<<20 {
		return domain.Fail(domain.ResourceExhausted, "The original approval history is full.", "Preserve accepted original approval and Worker records.")
	}
	if err := security.WriteAtomic(filepath.Join(a.Root, "tailscale-approvals.owner"), []byte(a.ServerID)); err != nil {
		return err
	}
	return security.WriteAtomic(filepath.Join(a.Root, "tailscale-approvals.json"), raw)
}
func recovery() error {
	return domain.Fail(domain.RecoveryRequired, "The original Tailscale operation requires reconciliation.", "Inspect the retained original operation without replacing it.")
}
func transcript(i ApprovalInput, target []byte) ([]byte, error) {
	if i.Validate() != nil || len(target) != 32 {
		return nil, invalid()
	}
	return json.Marshal(struct {
		Input     ApprovalInput
		Request   domain.ID
		Requester []byte
		Target    []byte
		Server    domain.ID
		Origin    string
		Role      domain.DeviceType
	}{i, i.ID, i.RequesterKey, target, i.ServerID, i.Origin, i.Role})
}
func confirmation(i ApprovalInput, target []byte) (string, error) {
	raw, err := transcript(i, target)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%06d", binary.BigEndian.Uint32(sum[:4])%1000000), nil
}
func (a *Approvals) Request(i ApprovalInput) (Approval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if i.Validate() != nil || i.ServerID != a.ServerID || i.Origin != a.Origin {
		return Approval{}, invalid()
	}
	j, err := a.load()
	if err != nil {
		return Approval{}, err
	}
	for _, r := range j.Requests {
		if r.Approval.Input.ID == i.ID {
			left, _ := json.Marshal(r.Approval.Input)
			right, _ := json.Marshal(i)
			if !bytes.Equal(left, right) {
				return Approval{}, domain.Fail(domain.Conflict, "The original approval request changed.", "Reconcile the retained request.")
			}
			return a.project(r), nil
		}
	}
	if len(j.Requests) >= maxApprovalRecords {
		return Approval{}, domain.Fail(domain.ResourceExhausted, "The approval history is full.", "Retain original approval ownership; no records were discarded.")
	}
	key, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Approval{}, err
	}
	peerKey, err := ecdh.X25519().NewPublicKey(i.RequesterKey)
	if err != nil {
		return Approval{}, invalid()
	}
	if _, err := key.ECDH(peerKey); err != nil {
		return Approval{}, invalid()
	}
	code, err := confirmation(i, key.PublicKey().Bytes())
	if err != nil {
		return Approval{}, err
	}
	grant := make([]byte, 32)
	if _, err := rand.Read(grant); err != nil {
		return Approval{}, err
	}
	r := approvalIntent{Approval: Approval{Input: i, TargetKey: key.PublicKey().Bytes(), Code: code, ExpiresAt: a.now().Add(5 * time.Minute), State: ApprovalPending}, PrivateKey: key.Bytes(), PairingRequest: domain.NewID(), GrantCode: base64.RawURLEncoding.EncodeToString(grant)}
	j.Requests = append(j.Requests, r)
	if err := a.save(j); err != nil {
		return Approval{}, err
	}
	return a.project(r), nil
}
func (a *Approvals) project(r approvalIntent) Approval {
	v := r.Approval
	v.DecisionID = r.DecisionID
	v.DecisionAllow = r.DecisionAllow
	v.Input.RequesterKey = append([]byte(nil), v.Input.RequesterKey...)
	v.TargetKey = append([]byte(nil), v.TargetKey...)
	v.EncryptedGrant = append([]byte(nil), v.EncryptedGrant...)
	if v.State == ApprovalPending && !v.ExpiresAt.After(a.now()) {
		v.State = ApprovalExpired
	}
	return v
}
func (a *Approvals) Get(id domain.ID, requester []byte) (Approval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, err := a.load()
	if err != nil {
		return Approval{}, err
	}
	for _, r := range j.Requests {
		if r.Approval.Input.ID == id && subtle.ConstantTimeCompare(requester, r.Approval.Input.RequesterKey) == 1 {
			return a.project(r), nil
		}
	}
	return Approval{}, domain.Fail(domain.NotFound, "The approval request is unavailable.", "Inspect the original request identity.")
}
func (a *Approvals) List() ([]Approval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, err := a.load()
	if err != nil {
		return nil, err
	}
	result := []Approval{}
	for _, r := range j.Requests {
		v := a.project(r)
		v.EncryptedGrant = nil
		result = append(result, v)
	}
	return result, nil
}
func (a *Approvals) Cancel(id domain.ID, requester []byte) (Approval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, err := a.load()
	if err != nil {
		return Approval{}, err
	}
	for n, r := range j.Requests {
		if r.Approval.Input.ID != id || subtle.ConstantTimeCompare(requester, r.Approval.Input.RequesterKey) != 1 {
			continue
		}
		v := a.project(r)
		if v.State == ApprovalPending && r.DecisionID == "" {
			j.Requests[n].Approval.State = ApprovalCanceled
			if err := a.save(j); err != nil {
				return Approval{}, err
			}
			return a.project(j.Requests[n]), nil
		}
		return v, nil
	}
	return Approval{}, invalid()
}

// IssueGrant must use the existing Device service with the persisted pairing
// request/verifier. Losing its response never creates a replacement grant.
type IssueGrant func(context.Context, domain.ID, string, domain.DeviceType, []byte) ([]byte, error)

func (a *Approvals) Decide(ctx context.Context, id, decision domain.ID, code string, allow bool, actor string, issue IssueGrant) (Approval, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if decision.Validate() != nil || len(code) != 6 || domain.Text(actor, "decision actor", 256, true) != nil {
		return Approval{}, invalid()
	}
	j, err := a.load()
	if err != nil {
		return Approval{}, err
	}
	for n, r := range j.Requests {
		if r.Approval.Input.ID != id {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(code), []byte(r.Approval.Code)) != 1 {
			return Approval{}, domain.Fail(domain.PermissionDenied, "The confirmation code does not match.", "Compare the original code on both devices.")
		}
		if r.DecisionID != "" && (r.DecisionID != decision || r.DecisionAllow != allow || r.DecisionActor != actor) {
			return Approval{}, domain.Fail(domain.Conflict, "The approval decision already has an owner.", "Reconcile that exact decision.")
		}
		v := a.project(r)
		if v.State != ApprovalPending && !(v.State == ApprovalExpired && r.DecisionID != "") {
			return v, nil
		}
		if r.DecisionID == "" {
			r.DecisionID = decision
			r.DecisionAllow = allow
			r.DecisionActor = actor
			j.Requests[n] = r
			if err := a.save(j); err != nil {
				return Approval{}, err
			}
		}
		if !allow {
			r.Approval.State = ApprovalDenied
		} else {
			// Persisted original intent precedes the only grant operation. Exact retry
			// reuses its original verifier and Device receipt after an uncertain result.
			digest := sha256.Sum256([]byte(r.GrantCode))
			grant, err := issue(ctx, r.PairingRequest, r.GrantCode, r.Approval.Input.Role, digest[:])
			if err != nil {
				return Approval{}, err
			}
			encrypted, err := sealGrant(r, grant)
			if err != nil {
				return Approval{}, err
			}
			r.Approval.EncryptedGrant = encrypted
			r.Approval.State = ApprovalApproved
		}
		j.Requests[n] = r
		if err := a.save(j); err != nil {
			return Approval{}, err
		}
		return a.project(r), nil
	}
	return Approval{}, invalid()
}
func grantCipher(private, public, associated []byte) (cipher.AEAD, error) {
	key, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		return nil, err
	}
	peer, err := ecdh.X25519().NewPublicKey(public)
	if err != nil {
		return nil, err
	}
	shared, err := key.ECDH(peer)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(append(append([]byte("delidev-tailscale-grant-v1\x00"), shared...), associated...))
	block, err := aes.NewCipher(sum[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
func sealGrant(r approvalIntent, grant []byte) ([]byte, error) {
	if len(grant) > 4<<10 {
		return nil, invalid()
	}
	associated, err := transcript(r.Approval.Input, r.Approval.TargetKey)
	if err != nil {
		return nil, err
	}
	aead, err := grantCipher(r.PrivateKey, r.Approval.Input.RequesterKey, associated)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, grant, associated), nil
}
func OpenGrant(private []byte, a Approval) ([]byte, error) {
	key, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil || !bytes.Equal(key.PublicKey().Bytes(), a.Input.RequesterKey) || a.State != ApprovalApproved || len(a.EncryptedGrant) > 33<<10 {
		return nil, invalid()
	}
	associated, err := transcript(a.Input, a.TargetKey)
	if err != nil {
		return nil, err
	}
	aead, err := grantCipher(private, a.TargetKey, associated)
	if err != nil || len(a.EncryptedGrant) < aead.NonceSize() {
		return nil, invalid()
	}
	return aead.Open(nil, a.EncryptedGrant[:aead.NonceSize()], a.EncryptedGrant[aead.NonceSize():], associated)
}

// WorkerDelivery is encrypted for the originally approved target. Its grant
// belongs to the requester's selected server, never the target's own server.
type WorkerDelivery struct {
	Token string          `json:"token"`
	Grant json.RawMessage `json:"grant"`
}

func SealWorkerDelivery(private []byte, a Approval, payload []byte) ([]byte, error) {
	if a.State != ApprovalApproved || a.Input.Role != domain.WorkerDevice || len(payload) > 32<<10 {
		return nil, invalid()
	}
	key, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil || !bytes.Equal(key.PublicKey().Bytes(), a.Input.RequesterKey) {
		return nil, invalid()
	}
	associated, err := transcript(a.Input, a.TargetKey)
	if err != nil {
		return nil, err
	}
	associated = append([]byte("worker-delivery-v1\x00"), associated...)
	c, err := grantCipher(private, a.TargetKey, associated)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, c.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return nil, err
	}
	return c.Seal(nonce, nonce, payload, associated), nil
}
func (a *Approvals) DeliverWorker(ctx context.Context, id domain.ID, requester, sealed []byte, consume func(context.Context, ApprovalInput, json.RawMessage, bool) (domain.ID, error)) (domain.ID, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(sealed) > 33<<10 || id.Validate() != nil {
		return "", invalid()
	}
	j, err := a.load()
	if err != nil {
		return "", err
	}
	for n, r := range j.Requests {
		if r.Approval.Input.ID != id {
			continue
		}
		if r.Approval.State != ApprovalApproved || r.Approval.Input.Role != domain.WorkerDevice || !bytes.Equal(requester, r.Approval.Input.RequesterKey) {
			return "", invalid()
		}
		digest := sha256.Sum256(sealed)
		fresh := len(r.DeliveryDigest) == 0
		if !fresh && !bytes.Equal(r.DeliveryDigest, digest[:]) {
			return "", domain.Fail(domain.Conflict, "The original Worker delivery changed.", "Reconcile the retained delivery.")
		}
		if r.WorkerDeviceID != "" {
			return r.WorkerDeviceID, nil
		}
		associated, err := transcript(r.Approval.Input, r.Approval.TargetKey)
		if err != nil {
			return "", err
		}
		associated = append([]byte("worker-delivery-v1\x00"), associated...)
		c, err := grantCipher(r.PrivateKey, requester, associated)
		if err != nil || len(sealed) < c.NonceSize() {
			return "", invalid()
		}
		raw, err := c.Open(nil, sealed[:c.NonceSize()], sealed[c.NonceSize():], associated)
		if err != nil {
			return "", invalid()
		}
		defer clear(raw)
		var delivery WorkerDelivery
		if domain.Decode(raw, &delivery) != nil || subtle.ConstantTimeCompare([]byte(delivery.Token), []byte(r.GrantCode)) != 1 {
			return "", invalid()
		}
		if fresh {
			r.DeliveryDigest = append([]byte(nil), digest[:]...)
			j.Requests[n] = r
			if err = a.save(j); err != nil {
				return "", err
			}
		}
		device, err := consume(ctx, r.Approval.Input, delivery.Grant, fresh)
		if err != nil {
			return "", err
		}
		if device.Validate() != nil {
			return "", recovery()
		}
		r.WorkerDeviceID = device
		j.Requests[n] = r
		if err = a.save(j); err != nil {
			return "", err
		}
		return device, nil
	}
	return "", invalid()
}

type WorkerAction string

const (
	WorkerStart WorkerAction = "start"
	WorkerStop  WorkerAction = "stop"
)

type WorkerControlPhase int

const (
	WorkerValidate WorkerControlPhase = iota
	WorkerMutate
	WorkerObserve
)

type WorkerControl struct {
	ID         domain.ID    `json:"id"`
	Action     WorkerAction `json:"action"`
	Generation domain.ID    `json:"generation,omitempty"`
	Actor      string       `json:"actor"`
}

func (a *Approvals) OwnedWorker(id domain.ID) (ApprovalInput, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	j, err := a.load()
	if err != nil {
		return ApprovalInput{}, err
	}
	for _, r := range j.Requests {
		if r.Approval.Input.ID == id && r.Approval.State == ApprovalApproved && r.Approval.Input.Role == domain.WorkerDevice && len(r.DeliveryDigest) == 32 {
			return r.Approval.Input, nil
		}
	}
	return ApprovalInput{}, domain.Fail(domain.NotFound, "The original approved Worker delivery is unavailable.", "Complete its original delivery; do not register a replacement.")
}

// ControlWorker retains the original action before controller I/O. Receipt
// replay only observes its original scope; it cannot repeat Start or Stop.
func (a *Approvals) ControlWorker(ctx context.Context, id domain.ID, action WorkerControl, run func(context.Context, ApprovalInput, WorkerControlPhase) error) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if action.ID.Validate() != nil || (action.Action != WorkerStart && action.Action != WorkerStop) || action.Generation != "" && action.Generation.Validate() != nil || domain.Text(action.Actor, "original actor", 256, true) != nil {
		return invalid()
	}
	j, err := a.load()
	if err != nil {
		return err
	}
	for n, r := range j.Requests {
		if r.Approval.Input.ID != id {
			continue
		}
		if r.Approval.State != ApprovalApproved || r.Approval.Input.Role != domain.WorkerDevice || len(r.DeliveryDigest) != 32 {
			return invalid()
		}
		for _, old := range r.WorkerControls {
			if old.ID == action.ID {
				if old != action {
					return domain.Fail(domain.Conflict, "The original Worker action changed.", "Read the original Worker scope and action.")
				}
				return run(ctx, r.Approval.Input, WorkerObserve)
			}
		}
		if len(r.WorkerControls) >= 64 {
			return domain.Fail(domain.ResourceExhausted, "The original Worker control history is full.", "Preserve its lifecycle and accepted actions.")
		}
		// The callback verifies original credential/generation before retention.
		if err = run(ctx, r.Approval.Input, WorkerValidate); err != nil {
			return err
		}
		r.WorkerControls = append(r.WorkerControls, action)
		j.Requests[n] = r
		if err = a.save(j); err != nil {
			return err
		}
		return run(ctx, r.Approval.Input, WorkerMutate)
	}
	return invalid()
}

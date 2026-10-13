// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/connections"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/tailscale"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type tailnetRecord struct {
	Approval      tailscale.Approval  `json:"approval"`
	PrivateKey    []byte              `json:"private_key"`
	PairRequest   domain.ID           `json:"pair_request,omitempty"`
	Grant         *worker.PairingCode `json:"grant,omitempty"`
	Actor         *domain.Principal   `json:"actor,omitempty"`
	Allow         *bool               `json:"allow,omitempty"`
	IncomingGrant []byte              `json:"incoming_grant,omitempty"`
	PairStarted   bool                `json:"pair_started"`
	// RequesterHost is a SHA-256 digest of the unauthenticated tailnet peer host
	// that created the request. It bounds live unapproved requests per host and
	// is never exposed in public approval documents.
	RequesterHost string `json:"requester_host,omitempty"`
}

// Tailnet approval lifecycle limits. They are shared by Worker delivery
// recovery and by approval record reclamation (see the connections contract).
const (
	tailnetRequestLifetime = 5 * time.Minute
	// tailnetRecoveryWindow is how long after ExpiresAt an approval record stays
	// retained and an Approved Worker setup can still be recovered.
	tailnetRecoveryWindow = 24 * time.Hour
	// tailnetApprovalLimit is the maximum retained records per approvals directory.
	tailnetApprovalLimit = 256
	// tailnetHostPendingLimit is the maximum live unapproved Pending records one
	// remote tailnet host may hold at the same time.
	tailnetHostPendingLimit = 8
)

// tailnetAdmissionReason is the stable reason recorded when a new approval
// request is refused for capacity.
type tailnetAdmissionReason string

const (
	tailnetAdmissionDirectoryFull tailnetAdmissionReason = "directory_full"
	tailnetAdmissionHostLimit     tailnetAdmissionReason = "host_limit"
)

type tailnetHostKey struct{}

// tailnetHostDigest returns the stable digest stored with a request for the
// given remote address; unattributable callers share the empty digest.
func tailnetHostDigest(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil || host == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(host))
	return hex.EncodeToString(sum[:16])
}

// tailnetPeerAddress returns the "host:port" address of the originating tailnet
// peer. The ingress listens only on loopback behind `tailscale serve`, so
// RemoteAddr is the local Serve proxy shared by every peer. Serve overwrites
// X-Forwarded-For with the peer's tailnet address on each proxied request, so a
// single well-formed address from a loopback connection identifies the peer.
// Anything else (no header, several values, a zoned or unspecified address, or a
// non-loopback connection) falls back to RemoteAddr, which only makes the shared
// bucket stricter. The value is used for abuse bounds only and grants no trust.
func tailnetPeerAddress(r *http.Request) string {
	remote, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	if proxy, err := netip.ParseAddr(remote); err != nil || !proxy.IsLoopback() {
		return r.RemoteAddr
	}
	forwarded := r.Header.Values("X-Forwarded-For")
	if len(forwarded) != 1 {
		return r.RemoteAddr
	}
	peer, err := netip.ParseAddr(strings.TrimSpace(forwarded[0]))
	if err != nil || peer.Zone() != "" || peer.IsUnspecified() {
		return r.RemoteAddr
	}
	return net.JoinHostPort(peer.Unmap().String(), "0")
}
func tailnetRequestHost(ctx context.Context) string {
	host, _ := ctx.Value(tailnetHostKey{}).(string)
	return host
}

// tailnetClock returns the service clock; tests replace it to cross expiry.
func (s *Service) tailnetClock() time.Time {
	if s.tailnetNow != nil {
		return s.tailnetNow()
	}
	return time.Now()
}
func (s *Service) tailnetHTTPClient() (*http.Client, *http.Transport) {
	if s.tailnetHTTP != nil {
		return s.tailnetHTTP()
	}
	return rpc.HTTPClient()
}
func (s *Service) tailnetRetryPair(ctx context.Context, root string, grant worker.PairingCode, kind domain.DeviceType, name string) (worker.Credential, error) {
	if s.tailnetPair != nil {
		return s.tailnetPair(ctx, root, grant, kind, name)
	}
	return worker.RetryPair(ctx, root, grant, kind, name)
}

// tailnetRecoverableUntil is the last instant at which an Approved Worker setup
// without a delivered grant may still be reconciled through its original request.
func tailnetRecoverableUntil(a tailscale.Approval) time.Time {
	return a.ExpiresAt.Add(tailnetRecoveryWindow)
}

// tailnetReclaimable reports whether an approval record may be removed. Only
// unapproved Pending records, Denied/Expired terminal records and Approved Worker
// records that never accepted a delivery are reclaimable, and only after the
// recovery window that follows ExpiresAt. Completed receipts, client approvals
// that froze an original grant and any record holding a delivered Worker grant
// are the original receipts for exact replay and are never reclaimed.
func tailnetReclaimable(v tailnetRecord, now time.Time) bool {
	if !now.After(tailnetRecoverableUntil(v.Approval)) {
		return false
	}
	switch v.Approval.State {
	case tailscale.Denied, tailscale.Expired:
		return true
	case tailscale.Pending:
		return v.Allow == nil || !*v.Allow || v.Grant == nil
	case tailscale.Approved:
		return v.Approval.Role == tailscale.WorkerRole && v.IncomingGrant == nil
	}
	return false
}

type tailnetOutgoing struct {
	Version        int
	ID             domain.ID
	PeerID         string
	Name           string
	Role           pb.DeviceType
	Actor          domain.Principal
	Origin         string
	ServerID       domain.ID
	PrivateKey     []byte
	PublicKey      []byte
	Approval       *tailscale.Approval
	Grant          *worker.PairingCode
	GrantRequest   domain.ID
	EncryptedGrant []byte
	ProfileID      domain.ID
	Complete       bool
}

func tailnetFailure(code domain.Code) error {
	return domain.Fail(code, "The original Tailscale operation requires inspection.", "Retain and reconcile its original request. Use manual pairing or SSH when the peer is unsupported.")
}
func (s *Service) tailnetRoot() string { return filepath.Join(s.Store.Root(), "tailscale") }
func (s *Service) tailnetRead(ctx context.Context) (tailscale.Inventory, error) {
	if s.tailscale == nil {
		return tailscale.Inventory{}, tailnetFailure(domain.Unsupported)
	}
	path, err := tailscale.Resolve()
	if err != nil {
		return tailscale.Inventory{}, err
	}
	root := filepath.Join(s.tailnetRoot(), "observations")
	err = security.PrivateDir(root)
	if err != nil {
		return tailscale.Inventory{}, err
	}
	return (tailscale.Adapter{Executable: path, Root: root, Logger: s.logger}).Status(ctx)
}
func tailnetJSON(value any) []byte { raw, _ := json.Marshal(value); return raw }
func (s *Service) DiscoverTailscale(ctx context.Context, req *connect.Request[pb.DiscoverTailscaleRequest]) (*connect.Response[pb.DiscoverTailscaleResponse], error) {
	v, err := s.tailnetRead(ctx)
	if err != nil {
		return nil, rpc.Error(err, req.Header().Get(rpc.CorrelationHeader))
	}
	return connect.NewResponse(&pb.DiscoverTailscaleResponse{DocumentJson: tailnetJSON(v)}), nil
}
func (s *Service) tailnetPeer(ctx context.Context, id string) (tailscale.Peer, error) {
	v, err := s.tailnetRead(ctx)
	if err != nil {
		return tailscale.Peer{}, err
	}
	for _, p := range v.Peers {
		if p.ID == id && !p.SharedTo && p.Online {
			return p, nil
		}
	}
	return tailscale.Peer{}, tailnetFailure(domain.Unavailable)
}

type tailnetIdentity struct {
	Version         int       `json:"version"`
	ServerID        domain.ID `json:"server_id"`
	Origin          string    `json:"origin"`
	ProtocolVersion int       `json:"protocol_version"`
	Approval        bool      `json:"approval"`
}

func (s *Service) CheckTailscale(ctx context.Context, req *connect.Request[pb.CheckTailscaleRequest]) (*connect.Response[pb.CheckTailscaleResponse], error) {
	peer, err := s.tailnetPeer(ctx, req.Msg.PeerId)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	origin, _ := tailscale.Origin(peer.Host)
	var v tailnetIdentity
	if err := tailscale.Probe(ctx, origin, &v); err != nil {
		return nil, rpc.Error(err, "")
	}
	if v.Version != 1 || v.ServerID.Validate() != nil || v.Origin != origin || v.ProtocolVersion != rpc.ProtocolVersion || !v.Approval {
		return nil, rpc.Error(tailnetFailure(domain.Unsupported), "")
	}
	return connect.NewResponse(&pb.CheckTailscaleResponse{DocumentJson: tailnetJSON(v)}), nil
}
func (s *Service) GetTailscaleAccess(ctx context.Context, req *connect.Request[pb.GetTailscaleAccessRequest]) (*connect.Response[pb.GetTailscaleAccessResponse], error) {
	if s.tailscale == nil {
		return nil, rpc.Error(tailnetFailure(domain.Unsupported), "")
	}
	return connect.NewResponse(&pb.GetTailscaleAccessResponse{DocumentJson: tailnetJSON(s.tailscale.Status())}), nil
}
func (s *Service) SetTailscaleAccess(ctx context.Context, req *connect.Request[pb.SetTailscaleAccessRequest]) (*connect.Response[pb.SetTailscaleAccessResponse], error) {
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || !s.localTailnetActor(actor) || s.tailscale == nil || s.stopping.Load() {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, "")
	}
	v, err := s.tailscale.Set(ctx, domain.ID(req.Msg.RequestId), req.Msg.Enabled)
	if err != nil {
		s.logger.WarnContext(ctx, "tailscale_access", "phase", "set", "code", domain.SafeError(err).Code)
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.SetTailscaleAccessResponse{DocumentJson: tailnetJSON(v)}), nil
}
func (s *Service) tailnetIngress(origin string) http.Handler {
	business := s.Handler([]string{"tauri://localhost", "http://tauri.localhost", "https://tauri.localhost"}, false)
	public := http.NewServeMux()
	public.Handle(delidevv1connect.NewTailscaleServiceHandler(s, connect.WithReadMaxBytes(32<<10), connect.WithSendMaxBytes(32<<10)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if s.stopping.Load() {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/delidev/tailscale/v1/identity" && r.Method == http.MethodGet {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(tailnetIdentity{1, s.Identity.ServerID, origin, rpc.ProtocolVersion, true})
			return
		}
		if s.tailscale == nil || s.tailscale.Status().State != tailscale.Ready {
			http.Error(w, "unavailable", http.StatusServiceUnavailable)
			return
		}
		switch r.URL.Path {
		case delidevv1connect.TailscaleServiceCancelTailscaleApprovalProcedure, delidevv1connect.TailscaleServiceRequestTailscaleApprovalProcedure, delidevv1connect.TailscaleServiceGetTailscaleApprovalProcedure, delidevv1connect.TailscaleServiceDeliverTailscaleWorkerProcedure:
			peer := tailnetPeerAddress(r)
			if !s.pairingAllowed(peer) {
				http.Error(w, "limited", http.StatusTooManyRequests)
				return
			}
			public.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tailnetHostKey{}, tailnetHostDigest(peer))))
			return
		}
		// Mount authenticated Connect business transports only. Private native relay,
		// desktop generation challenge, locator and unauthenticated UI never appear.
		if len(r.URL.Path) < len("/delidev.v1.") || r.URL.Path[:len("/delidev.v1.")] != "/delidev.v1." {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == delidevv1connect.TailscaleServiceCheckTailscaleProcedure || r.URL.Path == delidevv1connect.TailscaleServiceBeginTailscaleConnectionProcedure || r.URL.Path == delidevv1connect.TailscaleServiceResumeTailscaleConnectionProcedure || r.URL.Path == delidevv1connect.TailscaleServiceCancelTailscaleConnectionProcedure || r.URL.Path == delidevv1connect.TailscaleServiceDiscoverTailscaleProcedure || r.URL.Path == delidevv1connect.TailscaleServiceSetTailscaleAccessProcedure || r.URL.Path == delidevv1connect.SystemServiceStopServerProcedure {
			http.Error(w, "denied", http.StatusForbidden)
			return
		}
		business.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tailnetOriginKey{}, origin)))
	})
}

// Bound enumeration before allocating a complete directory listing. Retained
// requests are inspected explicitly; reaching the limit never deletes receipts.
func tailnetEntries(path string) ([]os.DirEntry, error) {
	if err := security.CheckPrivateDir(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	entries, err := f.ReadDir(257)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, nil
}

func (s *Service) tailnetPath(id domain.ID) string {
	return filepath.Join(s.tailnetRoot(), "approvals", string(id)+".json")
}
func (s *Service) readTailnet(id domain.ID) (tailnetRecord, error) {
	var v tailnetRecord
	if id.Validate() != nil {
		return v, tailnetFailure(domain.InvalidArgument)
	}
	if err := security.CheckPrivateDir(filepath.Join(s.tailnetRoot(), "approvals")); err != nil {
		return v, err
	}
	raw, err := security.ReadPrivate(s.tailnetPath(id), 64<<10)
	if err != nil {
		return v, err
	}
	defer clear(raw)
	if domain.Decode(raw, &v) != nil || v.Approval.ID != id || v.Approval.ServerID != s.Identity.ServerID || v.Approval.Code != v.Approval.Confirmation() || v.Approval.Validate() != nil || !tailscale.MatchesKey(v.PrivateKey, v.Approval.TargetKey) {
		return v, tailnetFailure(domain.RecoveryRequired)
	}
	return v, nil
}
func (s *Service) writeTailnet(v tailnetRecord) error {
	return security.WriteAtomic(s.tailnetPath(v.Approval.ID), tailnetJSON(v))
}
func publicApproval(v tailnetRecord, grant bool, now time.Time) tailscale.Approval {
	a := v.Approval
	if a.State == tailscale.Pending && now.After(a.ExpiresAt) && (v.Allow == nil || !*v.Allow || v.Grant == nil) {
		a.State = tailscale.Expired
	}
	if !grant {
		a.EncryptedGrant = nil
	}
	return a
}

// tailnetAdmission is the retained-capacity view used to admit a new request.
type tailnetAdmission struct {
	// Records counts every retained entry in the approvals directory, including
	// entries this service does not own and therefore never touches.
	Records int
	// HostPending counts live unapproved Pending records by requester host digest.
	HostPending map[string]int
	Reclaimed   int
}

// reclaimTailnet removes only approval records whose recovery window ended (see
// tailnetReclaimable) and returns the remaining capacity view. It is derived
// purely from durable records, so it behaves identically after a restart. Foreign,
// malformed, non-regular or unreadable entries are retained and still count
// against capacity; outgoing journals live in a separate directory and are never
// visited. The caller must hold tailnetMu.
func (s *Service) reclaimTailnet(ctx context.Context, directory string, now time.Time) (tailnetAdmission, error) {
	var result tailnetAdmission
	// Each pass lists a bounded directory; a pass that removes nothing is final.
	for pass := 0; pass < 4; pass++ {
		entries, err := tailnetEntries(directory)
		if err != nil {
			return result, err
		}
		removed := 0
		result.Records = len(entries)
		result.HostPending = map[string]int{}
		for _, e := range entries {
			name := e.Name()
			if !e.Type().IsRegular() || len(name) <= len(".json") || filepath.Ext(name) != ".json" {
				continue
			}
			id := domain.ID(name[:len(name)-len(".json")])
			v, err := s.readTailnet(id)
			if err != nil {
				continue
			}
			if tailnetReclaimable(v, now) {
				if err := os.Remove(s.tailnetPath(id)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return result, err
				}
				if err := security.SyncParent(s.tailnetPath(id)); err != nil {
					return result, err
				}
				s.logger.InfoContext(ctx, "tailscale_approval", "operation_id", id, "phase", "reclaimed", "state", v.Approval.State, "role", v.Approval.Role)
				removed++
				result.Records--
				result.Reclaimed++
				continue
			}
			if v.Approval.State == tailscale.Pending && v.Allow == nil && !now.After(v.Approval.ExpiresAt) {
				result.HostPending[v.RequesterHost]++
			}
		}
		if removed == 0 {
			return result, nil
		}
	}
	return result, nil
}
func (s *Service) RequestTailscaleApproval(ctx context.Context, req *connect.Request[pb.RequestTailscaleApprovalRequest]) (*connect.Response[pb.RequestTailscaleApprovalResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	m := req.Msg
	if s.tailscale == nil || s.stopping.Load() {
		return nil, rpc.Error(tailnetFailure(domain.Unavailable), "")
	}
	access := s.tailscale.Status()
	role := tailscale.ClientRole
	if m.Role == pb.DeviceType_DEVICE_TYPE_WORKER {
		role = tailscale.WorkerRole
	} else if m.Role != pb.DeviceType_DEVICE_TYPE_CLIENT {
		return nil, rpc.Error(tailnetFailure(domain.InvalidArgument), "")
	}
	if !access.Enabled || access.State != tailscale.Ready || m.ServerId != string(s.Identity.ServerID) || m.Origin != access.Origin || domain.ID(m.RequestId).Validate() != nil || len(m.RequesterKey) != 32 || domain.Text(m.Name, "requester name", 256, true) != nil {
		return nil, rpc.Error(tailnetFailure(domain.InvalidArgument), "")
	}
	if role == "worker" && (domain.ID(m.SelectedServerId).Validate() != nil || tailscale.ValidateOrigin(m.SelectedOrigin) != nil) {
		return nil, rpc.Error(tailnetFailure(domain.InvalidArgument), "")
	}
	v, err := s.readTailnet(domain.ID(m.RequestId))
	if errors.Is(err, os.ErrNotExist) {
		directory := filepath.Join(s.tailnetRoot(), "approvals")
		if err := security.PrivateDir(directory); err != nil {
			return nil, rpc.Error(err, "")
		}
		host := tailnetRequestHost(ctx)
		admission, err := s.reclaimTailnet(ctx, directory, s.tailnetClock())
		if err != nil {
			return nil, rpc.Error(tailnetFailure(domain.ResourceExhausted), "")
		}
		if admission.Records >= tailnetApprovalLimit {
			s.logger.WarnContext(ctx, "tailscale_approval", "operation_id", m.RequestId, "phase", "admission_rejected", "reason", tailnetAdmissionDirectoryFull, "records", admission.Records)
			return nil, rpc.Error(tailnetFailure(domain.ResourceExhausted), "")
		}
		if admission.HostPending[host] >= tailnetHostPendingLimit {
			s.logger.WarnContext(ctx, "tailscale_approval", "operation_id", m.RequestId, "phase", "admission_rejected", "reason", tailnetAdmissionHostLimit, "host_pending", admission.HostPending[host])
			return nil, rpc.Error(tailnetFailure(domain.ResourceExhausted), "")
		}
		private, public, err := tailscale.NewKey()
		if err != nil {
			return nil, rpc.Error(err, "")
		}
		v = tailnetRecord{PrivateKey: private, Approval: tailscale.Approval{ID: domain.ID(m.RequestId), ServerID: s.Identity.ServerID, Origin: m.Origin, Role: role, Name: m.Name, RequesterKey: m.RequesterKey, TargetKey: public, SelectedServer: domain.ID(m.SelectedServerId), SelectedOrigin: m.SelectedOrigin, ExpiresAt: s.tailnetClock().UTC().Add(tailnetRequestLifetime), State: tailscale.Pending}, RequesterHost: host}
		v.Approval.Code = v.Approval.Confirmation()
		if err = s.writeTailnet(v); err != nil {
			return nil, rpc.Error(err, "")
		}
	} else if err != nil {
		return nil, rpc.Error(err, "")
	}
	a := v.Approval
	if a.ServerID != domain.ID(m.ServerId) || a.Origin != m.Origin || a.Role != role || a.Name != m.Name || !bytes.Equal(a.RequesterKey, m.RequesterKey) || a.SelectedServer != domain.ID(m.SelectedServerId) || a.SelectedOrigin != m.SelectedOrigin {
		return nil, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	return connect.NewResponse(&pb.RequestTailscaleApprovalResponse{DocumentJson: tailnetJSON(publicApproval(v, true, s.tailnetClock()))}), nil
}
func (s *Service) GetTailscaleApproval(ctx context.Context, req *connect.Request[pb.GetTailscaleApprovalRequest]) (*connect.Response[pb.GetTailscaleApprovalResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	v, err := s.readTailnet(domain.ID(req.Msg.RequestId))
	if err != nil || !bytes.Equal(v.Approval.RequesterKey, req.Msg.RequesterKey) {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	return connect.NewResponse(&pb.GetTailscaleApprovalResponse{DocumentJson: tailnetJSON(publicApproval(v, true, s.tailnetClock()))}), nil
}
func (s *Service) ListTailscaleApprovals(ctx context.Context, req *connect.Request[pb.ListTailscaleApprovalsRequest]) (*connect.Response[pb.ListTailscaleApprovalsResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	entries, err := tailnetEntries(filepath.Join(s.tailnetRoot(), "approvals"))
	if errors.Is(err, os.ErrNotExist) {
		entries = nil
	} else if err != nil {
		return nil, rpc.Error(err, "")
	}
	if len(entries) > 256 {
		return nil, rpc.Error(tailnetFailure(domain.ResourceExhausted), "")
	}
	values := []tailscale.Approval{}
	for _, e := range entries {
		if e.IsDir() || len(e.Name()) <= 5 || filepath.Ext(e.Name()) != ".json" {
			return nil, rpc.Error(tailnetFailure(domain.RecoveryRequired), "")
		}
		id := domain.ID(e.Name()[:len(e.Name())-len(".json")])
		v, err := s.readTailnet(id)
		if err != nil {
			return nil, rpc.Error(err, "")
		}
		a := publicApproval(v, false, s.tailnetClock())
		if a.State == tailscale.Pending || (a.State == tailscale.Approved && a.Role == "worker") {
			values = append(values, a)
		}
	}
	return connect.NewResponse(&pb.ListTailscaleApprovalsResponse{DocumentJson: tailnetJSON(values)}), nil
}
func (s *Service) DecideTailscaleApproval(ctx context.Context, req *connect.Request[pb.DecideTailscaleApprovalRequest]) (*connect.Response[pb.DecideTailscaleApprovalResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type == domain.WorkerDevice {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	v, err := s.readTailnet(domain.ID(req.Msg.RequestId))
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	a := publicApproval(v, false, s.tailnetClock())
	if req.Msg.ConfirmationCode != a.Code || (v.Actor != nil && (*v.Actor != actor || v.Allow == nil || *v.Allow != req.Msg.Allow)) {
		return nil, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	if a.State == tailscale.Expired || a.State == tailscale.Denied || a.State == tailscale.Completed || (a.State == tailscale.Approved && (a.Role == "worker" || len(v.Approval.EncryptedGrant) > 0)) {
		return connect.NewResponse(&pb.DecideTailscaleApprovalResponse{DocumentJson: tailnetJSON(a)}), nil
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, "")
	}
	v.Actor = &actor
	v.Allow = &req.Msg.Allow
	if !req.Msg.Allow {
		v.Approval.State = tailscale.Denied
		if err := s.writeTailnet(v); err != nil {
			return nil, rpc.Error(err, "")
		}
		return connect.NewResponse(&pb.DecideTailscaleApprovalResponse{DocumentJson: tailnetJSON(v.Approval)}), nil
	}
	if a.Role == "client" && v.Grant == nil {
		code, err := worker.RandomToken()
		if err != nil {
			return nil, rpc.Error(err, "")
		}
		v.PairRequest = domain.NewID()
		v.Grant = &worker.PairingCode{Version: 1, ServerID: s.Identity.ServerID, Endpoint: a.Origin, Code: code}
	}
	// Persist original actor and random grant before CreatePairing. An uncertain
	// database response retries the same request/verifier rather than minting one.
	if err := s.writeTailnet(v); err != nil {
		return nil, rpc.Error(err, "")
	}
	if a.Role == "client" {
		digest := sha256.Sum256([]byte(v.Grant.Code))
		grant, err := s.CreatePairing(ctx, connect.NewRequest(&pb.CreatePairingRequest{RequestId: string(v.PairRequest), Name: a.Name, Type: pb.DeviceType_DEVICE_TYPE_CLIENT, CodeDigest: digest[:]}))
		if err != nil {
			return nil, err
		}
		v.Grant.PairingID = domain.ID(grant.Msg.Pairing.Id)
		plain := tailnetJSON(v.Grant)
		defer clear(plain)
		v.Approval.EncryptedGrant, err = tailscale.Encrypt(v.PrivateKey, a.RequesterKey, a.Transcript(), plain)
		if err != nil {
			return nil, rpc.Error(err, "")
		}
	}
	v.Approval.State = tailscale.Approved
	if err := s.writeTailnet(v); err != nil {
		return nil, rpc.Error(err, "")
	}
	s.logger.InfoContext(ctx, "tailscale_approval", "operation_id", a.ID, "phase", "approved")
	return connect.NewResponse(&pb.DecideTailscaleApprovalResponse{DocumentJson: tailnetJSON(publicApproval(v, false, s.tailnetClock()))}), nil
}

func (s *Service) outgoingPath(id domain.ID) string {
	return filepath.Join(s.tailnetRoot(), "outgoing", string(id)+".json")
}
func (s *Service) readOutgoing(id domain.ID) (tailnetOutgoing, error) {
	var v tailnetOutgoing
	if id.Validate() != nil {
		return v, tailnetFailure(domain.InvalidArgument)
	}
	if err := security.CheckPrivateDir(filepath.Join(s.tailnetRoot(), "outgoing")); err != nil {
		return v, err
	}
	raw, err := security.ReadPrivate(s.outgoingPath(id), 64<<10)
	if err != nil {
		return v, err
	}
	defer clear(raw)
	if domain.Decode(raw, &v) != nil || v.Version != 1 || v.ID != id || len(v.PrivateKey) != 32 || len(v.PublicKey) != 32 || !tailscale.MatchesKey(v.PrivateKey, v.PublicKey) || tailscale.ValidateOrigin(v.Origin) != nil || v.ServerID.Validate() != nil || v.ProfileID.Validate() != nil || domain.Text(v.Name, "connection name", 256, true) != nil || (v.Role != pb.DeviceType_DEVICE_TYPE_CLIENT && v.Role != pb.DeviceType_DEVICE_TYPE_WORKER) || (v.Role == pb.DeviceType_DEVICE_TYPE_WORKER && v.GrantRequest.Validate() != nil) {
		return v, tailnetFailure(domain.RecoveryRequired)
	}
	if v.Approval != nil && (v.Approval.Validate() != nil || v.Approval.ID != id || v.Approval.ServerID != v.ServerID || v.Approval.Origin != v.Origin || !bytes.Equal(v.Approval.RequesterKey, v.PublicKey)) {
		return v, tailnetFailure(domain.RecoveryRequired)
	}
	return v, nil
}
func (s *Service) writeOutgoing(v tailnetOutgoing) error {
	return security.WriteAtomic(s.outgoingPath(v.ID), tailnetJSON(v))
}
func outgoingPublic(v tailnetOutgoing) tailscale.Approval {
	if v.Approval != nil {
		a := *v.Approval
		a.EncryptedGrant = nil
		a.ProfileID = v.ProfileID
		if v.Complete {
			a.State = tailscale.Completed
		}
		return a
	}
	return tailscale.Approval{ID: v.ID, State: tailscale.Uncertain, ServerID: v.ServerID, Origin: v.Origin}
}
func (s *Service) BeginTailscaleConnection(ctx context.Context, req *connect.Request[pb.BeginTailscaleConnectionRequest]) (*connect.Response[pb.BeginTailscaleConnectionResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type == domain.WorkerDevice {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	m := req.Msg
	id := domain.ID(m.RequestId)
	if id.Validate() != nil || domain.Text(m.Name, "connection name", 256, true) != nil || (m.Role != pb.DeviceType_DEVICE_TYPE_CLIENT && m.Role != pb.DeviceType_DEVICE_TYPE_WORKER) {
		return nil, rpc.Error(tailnetFailure(domain.InvalidArgument), "")
	}
	v, err := s.readOutgoing(id)
	if errors.Is(err, os.ErrNotExist) {
		checked, err := s.CheckTailscale(ctx, connect.NewRequest(&pb.CheckTailscaleRequest{PeerId: m.PeerId}))
		if err != nil {
			return nil, err
		}
		var identity tailnetIdentity
		if json.Unmarshal(checked.Msg.DocumentJson, &identity) != nil {
			return nil, rpc.Error(tailnetFailure(domain.Unsupported), "")
		}
		if err := security.PrivateDir(filepath.Join(s.tailnetRoot(), "outgoing")); err != nil {
			return nil, rpc.Error(err, "")
		}
		entries, err := tailnetEntries(filepath.Join(s.tailnetRoot(), "outgoing"))
		if err != nil || len(entries) >= 256 {
			return nil, rpc.Error(tailnetFailure(domain.ResourceExhausted), "")
		}
		private, public, err := tailscale.NewKey()
		if err != nil {
			return nil, rpc.Error(err, "")
		}
		v = tailnetOutgoing{Version: 1, ID: id, PeerID: m.PeerId, Name: m.Name, Role: m.Role, Actor: actor, Origin: identity.Origin, ServerID: identity.ServerID, PrivateKey: private, PublicKey: public, ProfileID: domain.NewID()}
		if m.Role == pb.DeviceType_DEVICE_TYPE_WORKER {
			if s.tailscale.Status().State != tailscale.Ready {
				return nil, rpc.Error(tailnetFailure(domain.Unavailable), "")
			}
			v.GrantRequest = domain.NewID()
		}
		if err = s.writeOutgoing(v); err != nil {
			return nil, rpc.Error(err, "")
		}
	} else if err != nil {
		return nil, rpc.Error(err, "")
	}
	if v.PeerID != m.PeerId || v.Name != m.Name || v.Role != m.Role || v.Actor != actor {
		return nil, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	v, err = s.resumeOutgoing(ctx, v)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.BeginTailscaleConnectionResponse{DocumentJson: tailnetJSON(outgoingPublic(v))}), nil
}
func (s *Service) ResumeTailscaleConnection(ctx context.Context, req *connect.Request[pb.ResumeTailscaleConnectionRequest]) (*connect.Response[pb.ResumeTailscaleConnectionResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	v, err := s.readOutgoing(domain.ID(req.Msg.RequestId))
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor != v.Actor {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	v, err = s.resumeOutgoing(ctx, v)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.ResumeTailscaleConnectionResponse{DocumentJson: tailnetJSON(outgoingPublic(v))}), nil
}
func (s *Service) resumeOutgoing(ctx context.Context, v tailnetOutgoing) (tailnetOutgoing, error) {
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return v, rpc.Error(err, "")
	}
	if v.Complete {
		return v, nil
	}
	httpClient, transport := s.tailnetHTTPClient()
	defer transport.CloseIdleConnections()
	remote := delidevv1connect.NewTailscaleServiceClient(httpClient, v.Origin, connect.WithReadMaxBytes(32<<10), connect.WithSendMaxBytes(32<<10))
	ctx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	selectedOrigin := ""
	selectedServer := ""
	if v.Role == pb.DeviceType_DEVICE_TYPE_WORKER {
		selectedOrigin = s.tailscale.Status().Origin
		selectedServer = string(s.Identity.ServerID)
		if v.Approval != nil {
			selectedOrigin = v.Approval.SelectedOrigin
			selectedServer = string(v.Approval.SelectedServer)
		}
	}
	response, err := remote.RequestTailscaleApproval(ctx, connect.NewRequest(&pb.RequestTailscaleApprovalRequest{RequestId: string(v.ID), ServerId: string(v.ServerID), Origin: v.Origin, Role: v.Role, RequesterKey: v.PublicKey, Name: v.Name, SelectedServerId: selectedServer, SelectedOrigin: selectedOrigin}))
	if err != nil {
		return v, rpc.ClientError(err)
	}
	var approval tailscale.Approval
	expectedRole := tailscale.ClientRole
	if v.Role == pb.DeviceType_DEVICE_TYPE_WORKER {
		expectedRole = tailscale.WorkerRole
	}
	if domain.Decode(response.Msg.DocumentJson, &approval) != nil || approval.ID != v.ID || approval.ServerID != v.ServerID || approval.Origin != v.Origin || !bytes.Equal(approval.RequesterKey, v.PublicKey) || approval.Code != approval.Confirmation() || approval.Role != expectedRole || approval.Name != v.Name || string(approval.SelectedServer) != selectedServer || approval.SelectedOrigin != selectedOrigin || approval.Validate() != nil {
		return v, rpc.Error(tailnetFailure(domain.Unsupported), "")
	}
	if v.Approval != nil && !bytes.Equal(v.Approval.Transcript(), approval.Transcript()) {
		return v, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	v.Approval = &approval
	if err := s.writeOutgoing(v); err != nil {
		return v, rpc.Error(err, "")
	}
	if v.Role == pb.DeviceType_DEVICE_TYPE_WORKER && approval.State == tailscale.Completed && v.Grant != nil && len(v.EncryptedGrant) > 0 {
		// The target accepted the original delivery but its response was lost.
		// Reconcile to the same completed state; never create or deliver another grant.
		v.Complete = true
		if err := s.writeOutgoing(v); err != nil {
			return v, rpc.Error(err, "")
		}
		s.logger.InfoContext(ctx, "tailscale_approval", "operation_id", v.ID, "phase", "worker_delivery_reconciled")
		return v, nil
	}
	if approval.State != tailscale.Approved {
		return v, nil
	}
	if v.Role == pb.DeviceType_DEVICE_TYPE_CLIENT {
		plain, err := tailscale.Decrypt(v.PrivateKey, approval.TargetKey, approval.Transcript(), approval.EncryptedGrant)
		if err != nil {
			return v, rpc.Error(err, "")
		}
		defer clear(plain)
		var grant worker.PairingCode
		if domain.Decode(plain, &grant) != nil || grant.Validate() != nil || grant.ServerID != v.ServerID || grant.Endpoint != v.Origin {
			return v, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
		}
		profile, err := connections.Pair(ctx, s.Store.Root(), v.ProfileID, v.Name, grant)
		if err != nil {
			return v, rpc.Error(err, "")
		}
		v.ProfileID = profile.ID
		v.Complete = true
	} else {
		// An Approved receipt proves the target decided before ExpiresAt, so the
		// original grant is still created after expiry. Recovery is bounded by the
		// shared window that also governs record reclamation. The deadline stays
		// enforced until the encrypted delivery is durably journaled: a retained
		// grant without that journal (for example after a stop between persisting
		// it and CreatePairing) is not delivery-attempt evidence, and creating the
		// pairing after the window would leave one that the target rejects.
		if v.EncryptedGrant == nil && s.tailnetClock().After(tailnetRecoverableUntil(approval)) {
			return v, rpc.Error(tailnetFailure(domain.Conflict), "")
		}
		if v.Grant == nil {
			code, err := worker.RandomToken()
			if err != nil {
				return v, rpc.Error(err, "")
			}
			v.Grant = &worker.PairingCode{Version: 1, ServerID: s.Identity.ServerID, Endpoint: approval.SelectedOrigin, Code: code}
			if err := s.writeOutgoing(v); err != nil {
				return v, rpc.Error(err, "")
			}
		}
		digest := sha256.Sum256([]byte(v.Grant.Code))
		pairing, err := s.CreatePairing(ctx, connect.NewRequest(&pb.CreatePairingRequest{RequestId: string(v.GrantRequest), Name: v.Name, Type: pb.DeviceType_DEVICE_TYPE_WORKER, CodeDigest: digest[:]}))
		if err != nil {
			return v, err
		}
		v.Grant.PairingID = domain.ID(pairing.Msg.Pairing.Id)
		if v.EncryptedGrant == nil {
			plain := tailnetJSON(v.Grant)
			defer clear(plain)
			v.EncryptedGrant, err = tailscale.Encrypt(v.PrivateKey, approval.TargetKey, approval.Transcript(), plain)
			if err != nil {
				return v, rpc.Error(err, "")
			}
			if err := s.writeOutgoing(v); err != nil {
				return v, rpc.Error(err, "")
			}
		}
		delivered, err := remote.DeliverTailscaleWorker(ctx, connect.NewRequest(&pb.DeliverTailscaleWorkerRequest{RequestId: string(v.ID), RequesterKey: v.PublicKey, EncryptedGrant: v.EncryptedGrant}))
		if err != nil {
			return v, rpc.ClientError(err)
		}
		var final tailscale.Approval
		if domain.Decode(delivered.Msg.DocumentJson, &final) != nil || final.ID != v.ID || final.ServerID != v.ServerID || final.Validate() != nil || !bytes.Equal(final.Transcript(), approval.Transcript()) {
			return v, rpc.Error(tailnetFailure(domain.Unsupported), "")
		}
		v.Approval = &final
		v.Complete = final.State == tailscale.Completed
	}
	if err := s.writeOutgoing(v); err != nil {
		return v, rpc.Error(err, "")
	}
	return v, nil
}
func (s *Service) DeliverTailscaleWorker(ctx context.Context, req *connect.Request[pb.DeliverTailscaleWorkerRequest]) (*connect.Response[pb.DeliverTailscaleWorkerResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	v, err := s.readTailnet(domain.ID(req.Msg.RequestId))
	if err != nil || !bytes.Equal(v.Approval.RequesterKey, req.Msg.RequesterKey) || v.Approval.Role != "worker" || v.Actor == nil || v.Allow == nil || !*v.Allow || (v.Approval.State != tailscale.Approved && v.Approval.State != tailscale.Completed) {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	if v.IncomingGrant != nil && !bytes.Equal(v.IncomingGrant, req.Msg.EncryptedGrant) {
		return nil, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	if v.Approval.State == tailscale.Completed {
		return connect.NewResponse(&pb.DeliverTailscaleWorkerResponse{DocumentJson: tailnetJSON(publicApproval(v, false, s.tailnetClock()))}), nil
	}
	// Reauthenticate the original approving actor at commit/registration. The
	// unauthenticated key-bound delivery cannot substitute a later owner/client.
	owned := domain.WithPrincipal(ctx, *v.Actor)
	if err := s.Store.Read(owned, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, "")
	}
	// State Approved proves a timely decision; only the shared recovery window
	// bounds the first delivery.
	if s.tailnetClock().After(tailnetRecoverableUntil(v.Approval)) && v.IncomingGrant == nil {
		return nil, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	plain, err := tailscale.Decrypt(v.PrivateKey, v.Approval.RequesterKey, v.Approval.Transcript(), req.Msg.EncryptedGrant)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	defer clear(plain)
	var grant worker.PairingCode
	if domain.Decode(plain, &grant) != nil || grant.Validate() != nil || grant.ServerID != v.Approval.SelectedServer || grant.Endpoint != v.Approval.SelectedOrigin {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	root := filepath.Join(s.tailnetRoot(), "workers", string(v.Approval.ID))
	fresh := v.IncomingGrant == nil
	if fresh {
		if _, err := worker.PreparePair(owned, root, grant, domain.WorkerDevice, v.Approval.Name); err != nil {
			return nil, rpc.Error(err, "")
		}
		v.IncomingGrant = append([]byte(nil), req.Msg.EncryptedGrant...)
		if err := s.writeTailnet(v); err != nil {
			return nil, rpc.Error(err, "")
		}
	}
	credential, err := s.tailnetRetryPair(owned, root, grant, domain.WorkerDevice, v.Approval.Name)
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	if s.tailnetStartWorker == nil {
		return nil, rpc.Error(tailnetFailure(domain.Unsupported), "")
	}
	if err := s.tailnetStartWorker(owned, root); err != nil {
		return nil, rpc.Error(err, "")
	}
	v.Approval.MachineID = credential.MachineID
	v.Approval.State = tailscale.Completed
	if err := s.writeTailnet(v); err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.DeliverTailscaleWorkerResponse{DocumentJson: tailnetJSON(publicApproval(v, false, s.tailnetClock()))}), nil
}

func (s *Service) ListTailscaleConnections(ctx context.Context, req *connect.Request[pb.ListTailscaleConnectionsRequest]) (*connect.Response[pb.ListTailscaleConnectionsResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor.Type == domain.WorkerDevice {
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	entries, err := tailnetEntries(filepath.Join(s.tailnetRoot(), "outgoing"))
	if errors.Is(err, os.ErrNotExist) {
		entries = nil
	} else if err != nil {
		return nil, rpc.Error(err, "")
	}
	if len(entries) > 256 {
		return nil, rpc.Error(tailnetFailure(domain.ResourceExhausted), "")
	}
	values := []tailscale.Approval{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || len(name) <= 5 || filepath.Ext(name) != ".json" {
			return nil, rpc.Error(tailnetFailure(domain.RecoveryRequired), "")
		}
		v, err := s.readOutgoing(domain.ID(name[:len(name)-5]))
		if err != nil {
			return nil, rpc.Error(err, "")
		}
		if v.Actor == actor {
			values = append(values, outgoingPublic(v))
		}
	}
	return connect.NewResponse(&pb.ListTailscaleConnectionsResponse{DocumentJson: tailnetJSON(values)}), nil
}

func (s *Service) localTailnetActor(actor domain.Principal) bool {
	if actor.Type == domain.OwnerDevice {
		return true
	}
	c, err := worker.LoadCredential(filepath.Join(s.Store.Root(), "desktop-client"))
	return err == nil && actor.Type == domain.ClientDevice && c.DeviceID == actor.DeviceID && c.ServerID == s.Identity.ServerID
}

type tailnetOriginKey struct{}

func (s *Service) CancelTailscaleConnection(ctx context.Context, req *connect.Request[pb.CancelTailscaleConnectionRequest]) (*connect.Response[pb.CancelTailscaleConnectionResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	v, err := s.readOutgoing(domain.ID(req.Msg.RequestId))
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	actor, ok := domain.PrincipalFrom(ctx)
	if !ok || actor != v.Actor || v.Approval == nil {
		return nil, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	if err := s.Store.Read(ctx, func(tx *store.Tx) error { return tx.Authorize() }); err != nil {
		return nil, rpc.Error(err, "")
	}
	if v.Approval.State == tailscale.Denied || v.Approval.State == tailscale.Expired {
		return connect.NewResponse(&pb.CancelTailscaleConnectionResponse{DocumentJson: tailnetJSON(outgoingPublic(v))}), nil
	}
	if v.Approval.State != tailscale.Pending {
		return nil, rpc.Error(tailnetFailure(domain.Conflict), "")
	}
	proof, err := tailscale.Encrypt(v.PrivateKey, v.Approval.TargetKey, v.Approval.Transcript(), []byte("cancel"))
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	httpClient, transport := s.tailnetHTTPClient()
	defer transport.CloseIdleConnections()
	remote := delidevv1connect.NewTailscaleServiceClient(httpClient, v.Origin, connect.WithReadMaxBytes(32<<10), connect.WithSendMaxBytes(32<<10))
	result, err := remote.CancelTailscaleApproval(ctx, connect.NewRequest(&pb.CancelTailscaleApprovalRequest{RequestId: string(v.ID), EncryptedConfirmation: proof}))
	if err != nil {
		return nil, rpc.ClientError(err)
	}
	var approval tailscale.Approval
	if domain.Decode(result.Msg.DocumentJson, &approval) != nil || approval.Validate() != nil || !bytes.Equal(approval.Transcript(), v.Approval.Transcript()) {
		return nil, rpc.Error(tailnetFailure(domain.Unsupported), "")
	}
	v.Approval = &approval
	if err := s.writeOutgoing(v); err != nil {
		return nil, rpc.Error(err, "")
	}
	return connect.NewResponse(&pb.CancelTailscaleConnectionResponse{DocumentJson: tailnetJSON(outgoingPublic(v))}), nil
}
func (s *Service) CancelTailscaleApproval(ctx context.Context, req *connect.Request[pb.CancelTailscaleApprovalRequest]) (*connect.Response[pb.CancelTailscaleApprovalResponse], error) {
	s.tailnetMu.Lock()
	defer s.tailnetMu.Unlock()
	v, err := s.readTailnet(domain.ID(req.Msg.RequestId))
	if err != nil {
		return nil, rpc.Error(err, "")
	}
	plain, err := tailscale.Decrypt(v.PrivateKey, v.Approval.RequesterKey, v.Approval.Transcript(), req.Msg.EncryptedConfirmation)
	if err != nil || string(plain) != "cancel" {
		clear(plain)
		return nil, rpc.Error(tailnetFailure(domain.PermissionDenied), "")
	}
	clear(plain)
	if v.Approval.State == tailscale.Pending {
		v.Approval.State = tailscale.Denied
		if err := s.writeTailnet(v); err != nil {
			return nil, rpc.Error(err, "")
		}
	}
	return connect.NewResponse(&pb.CancelTailscaleApprovalResponse{DocumentJson: tailnetJSON(publicApproval(v, false, s.tailnetClock()))}), nil
}

func (s *Service) tailnetWorkerEndpoint() string {
	if s.tailscale != nil {
		access := s.tailscale.Status()
		if access.State == tailscale.Ready {
			return access.Origin
		}
	}
	return s.Endpoint.URL
}

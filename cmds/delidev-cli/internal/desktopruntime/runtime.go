// SPDX-License-Identifier: Apache-2.0
// Package desktopruntime separates immutable pairing authority from an
// app-owned, ephemeral loopback transport. It never reads owner credentials.
package desktopruntime

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const ProofPath = "/__delidev_desktop_runtime"
const recordName = "desktop-runtime.json"

type Target struct {
	Version    int       `json:"version"`
	Endpoint   string    `json:"endpoint"`
	Generation domain.ID `json:"generation"`
	ServerID   domain.ID `json:"server_id"`
	Key        string    `json:"key"`
	Root       string    `json:"-"`
}

func (t Target) Validate() error {
	u, err := url.Parse(t.Endpoint)
	if err != nil || u == nil {
		return unavailable()
	}
	port, portErr := strconv.ParseUint(u.Port(), 10, 16)
	key, keyErr := base64.RawURLEncoding.DecodeString(t.Key)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || portErr != nil || port == 0 || u.Host != "127.0.0.1:"+strconv.FormatUint(port, 10) || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || t.Version != 2 || t.Generation.Validate() != nil || t.ServerID.Validate() != nil || keyErr != nil || len(key) != 32 || base64.RawURLEncoding.EncodeToString(key) != t.Key {
		return unavailable()
	}
	return nil
}

func unavailable() error {
	return domain.Fail(domain.ServerUnavailable, "The desktop runtime is unavailable.", "Inspect the original desktop connection before retrying.")
}

type contextKey struct{}

func WithTarget(ctx context.Context, t *Target) context.Context {
	return context.WithValue(ctx, contextKey{}, t)
}
func FromContext(ctx context.Context) *Target { t, _ := ctx.Value(contextKey{}).(*Target); return t }

func Publish(t Target) error {
	if err := t.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(t)
	if err != nil {
		return err
	}
	if err := security.WriteAtomic(filepath.Join(t.Root, recordName), raw); err != nil {
		return err
	}
	// An unverifiable Local Worker never blocks the app runtime itself. It
	// simply gains no follow marker and keeps failing closed at its transport.
	if err := MarkLocal(t); err != nil {
		if domain.SafeError(err).Code != domain.RecoveryRequired {
			return err
		}
		logUnverifiedPairing("publish", err)
	}
	return nil
}
func Retire(t Target) error {
	current, err := Load(t.Root, t.ServerID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Generation != t.Generation {
		return nil
	}
	if err := os.Remove(filepath.Join(t.Root, recordName)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
func Load(root string, serverID domain.ID) (Target, error) {
	raw, err := security.ReadPrivate(filepath.Join(root, recordName), 4096)
	if err != nil {
		return Target{}, err
	}
	var t Target
	if domain.Decode(raw, &t) != nil || t.Validate() != nil || t.ServerID != serverID {
		return Target{}, unavailable()
	}
	t.Root = root
	return t, nil
}

// Only the original fixed local Worker with its retained local-pairing proof
// may follow this locator. Saved/remote Workers never gain this authority.
func LocalRoot(root string, serverID domain.ID, pairedEndpoint string) (string, bool, error) {
	if filepath.Base(root) != "worker" {
		return "", false, nil
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "local-pairing.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	var original struct {
		RequestID domain.ID `json:"request_id"`
		ServerID  domain.ID `json:"server_id"`
		Endpoint  string    `json:"endpoint"`
		Name      string    `json:"name,omitempty"`
	}
	if domain.Decode(raw, &original) != nil || original.RequestID.Validate() != nil || original.ServerID != serverID || original.Endpoint != pairedEndpoint {
		return "", false, unavailable()
	}
	owner := filepath.Dir(root)
	// A saved connection's Worker is independently paired, even when local.
	if filepath.Base(filepath.Dir(owner)) == "connections" {
		return "", false, nil
	}
	if err := security.CheckPrivateDir(owner); err != nil {
		return "", false, err
	}
	return owner, true, nil
}

// Following reports whether LocalTransport would route this fixed Local Worker
// through an app-owned runtime now: a current record exists, or the durable
// follow marker proves a retired runtime must fail closed. An unchanged Local
// address (no record and no marker) and every Saved/remote Worker report false.
// Corrupt state reports true so the transport rejects it instead of silently
// using the pairing address.
func Following(root string, serverID domain.ID, pairedEndpoint string) (bool, error) {
	owner, local, err := LocalRoot(root, serverID, pairedEndpoint)
	if err != nil || !local {
		return false, err
	}
	if _, err := Load(owner, serverID); err == nil || !errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if _, err := security.ReadPrivate(filepath.Join(root, "desktop-runtime-follow.json"), 4096); errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return true, nil
}

func proof(t Target, challenge string) string {
	key, _ := base64.RawURLEncoding.DecodeString(t.Key)
	mac := hmac.New(sha256.New, key)
	io.WriteString(mac, "delidev-desktop-v2\n"+string(t.Generation)+"\n"+string(t.ServerID)+"\n"+challenge)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}
func Handler(t Target, next http.Handler, origins []string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != ProofPath {
			verified, err := unwrapBearer(t, r)
			if err != nil {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, verified)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Type", "text/plain")
		origin := r.Header.Get("Origin")
		if origin != "" {
			allowed := false
			for _, candidate := range origins {
				if candidate == origin {
					allowed = true
				}
			}
			if !allowed {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		challenge := r.URL.Query().Get("challenge")
		raw, err := base64.RawURLEncoding.DecodeString(challenge)
		if r.Method != http.MethodGet || len(r.URL.Query()) != 1 || len(r.URL.Query()["challenge"]) != 1 || err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != challenge {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		io.WriteString(w, proof(t, challenge))
	})
}

// Each request proves the current listener before releasing its bearer token.
// No redirect, proxy, stale endpoint fallback or cached proof is accepted.
type Transport struct {
	Base    http.RoundTripper
	Resolve func() (Target, error)
}

func (t *Transport) RoundTrip(r *http.Request) (*http.Response, error) {
	target, err := t.Resolve()
	if err != nil || target.Validate() != nil {
		return nil, unavailable()
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, unavailable()
	}
	challenge := base64.RawURLEncoding.EncodeToString(raw)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	probe, _ := http.NewRequestWithContext(ctx, http.MethodGet, target.Endpoint+ProofPath+"?challenge="+challenge, nil)
	reply, err := t.Base.RoundTrip(probe)
	if err != nil {
		return nil, unavailable()
	}
	body, err := io.ReadAll(io.LimitReader(reply.Body, 65))
	reply.Body.Close()
	if err != nil || reply.StatusCode != http.StatusOK || !hmac.Equal(body, []byte(proof(target, challenge))) {
		return nil, unavailable()
	}
	copy := r.Clone(r.Context())
	u := *r.URL
	u.Scheme = "http"
	u.Host = strings.TrimPrefix(target.Endpoint, "http://")
	copy.URL = &u
	copy.Host = u.Host
	if err := wrapBearer(target, copy); err != nil {
		return nil, err
	}
	return t.Base.RoundTrip(copy)
}

func followMarkerPath(root string) string {
	return filepath.Join(root, "desktop-runtime-follow.json")
}

// FollowVerified persists the follow marker for the fixed Local Worker at root
// only after the retained original pairing grant proves that the saved Worker
// identity (server, endpoint and pairing) is the one the owner granted. The
// proof is file-only and runs before any listener is contacted. An existing
// marker is never trusted on its own: a version-2 marker written before grant
// verification existed carries no proof, so the grant is re-verified every time.
func FollowVerified(owner, root string, serverID domain.ID, endpoint string, pairingID domain.ID) error {
	if serverID.Validate() != nil {
		return unavailable()
	}
	if err := VerifyOriginalPairing(owner, root, serverID, endpoint, pairingID); err != nil {
		return err
	}
	return follow(root, serverID)
}

// Once an explicitly paired Local Worker follows an app runtime, retirement
// must never fall back to its obsolete immutable pairing address.
func follow(root string, serverID domain.ID) error {
	if serverID.Validate() != nil {
		return unavailable()
	}
	path := followMarkerPath(root)
	type marker struct {
		Version  int       `json:"version"`
		ServerID domain.ID `json:"server_id"`
	}
	previous, err := security.ReadPrivate(path, 4096)
	if err == nil {
		var original marker
		if domain.Decode(previous, &original) != nil || original.Version != 2 || original.ServerID != serverID {
			return unavailable()
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, _ := json.Marshal(marker{2, serverID})
	return security.WriteAtomic(path, raw)
}

type LocalTransport struct {
	Base        http.RoundTripper
	Root, Owner string
	ServerID    domain.ID
	// Endpoint and PairingID are the saved Worker's identity. They let the
	// transport prove the retained original grant before it creates a marker.
	Endpoint  string
	PairingID domain.ID
}

func (t *LocalTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	// The retained original grant is proven from files before any branch can
	// contact a listener: the runtime, a marker-refused retirement or the
	// original pairing address. Edited credential and journal endpoints that
	// the unchanged grant does not name never receive the bearer token.
	// Unverifiable evidence keeps its recovery_required code so callers such as
	// the resident Worker stop instead of retrying a transient-looking outage.
	if err := VerifyOriginalPairing(t.Owner, t.Root, t.ServerID, t.Endpoint, t.PairingID); err != nil {
		logUnverifiedPairing("transport", err)
		return nil, err
	}
	target, err := Load(t.Owner, t.ServerID)
	if err == nil {
		if err := follow(t.Root, t.ServerID); err != nil {
			return nil, unavailable()
		}
		return (&Transport{Base: t.Base, Resolve: func() (Target, error) { return target, nil }}).RoundTrip(r)
	}
	if errors.Is(err, os.ErrNotExist) {
		if _, markerErr := security.ReadPrivate(filepath.Join(t.Root, "desktop-runtime-follow.json"), 4096); errors.Is(markerErr, os.ErrNotExist) {
			return t.Base.RoundTrip(r)
		}
	}
	return nil, unavailable()
}

func MarkLocal(t Target) error {
	root := filepath.Join(t.Root, "worker")
	raw, err := security.ReadPrivate(filepath.Join(root, "local-pairing.json"), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return unreadableEvidence("journal", err)
	}
	var original struct {
		RequestID domain.ID `json:"request_id"`
		ServerID  domain.ID `json:"server_id"`
		Endpoint  string    `json:"endpoint"`
		Name      string    `json:"name,omitempty"`
	}
	if domain.Decode(raw, &original) != nil {
		return unreadableEvidence("journal", unavailable())
	}
	if original.ServerID != t.ServerID {
		return nil
	}
	owner, local, err := LocalRoot(root, t.ServerID, original.Endpoint)
	if err != nil {
		return unreadableEvidence("journal", err)
	}
	if !local || owner != t.Root {
		return unavailable()
	}
	exists, err := verifySavedWorker(owner, root, t.ServerID, original.Endpoint)
	if err != nil || !exists {
		// No accepted Worker identity yet means nothing can follow the runtime.
		return err
	}
	return follow(root, t.ServerID)
}

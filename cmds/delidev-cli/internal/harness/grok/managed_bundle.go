// SPDX-License-Identifier: Apache-2.0
package grok

import (
	"bytes"
	"crypto/sha256"
	"path/filepath"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/subscription"
)

// Only the original authenticated process can supply this final bundle. Retain
// immutable identity and digests rather than a second original plaintext copy.
// Capture is once-only, including failure, and must precede native wire closure.
type managedBundleCapture struct {
	mu        sync.Mutex
	identity  subscription.Identity
	digest    [sha256.Size]byte
	access    [sha256.Size]byte
	refresh   [sha256.Size]byte
	created   time.Time
	expires   time.Time
	attempted bool
	taken     bool
	bundle    []byte
	problem   error
}

func newManagedBundleCapture(original []byte) (*managedBundleCapture, error) {
	auth, identity, err := subscription.ParseGrok(original)
	if err != nil {
		return nil, err
	}
	return &managedBundleCapture{identity: identity, digest: sha256.Sum256(original), access: sha256.Sum256([]byte(auth.Key)), refresh: sha256.Sum256([]byte(auth.Refresh)), created: auth.Created, expires: auth.Expires}, nil
}

func (c *managedBundleCapture) capture(read func() ([]byte, error)) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.attempted {
		return c.problem
	}
	c.attempted = true
	c.problem = subscription.InvalidGrok()
	raw, err := read()
	if err != nil {
		clear(raw)
		return c.problem
	}
	auth, identity, err := subscription.ParseGrok(raw)
	if err != nil || !bytes.Equal(subscription.CommitmentInput(c.identity), subscription.CommitmentInput(identity)) || !auth.Expires.After(time.Now().UTC()) {
		clear(raw)
		return c.problem
	}
	if sha256.Sum256(raw) != c.digest && (!auth.Created.After(c.created) || !auth.Expires.After(c.expires) || sha256.Sum256([]byte(auth.Key)) == c.access && sha256.Sum256([]byte(auth.Refresh)) == c.refresh) {
		clear(raw)
		return c.problem
	}
	c.bundle, c.problem = raw, nil
	return nil
}

// take transfers captured bytes to the protected lease coordinator once. It
// never reads a closed native wire or regenerates a failed capture on retry.
func (c *managedBundleCapture) take() ([]byte, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.attempted || c.taken || c.problem != nil || len(c.bundle) == 0 {
		return nil, subscription.InvalidGrok()
	}
	c.taken = true
	value := c.bundle
	c.bundle = nil
	return value, nil
}

func (a *apiConnection) captureManagedBundle() error {
	if a.managedBundle == nil {
		return nil
	}
	return a.managedBundle.capture(func() ([]byte, error) {
		select {
		case <-a.wire.Done():
			return nil, subscription.InvalidGrok()
		default:
		}
		if a.profile.checkInitialized() != nil {
			return nil, subscription.InvalidGrok()
		}
		raw, err := security.ReadPrivate(filepath.Join(filepath.Dir(a.profile.path), "auth.json"), subscription.MaxBundle)
		select {
		case <-a.wire.Done():
			clear(raw)
			return nil, subscription.InvalidGrok()
		default:
		}
		return raw, err
	})
}

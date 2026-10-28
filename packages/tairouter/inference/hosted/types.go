// SPDX-License-Identifier: Apache-2.0
// Package hosted orchestrates remote helper tools through injected service operations.
// It never owns credentials, network clients, local tools, or cross-request caches.
package hosted

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/delinoio/oss/packages/tairouter/inference"
)

type Kind string

const (
	Search Kind = "search"
	Vision Kind = "vision"
	Image  Kind = "image"
	Video  Kind = "video"
)

// OperationRequest contains no model credential. Resolve must consult current owner
// policy, not infer a sidecar destination or account from untrusted tool arguments.
type OperationRequest struct {
	Principal   inference.Principal
	RequestID   string
	CallID      string
	Kind        Kind
	Declaration json.RawMessage
	Arguments   json.RawMessage
}

// Resolver selects a user-owned helper. Resolution must not replenish the physical
// send budget in ctx. Neither resolution nor execution may detach from cancellation.
type Resolver interface {
	Resolve(context.Context, OperationRequest) (OperationExecutor, error)
}

// OperationExecutor owns routing, credential selection, current dispatch authorization,
// usage recording (including cancellation/unknown usage), and artifact publication.
// EVERY physical send, poll, and download must inherit inference.PhysicalSend via
// the service's guarded transport. It must not create a fresh inference admission.
// Result artifacts must already be owner-fenced R2 artifacts, never local paths.
type OperationExecutor interface {
	Execute(context.Context, OperationRequest) (OperationResult, error)
}

type Source struct {
	URL   string `json:"url"`
	Title string `json:"title,omitempty"`
}
type Artifact struct {
	ID          string `json:"id"`
	URL         string `json:"url"`
	ContentType string `json:"content_type"`
}
type OperationResult struct {
	Output    string          `json:"output"`
	Sources   []Source        `json:"sources,omitempty"`
	Artifacts []Artifact      `json:"artifacts,omitempty"`
	Usage     inference.Usage `json:"usage"`
	// Failed is a completed, sanitized tool failure; authorization, cancellation,
	// budget exhaustion and ambiguous sends must instead return an error.
	Failed bool `json:"failed,omitempty"`
}

// Codec adapts the core's lossless standard-wire translator. Its Chat response
// representation must retain reasoning, signatures and provider tool metadata.
// Collect closes Body, bounds decoded bytes, and rejects incomplete/error streams.
type Codec interface {
	Request(json.RawMessage, inference.Wire, inference.Wire) (json.RawMessage, error)
	Collect(context.Context, *http.Response, inference.Wire, int64) (json.RawMessage, error)
}

type Config struct {
	// RecoveryPolicy supplies separately registered provider classifiers. It only
	// inspects successful headers; the core still owns refresh/rotation/retry.
	RecoveryPolicy inference.HTTPRecoveryPolicy
	// NativeTools is resolved by trusted provider/model policy. These declarations
	// stay native, even when another helper capability activates this wrapper.
	NativeTools map[Kind]bool
	// EnabledTools nil permits declared helpers. A non-nil map restricts helpers
	// to configured kinds; an empty map preserves every native declaration.
	EnabledTools map[Kind]bool

	Resolver        Resolver
	Codec           Codec
	MaxRounds       int
	MaxSearches     int
	MaxImages       int
	MaxVideos       int
	MaxDescriptions int
	MaxBytes        int64
	// EnableVideo and DescribeImages are trusted per-route capabilities, never
	// inferred from a client's function name or unsupported model name.
	EnableVideo    bool
	DescribeImages bool
	SearchTimeout  time.Duration
	VisionTimeout  time.Duration
	MediaTimeout   time.Duration
}

func defaults(c Config) Config {
	c.MaxRounds = bound(c.MaxRounds, 3, 10)
	c.MaxSearches = bound(c.MaxSearches, 3, 32)
	c.MaxImages = bound(c.MaxImages, 10, 10)
	c.MaxVideos = bound(c.MaxVideos, 3, 3)
	c.MaxDescriptions = bound(c.MaxDescriptions, 8, 16)
	if c.MaxBytes <= 0 || c.MaxBytes > 32<<20 {
		c.MaxBytes = 32 << 20
	}
	if c.SearchTimeout <= 0 || c.SearchTimeout > 600*time.Second {
		c.SearchTimeout = 60 * time.Second
	}
	if c.VisionTimeout <= 0 || c.VisionTimeout > 2147483647*time.Millisecond {
		c.VisionTimeout = 45 * time.Second
	}
	if c.MediaTimeout <= 0 || c.MediaTimeout > 300*time.Second {
		c.MediaTimeout = 300 * time.Second
	}
	return c
}
func bound(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
func fault(status int, code string) error { return &inference.Error{Status: status, Code: code} }

// SPDX-License-Identifier: Apache-2.0
package kiro

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"regexp"

	"github.com/delinoio/oss/packages/aiprouter/inference"
)

type ToolKind string

const (
	FunctionTool     ToolKind = "function"
	CustomTool       ToolKind = "custom"
	SearchTool       ToolKind = "tool_search"
	HostedSearchTool ToolKind = "web_search"
	HostedImageTool  ToolKind = "image_generation"
)

// ToolDefinition lets the central bridge restore custom and hosted calls. A
// declaration is not permission for the adapter to execute a tool.
type ToolDefinition struct {
	WireName      string
	Name          string
	Namespace     string
	Kind          ToolKind
	Configuration json.RawMessage
}
type Metering struct {
	Unit, UnitPlural string
	Usage            float64
}

// Metadata is a snapshot of provider measurements. Missing measurements remain
// nil. Persistence and owner/deletion fences remain the caller's responsibility.
type Metadata struct {
	ConversationID         string
	ContextUsagePercentage *float64
	Metering               []Metering
	Tools                  []ToolDefinition
	Usage                  inference.Usage
	Terminal               bool
	FailureCode            string
	ImagePositions         map[string]int
}

var conversationPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,256}$`)

type continuationKey struct{}
type continuationSeed struct {
	owner inference.Principal
	id    string
}

// WithContinuation accepts only a conversation loaded by the host for owner.
// Build independently checks that the authenticated principal still matches.
func WithContinuation(ctx context.Context, owner inference.Principal, id string) (context.Context, error) {
	if !conversationPattern.MatchString(id) {
		return nil, failure(400, "kiro_invalid_conversation_id")
	}
	return context.WithValue(ctx, continuationKey{}, continuationSeed{owner, id}), nil
}

// MetadataFor never returns state for a different owner or membership generation.
// Call after response consumption to obtain terminal usage and metering.
func MetadataFor(ctx context.Context, owner inference.Principal) (Metadata, bool) {
	s, _ := ctx.Value(stateKey{}).(*requestState)
	if s == nil {
		return Metadata{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.built || s.owner != owner {
		return Metadata{}, false
	}
	m := s.metadata
	m.ImagePositions = map[string]int{}
	for key, position := range s.metadata.ImagePositions {
		m.ImagePositions[key] = position
	}
	if m.ContextUsagePercentage != nil {
		n := *m.ContextUsagePercentage
		m.ContextUsagePercentage = &n
	}
	m.Metering = append([]Metering(nil), m.Metering...)
	m.Tools = append([]ToolDefinition(nil), m.Tools...)
	for i := range m.Tools {
		m.Tools[i].Configuration = append(json.RawMessage(nil), m.Tools[i].Configuration...)
	}
	clone := func(p *int64) *int64 {
		if p == nil {
			return nil
		}
		n := *p
		return &n
	}
	m.Usage = inference.Usage{InputTokens: clone(m.Usage.InputTokens), OutputTokens: clone(m.Usage.OutputTokens), CacheReadTokens: clone(m.Usage.CacheReadTokens), CacheWriteTokens: clone(m.Usage.CacheWriteTokens), CacheWrite1hTokens: clone(m.Usage.CacheWrite1hTokens)}
	return m, true
}

type imagePositionsKey struct{}
type imagePositionSeed struct {
	owner     inference.Principal
	positions map[string]int
}

// WithImagePositions restores owner-scoped emitted tiers. It never accepts raw
// images or stores process-wide image identities across tenants.
func WithImagePositions(ctx context.Context, owner inference.Principal, positions map[string]int) (context.Context, error) {
	if len(positions) > 4096 {
		return nil, failure(400, "kiro_image_position_limit")
	}
	copy := map[string]int{}
	for key, pos := range positions {
		if _, err := hex.DecodeString(key); err != nil {
			return nil, failure(400, "kiro_invalid_image_position")
		}
		if len(key) != 64 || pos < 0 || pos > 5 {
			return nil, failure(400, "kiro_invalid_image_position")
		}
		copy[key] = pos
	}
	return context.WithValue(ctx, imagePositionsKey{}, imagePositionSeed{owner, copy}), nil
}

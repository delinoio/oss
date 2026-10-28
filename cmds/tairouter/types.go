// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"time"
)

const DefaultPort = 17843
const PollInterval = 5 * time.Second
const MaxConfigBytes = 4 << 20

var ErrConflict = errors.New("settings changed; preserve the recovery backup and restore manually")
var ErrUnavailable = errors.New("OS credential store unavailable; no plaintext fallback is permitted")
var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,47}$`)

type Profile struct {
	Name              string `json:"name"`
	Server            string `json:"server"`
	Organization      string `json:"organization"`
	KeyID             string `json:"keyId"`
	Port              int    `json:"port"`
	RevocationPending bool   `json:"revocationPending,omitempty"`
}

func (p Profile) Validate() error {
	if !namePattern.MatchString(p.Name) {
		return errors.New("profile name must start with a lowercase letter and contain at most 48 lowercase letters, digits or hyphens")
	}
	if p.Port < 1024 || p.Port > 65535 {
		return errors.New("port must be fixed and between 1024 and 65535")
	}
	_, err := serverURL(p.Server)
	return err
}
func serverURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("server must be an HTTPS origin without credentials, path, query or fragment")
	}
	if u.Scheme != "https" {
		return nil, errors.New("server must use HTTPS")
	}
	return u, nil
}

type DeviceStatus string

const (
	DevicePending  DeviceStatus = "pending"
	DeviceApproved DeviceStatus = "approved"
	DeviceDenied   DeviceStatus = "denied"
	DeviceExpired  DeviceStatus = "expired"
)

type Device struct {
	ID, Secret, Code, URL string
	Expires               time.Time
}
type Grant struct {
	Status                   DeviceStatus
	KeyID, Key, Organization string
}
type ClaudeDesktopModel struct {
	Name                string `json:"name"`
	LabelOverride       string `json:"labelOverride,omitempty"`
	AnthropicFamilyTier string `json:"anthropicFamilyTier,omitempty"`
	IsFamilyDefault     *bool  `json:"isFamilyDefault,omitempty"`
	Supports1M          *bool  `json:"supports1m,omitempty"`
	Prefer1M            *bool  `json:"prefer1m,omitempty"`
}
type ClientReasoningInterleaving string

const (
	ClientInterleavingNone    ClientReasoningInterleaving = "none"
	ClientInterleavingContent ClientReasoningInterleaving = "reasoning_content"
)

type ClientModel struct {
	FastRowAvailable           *bool
	DefaultReasoningEffort     *string
	SupportsReasoningEffort    *bool
	SupportsThinkingToggle     *bool
	DefaultThinkingEnabled     *bool
	SupportsReasoningSummaries *bool
	ReasoningInterleaving      *ClientReasoningInterleaving
	MaxInputTokens             *uint64
	SupportsReasoning          *bool
	SupportsTools              *bool
	ReasoningEffortsKnown      *bool
	InputModalities            []string
	Desktop                    *ClaudeDesktopModel
	CodexCatalog               map[string]any
	ID, Name                   string
	Description                string
	Context, Output            int
	Tools, Vision              bool
	Efforts                    []string
}
type ClientAPI interface {
	Begin(context.Context, string) (Device, error)
	Poll(context.Context, Device) (Grant, error)
	Confirm(context.Context, Device) error
	Revoke(context.Context, string, string) error
	Models(context.Context, string) ([]ClientModel, error)
}
type Credentials interface {
	Set(string, string) error
	Get(string) (string, error)
	Delete(string) error
}
type Services interface {
	Install(context.Context, Profile, string, string) error
	Status(context.Context, Profile) (string, error)
	Stop(context.Context, Profile) error
	Remove(context.Context, Profile) error
}
type Prompter interface{ Confirm(string) bool }
type Clock interface {
	Now() time.Time
	Wait(context.Context, time.Duration) error
}
type RealClock struct{}

func (RealClock) Now() time.Time { return time.Now() }
func (RealClock) Wait(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func credentialID(p Profile) string {
	return "profile/" + digest([]byte(p.Server))[:16] + "/" + p.Name + "/" + p.KeyID
}
func revokeHint(p Profile) string {
	return fmt.Sprintf("Revoke CLI key %s in %s (API keys for organization %s).", p.KeyID, p.Server, p.Organization)
}

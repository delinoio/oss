// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"time"
)

const DefaultNativePort = 17844

type NativeRuntimeRequest struct {
	Profile Profile
	Mode    NativeCompatibilityMode
	Build   string
	Port    int
	Trial   bool
}
type NativeRuntimeConsent struct {
	Trust, Application, AccountTrial bool
	PlanFingerprint                  string
	PlanDigest                       string
}
type NativeRuntimePlan struct {
	PlanDigest                                               string
	Profile                                                  string
	Mode                                                     NativeCompatibilityMode
	Build, Address, CAPath, Fingerprint                      string
	Hosts, Routes, Listeners, TrustChanges, SettingsChanges  []string
	RequiresTrust, RequiresApplication, RequiresAccountTrial bool
}
type NativeRuntimeStatus struct {
	Profile                             string
	Mode                                NativeCompatibilityMode
	Build, Address, CAPath, Fingerprint string
	// ProxyURL is deliberately always empty: authenticated URLs belong only in Ready.
	ProxyURL                string `json:"-"`
	Enabled, Ready, Trusted bool
	Consent                 NativeRuntimeConsent
	TrialUntil              time.Time
}

// NativeRuntimeReady is a short-lived capability. Do not log it or its ProxyURL.
type NativeRuntimeReady struct {
	Profile       string
	Mode          NativeCompatibilityMode
	Build, CAPath string
	ProxyURL      string `json:"-"`
	proof         *nativeReadyProof
}
type nativeReadyProof struct {
	profile                 string
	mode                    NativeCompatibilityMode
	build, proxyURL, caPath string
	expires                 time.Time
	check                   func() bool
}

func (r NativeRuntimeReady) Validate() error {
	p := r.proof
	if p == nil || !time.Now().Before(p.expires) || r.Profile != p.profile || r.Mode != p.mode || r.Build != p.build || r.ProxyURL != p.proxyURL || r.CAPath != p.caPath {
		return errors.New("native runtime readiness is missing, altered, or expired")
	}
	if p.check != nil && (!p.check() || !time.Now().Before(p.expires)) {
		return errors.New("native runtime readiness was revoked")
	}
	return nil
}

// String prevents accidental formatting from exposing proxy authentication.
func (r NativeRuntimeReady) String() string   { return "native runtime readiness (redacted)" }
func (r NativeRuntimeReady) GoString() string { return r.String() }

type NativeRuntimeController interface {
	Plan(context.Context, NativeRuntimeRequest) (NativeRuntimePlan, error)
	Enable(context.Context, NativeRuntimeRequest, NativeRuntimeConsent) (NativeRuntimeStatus, error)
	Status(context.Context, string) (NativeRuntimeStatus, error)
	Ready(context.Context, string) (NativeRuntimeReady, error)
	Stop(context.Context, string) error
	Disable(context.Context, string) error
	Serve(context.Context, string) error
	Launch(context.Context, string) error
}

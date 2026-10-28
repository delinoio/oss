// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

func (a *Application) nativeController() NativeRuntimeController {
	if a.Native == nil {
		a.Native = NewNativeRuntimeController(a.Disk, a.Store)
	}
	return a.Native
}
func (a *Application) runNative(ctx context.Context, s *State, name, action string, mode NativeCompatibilityMode, build string, port int) error {
	runtime := a.nativeController()
	switch action {
	case "status":
		status, e := runtime.Status(ctx, name)
		if e != nil {
			return e
		}
		fmt.Fprintf(a.Out, "Native mode=%s build=%s enabled=%t trusted=%t ready=%t listener=%s certificate=%s\n", status.Mode, status.Build, status.Enabled, status.Trusted, status.Ready, status.Address, status.Fingerprint)
		return nil
	case "stop":
		return runtime.Stop(ctx, name)
	case "launch":
		ready, e := runtime.Ready(ctx, name)
		if e != nil {
			return e
		}
		if e = ready.Validate(); e != nil {
			return e
		}
		if ready.Mode != NativeCompatibilityCodexObserve && ready.Mode != NativeCompatibilityCodexAccountUITrial {
			return errors.New("native launch requires a Codex Desktop runtime mode")
		}
		if !a.Prompt.Confirm("Launch the verified Codex Desktop package with this owned, temporary proxy configuration?") {
			return errors.New("native application launch declined")
		}
		return runtime.Launch(ctx, name)
	case "disable":
		if !a.Prompt.Confirm("Stop the owned native listener, restore its application settings, and remove its owned trust certificate?") {
			return errors.New("native disable declined")
		}
		if e := runtime.Stop(ctx, name); e != nil {
			return e
		}
		var failures []string
		for client, record := range s.Records {
			if record.Profile == name && record.RuntimeMode != "" {
				if e := a.Disk.Restore(s, client); e != nil {
					failures = append(failures, "settings restoration incomplete for "+client)
				}
			}
		}
		if e := runtime.Disable(ctx, name); e != nil {
			failures = append(failures, "owned trust removal incomplete")
		}
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "; "))
		}
		return nil
	case "plan", "enable":
		profile, ok := s.Profiles[name]
		if !ok || profile.RevocationPending {
			return errors.New("profile is unavailable")
		}
		if port == profile.Port {
			return errors.New("native listener port conflicts with relay")
		}
		for _, other := range s.Profiles {
			if port == other.Port {
				return errors.New("native listener port conflicts with a profile relay")
			}
		}
		request := NativeRuntimeRequest{Profile: profile, Mode: mode, Build: build, Port: port, Trial: mode == NativeCompatibilityCodexAccountUITrial}
		plan, e := runtime.Plan(ctx, request)
		if e != nil {
			return e
		}
		fmt.Fprintf(a.Out, "Native mode=%s build=%s listener=%s\nCertificate: %s\nPublic CA: %s\nHosts: %q\nRoutes: %q\nListeners: %q\nTrust changes: %q\nApplication changes: %q\n", plan.Mode, plan.Build, plan.Address, plan.Fingerprint, plan.CAPath, plan.Hosts, plan.Routes, plan.Listeners, plan.TrustChanges, plan.SettingsChanges)
		if action == "plan" {
			return nil
		}
		consent := NativeRuntimeConsent{PlanFingerprint: plan.Fingerprint, PlanDigest: plan.PlanDigest}
		if plan.RequiresTrust {
			consent.Trust = a.Prompt.Confirm("Trust this exact owned certificate in your user trust store for the displayed destinations?")
			if !consent.Trust {
				return errors.New("native trust consent declined")
			}
		}
		if plan.RequiresApplication {
			consent.Application = a.Prompt.Confirm("Authorize the displayed native application changes for this exact mode and build?")
			if !consent.Application {
				return errors.New("native application consent declined")
			}
		}
		if plan.RequiresAccountTrial {
			consent.AccountTrial = a.Prompt.Confirm("Authorize the bounded account-UI trial? Its display does not establish billing credit or provider isolation.")
			if !consent.AccountTrial {
				return errors.New("native account trial consent declined")
			}
		}
		status, e := runtime.Enable(ctx, request, consent)
		if e != nil {
			return e
		}
		if !status.Trusted {
			return errors.New("native activation pending: owned certificate trust is not established")
		}
		if !status.Ready {
			fmt.Fprintln(a.Out, "Native consent recorded. Run native --action serve, then configure with the selected --mode; application settings require a ready listener.")
		}
		return nil
	default:
		return errors.New("invalid native action")
	}
}
func (a *Application) planConfiguration(ctx context.Context, s State, client string, p Profile, key string, models []ClientModel, mode NativeCompatibilityMode) ([]Change, error) {
	if mode == "" {
		if record, ok := s.Records[client]; ok && record.RuntimeMode != "" {
			return nil, errors.New("restore the native application settings before switching to gateway mode")
		}
		return PlanSettings(a.Resolver, s, client, p, key, models)
	}
	ready, e := a.nativeController().Ready(ctx, p.Name)
	if e != nil {
		return nil, e
	}
	if ready.Mode != mode || ready.Profile != p.Name {
		return nil, errors.New("native runtime mode or profile does not match")
	}
	if e = ready.Validate(); e != nil {
		return nil, e
	}
	var changes []Change
	switch mode {
	case NativeCompatibilityClaudeFirstParty, NativeCompatibilityClaudePicker:
		expected := "claude"
		if mode == NativeCompatibilityClaudePicker {
			expected = "claude-desktop"
		}
		if client != expected {
			return nil, errors.New("native mode does not match selected client")
		}
		changes, e = PlanClaudeCompatibility(a.Resolver, s, p, key, models, ready)
	case NativeCompatibilityCodexObserve, NativeCompatibilityCodexAccountUITrial:
		return nil, errors.New("Codex Desktop compatibility uses native --action launch after enable and serve; ordinary Codex configuration remains separate")
	default:
		return nil, errors.New("unknown native application mode")
	}
	if e != nil {
		return nil, e
	}
	for i := range changes {
		changes[i].RuntimeMode = mode
	}
	return changes, nil
}

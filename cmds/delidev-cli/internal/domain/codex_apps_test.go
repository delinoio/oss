// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"fmt"
	"testing"
)

func TestCodexAppConfigurationPreservesOriginalAuthority(t *testing.T) {
	original := CodexAppConfiguration{Version: 1, SessionID: NewID(), AccountID: NewID(), Generation: NewID(), AppIDs: []string{"original-a", "original-b"}}
	if original.Validate() != nil {
		t.Fatal("valid original selection rejected")
	}
	next := original.Clone()
	next.Generation = NewID()
	next.AppIDs = []string{"original-b"}
	if !original.RemovalOnly(next) {
		t.Fatal("original removal rejected")
	}
	for name, mutate := range map[string]func(*CodexAppConfiguration){
		"account":    func(c *CodexAppConfiguration) { c.AccountID = NewID() },
		"session":    func(c *CodexAppConfiguration) { c.SessionID = NewID() },
		"generation": func(c *CodexAppConfiguration) { c.Generation = original.Generation },
		"addition":   func(c *CodexAppConfiguration) { c.AppIDs = append(c.AppIDs, "foreign") },
		"duplicate":  func(c *CodexAppConfiguration) { c.AppIDs = append(c.AppIDs, c.AppIDs[0]) },
		"missing":    func(c *CodexAppConfiguration) { c.AppIDs = nil },
	} {
		t.Run(name, func(t *testing.T) {
			changed := next.Clone()
			mutate(&changed)
			if original.RemovalOnly(changed) {
				t.Fatal("foreign or increased live authority accepted")
			}
		})
	}
	next.AppIDs[0] = "changed"
	if original.AppIDs[1] != "original-b" {
		t.Fatal("returned selection mutated original generation")
	}
}

func TestCodexAppSelectionBoundAndDistinctAvailability(t *testing.T) {
	c := CodexAppConfiguration{Version: 1, SessionID: NewID(), AccountID: NewID(), Generation: NewID(), AppIDs: []string{}}
	for n := 0; n < MaxCodexAppSelections; n++ {
		c.AppIDs = append(c.AppIDs, fmt.Sprintf("app-%d", n))
	}
	if c.Validate() != nil {
		t.Fatal("full native read-bound selection rejected")
	}
	c.AppIDs = append(c.AppIDs, "overflow")
	if c.Validate() == nil {
		t.Fatal("selection beyond native read bound accepted")
	}
	a := CodexApp{ID: "available", Name: "Available app", Discovered: true, Accessible: true, Selected: true}
	if a.Validate() != nil {
		t.Fatal("available but not callable app rejected")
	}
	a.Callable = true
	if a.Validate() == nil {
		t.Fatal("discovery or selection granted callability")
	}
	a.Installed = true
	a.Enabled = true
	if a.Validate() != nil {
		t.Fatal("original installed/enabled callable state rejected")
	}
}

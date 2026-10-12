// SPDX-License-Identifier: Apache-2.0
package tailscale

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestObservedOwnershipAndHTTPSPrerequisites(t *testing.T) {
	raw := []byte(`{"Version":"1.94.0","BackendState":"Running","CertDomains":["self.example.ts.net"],"Self":{"ID":"self","HostName":"Self","DNSName":"self.example.ts.net.","UserID":7,"Online":true,"CapMap":{"https":[]}},"Peer":{"own":{"ID":"own","HostName":"Own","DNSName":"own.example.ts.net.","UserID":7,"Online":true},"shared":{"ID":"shared","HostName":"Shared","DNSName":"shared.example.ts.net.","UserID":7,"AltSharerUserID":9,"Online":true},"tagged":{"ID":"tagged","HostName":"Tagged","DNSName":"tagged.example.ts.net.","UserID":7,"Tags":["tag:build"],"Online":false},"shared-to":{"ID":"shared-to","HostName":"Shared to","DNSName":"sharee.example.ts.net.","UserID":7,"ShareeNode":true,"Online":true},"unknown":{"ID":"unknown","HostName":"Unknown","DNSName":"unknown.example.ts.net.","Online":true}}}`)
	inventory, err := ParseStatus(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !inventory.HTTPS || inventory.Self.Host != "self.example.ts.net" {
		t.Fatal(inventory)
	}
	expected := map[string]Ownership{"own": Own, "shared": Shared, "tagged": Tagged, "shared-to": UnknownOwnership, "unknown": UnknownOwnership}
	for _, peer := range inventory.Peers {
		if peer.Ownership != expected[peer.ID] {
			t.Fatal(peer)
		}
	}
	for _, changed := range []string{strings.Replace(string(raw), `"CapMap":{"https":[]}`, `"CapMap":{}`, 1), strings.Replace(string(raw), `"CertDomains":["self.example.ts.net"]`, `"CertDomains":["other.example.ts.net"]`, 1)} {
		got, err := ParseStatus([]byte(changed))
		if err != nil || got.HTTPS {
			t.Fatal("missing exact HTTPS admission accepted", err)
		}
	}
}
func TestUnsupportedStatusNeverProducesPartialInventory(t *testing.T) {
	for _, raw := range []string{`{}`, `{"Version":"1","BackendState":"Running","Self":{}}`, `{"Version":"1","BackendState":"NewState"}`, `{"Version":"1","BackendState":"Running","Self":{"ID":"self","HostName":"Self","DNSName":"self.example.ts.net.","Online":"true"},"Peer":{}}`} {
		if _, err := ParseStatus([]byte(raw)); err == nil {
			t.Fatal(raw)
		}
	}
	for _, state := range []string{"NeedsLogin", "NeedsMachineAuth", "Stopped", "Starting", "NoState"} {
		raw, _ := json.Marshal(map[string]string{"Version": "1", "BackendState": state})
		if _, err := ParseStatus(raw); err != nil {
			t.Fatal(state, err)
		}
	}
	if _, err := ParseStatus(make([]byte, 1<<20+1)); err == nil {
		t.Fatal("oversized inventory accepted")
	}
}
func TestForegroundAndForeignConfigurationAdmission(t *testing.T) {
	for _, raw := range []string{`{}`, `null`, `{"TCP":{},"Web":{},"Foreground":{},"Services":{}}`} {
		if !EmptyServe([]byte(raw)) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{`{"TCP":{"8443":{"HTTPS":true}}}`, `{"Foreground":{"foreign":{}}}`, `{"AllowFunnel":{"self.example.ts.net:8443":true}}`, `{"Services":{"svc:foreign":{}}}`, `{"future":true}`, `[]`} {
		if EmptyServe([]byte(raw)) {
			t.Fatal(raw)
		}
	}
	target := "http://127.0.0.1:12345"
	raw := []byte(`{"Foreground":{"original-session":{"TCP":{"8443":{"HTTPS":true}},"Web":{"self.example.ts.net:8443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:12345"}}}}}}}`)
	if ForegroundSession(raw, target, "self.example.ts.net:8443") != "original-session" {
		t.Fatal("foreground not recognized")
	}
	for _, changed := range []string{strings.Replace(string(raw), "12345", "12346", 1), strings.Replace(string(raw), "self.example.ts.net", "foreign.example.ts.net", 1), strings.Replace(string(raw), `"HTTPS":true`, `"HTTPS":true,"TCPForward":"127.0.0.1:1"`, 1), strings.Replace(string(raw), `"Foreground":{`, `"Services":{"svc:foreign":{}},"Foreground":{`, 1), strings.Replace(string(raw), `"HTTPS":true`, `"HTTPS":false`, 1), strings.Replace(string(raw), `"Foreground":{`, `"TCP":{"443":{"HTTPS":true}},"Foreground":{`, 1)} {
		if ForegroundSession([]byte(changed), target, "self.example.ts.net:8443") != "" {
			t.Fatal(changed)
		}
	}
}

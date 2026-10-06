// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net"
	"net/netip"
	"strconv"
	"strings"
)

type ProxyMode string

const (
	ProxyDirect ProxyMode = "direct"
	ProxyHTTP   ProxyMode = "http"
	ProxyHTTPS  ProxyMode = "https"
	ProxySOCKS5 ProxyMode = "socks5"
)

// Hosts are exact ASCII DNS names or canonical IP literals. CIDRs match only
// literal destination IPs: DNS answers must never silently acquire bypass.
type ProxyBypass struct {
	Host string `json:"host"`
	Port uint16 `json:"port,omitempty"`
}
type ProxyDefinition struct {
	Name   string        `json:"name"`
	Mode   ProxyMode     `json:"mode"`
	Host   string        `json:"host,omitempty"`
	Port   uint16        `json:"port,omitempty"`
	Bypass []ProxyBypass `json:"bypass,omitempty"`
}
type NetworkProfile struct {
	ProxyDefinition
	CredentialGeneration ID `json:"credential_generation,omitempty"`
}

// A selection freezes one revision, including its protected credential
// reference. Editing a profile never changes an already selected generation.
type NetworkRoute struct {
	Binding         *WorkerNetworkBinding `json:"binding,omitempty"`
	MachineID       ID                    `json:"machine_id,omitempty"`
	ProfileID       ID                    `json:"profile_id,omitempty"`
	ProfileRevision uint64                `json:"profile_revision,omitempty"`
	Profile         NetworkProfile        `json:"profile"`
}
type ProxyCredential struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func proxyInvalid() error {
	return Fail(InvalidArgument, "Invalid outbound proxy configuration.", "Use direct, http, https or socks5 with an exact host and nonzero port; bypass accepts at most 128 exact hosts, IPs or canonical CIDRs and optional ports.")
}
func ProxyHost(host string) bool {
	if ip, err := netip.ParseAddr(host); err == nil {
		return !ip.Is4In6() && ip.Zone() == "" && ip.String() == host
	}
	if host == "" || len(host) > 253 || host != strings.ToLower(host) {
		return false
	}
	// Reject alternate numeric IPv4 spellings rather than treating them as DNS.
	if strings.Trim(host, "0123456789.") == "" {
		return false
	}
	allNumeric := true
	for _, label := range strings.Split(host, ".") {
		if _, err := strconv.ParseUint(label, 0, 32); err != nil {
			allNumeric = false
		}
	}
	if allNumeric {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}
func (p ProxyDefinition) Validate() error {
	if Text(p.Name, "proxy profile name", 128, true) != nil || len(p.Bypass) > 128 {
		return proxyInvalid()
	}
	switch p.Mode {
	case ProxyDirect:
		if p.Host != "" || p.Port != 0 || len(p.Bypass) != 0 {
			return proxyInvalid()
		}
	case ProxyHTTP, ProxyHTTPS, ProxySOCKS5:
		if !ProxyHost(p.Host) || p.Port == 0 {
			return proxyInvalid()
		}
	default:
		return proxyInvalid()
	}
	seen := map[ProxyBypass]bool{}
	for _, b := range p.Bypass {
		prefix, err := netip.ParsePrefix(b.Host)
		if !ProxyHost(b.Host) && (err != nil || prefix.Addr().Is4In6() || prefix.Masked().String() != b.Host) {
			return proxyInvalid()
		}
		if seen[b] {
			return proxyInvalid()
		}
		seen[b] = true
	}
	return nil
}
func (p NetworkProfile) Validate() error {
	if err := p.ProxyDefinition.Validate(); err != nil {
		return err
	}
	if p.CredentialGeneration != "" && (p.Mode == ProxyDirect || p.CredentialGeneration.Validate() != nil) {
		return proxyInvalid()
	}
	return nil
}
func (r NetworkRoute) Validate() error {
	if r.Binding != nil && (r.MachineID == "" || r.Binding.Validate() != nil) {
		return proxyInvalid()
	}
	if r.MachineID != "" && r.MachineID.Validate() != nil {
		return proxyInvalid()
	}
	if r.ProfileID == "" {
		if r.ProfileRevision != 0 || r.Profile.Mode != ProxyDirect || r.Profile.CredentialGeneration != "" {
			return proxyInvalid()
		}
	} else if r.ProfileID.Validate() != nil || r.ProfileRevision == 0 {
		return proxyInvalid()
	}
	return r.Profile.Validate()
}
func (c ProxyCredential) Validate() error {
	// RFC 1929 uses one-byte lengths. Apply the same bounded visible-ASCII
	// credential shape to Basic authentication; usernames cannot contain ':'.
	if len(c.Username) < 1 || len(c.Username) > 255 || len(c.Password) < 1 || len(c.Password) > 255 || strings.Contains(c.Username, ":") {
		return proxyInvalid()
	}
	for _, s := range []string{c.Username, c.Password} {
		for _, c := range s {
			if c < 33 || c > 126 {
				return proxyInvalid()
			}
		}
	}
	return nil
}
func (p ProxyDefinition) Bypasses(address string) bool {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return false
	}
	host = strings.ToLower(host)
	ip, _ := netip.ParseAddr(host)
	for _, b := range p.Bypass {
		if b.Port != 0 && uint64(b.Port) != n {
			continue
		}
		if host == b.Host {
			return true
		}
		if prefix, err := netip.ParsePrefix(b.Host); err == nil && ip.IsValid() && prefix.Contains(ip) {
			return true
		}
	}
	return false
}

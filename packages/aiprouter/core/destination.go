// SPDX-License-Identifier: Apache-2.0
package core

import (
	"errors"
	"net"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

type DestinationRule struct {
	ID           string   `json:"id,omitempty"`
	Origin       string   `json:"origin"`
	AllowedCIDRs []string `json:"allowed_cidrs,omitempty"`
}

var ErrDestinationRule = errors.New("invalid destination rule")

func NormalizeDestinationRule(r DestinationRule) (DestinationRule, error) {
	u, e := url.Parse(r.Origin)
	if e != nil || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || (u.Scheme != "https" && u.Scheme != "http") || (u.Path != "" && u.Path != "/") || strings.Contains(u.Host, "%") || strings.HasSuffix(u.Hostname(), ".") {
		return r, ErrDestinationRule
	}
	port := u.Port()
	if port == "" {
		if u.Scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return r, ErrDestinationRule
	}
	r.Origin = u.Scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), strconv.Itoa(n))
	cidrs := make([]string, 0, len(r.AllowedCIDRs))
	seen := map[string]bool{}
	for _, raw := range r.AllowedCIDRs {
		p, e := netip.ParsePrefix(raw)
		if e != nil {
			return r, ErrDestinationRule
		}
		if p.Addr().Is4In6() {
			if p.Bits() < 96 {
				return r, ErrDestinationRule
			}
			p = netip.PrefixFrom(p.Addr().Unmap(), p.Bits()-96)
		}
		v := p.Masked().String()
		if !seen[v] {
			cidrs = append(cidrs, v)
			seen[v] = true
		}
	}
	r.AllowedCIDRs = cidrs
	return r, nil
}
func NormalizeDestinationRules(rules []DestinationRule) ([]DestinationRule, error) {
	out := make([]DestinationRule, 0, len(rules))
	seen := map[string]bool{}
	for _, r := range rules {
		v, e := NormalizeDestinationRule(r)
		if e != nil {
			return nil, e
		}
		if seen[v.Origin] {
			return nil, ErrDestinationRule
		}
		seen[v.Origin] = true
		out = append(out, v)
	}
	return out, nil
}

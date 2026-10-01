// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestNetworkValidationAndExactBypass(t *testing.T) {
	p := ProxyDefinition{Name: "Corporate", Mode: ProxyHTTP, Host: "proxy.example", Port: 3128, Bypass: []ProxyBypass{{Host: "example.com"}, {Host: "127.0.0.0/8", Port: 8080}, {Host: "::1"}}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for address, want := range map[string]bool{"example.com:443": true, "sub.example.com:443": false, "notexample.com:443": false, "127.0.0.1:8080": true, "127.0.0.1:443": false, "[::1]:80": true, "localhost:8080": false} {
		if got := p.Bypasses(address); got != want {
			t.Errorf("%s: %v", address, got)
		}
	}
	for _, bad := range []string{"*.example.com", ".example.com", "127.1", "0177.0.0.1", "0x7f000001", "EXAMPLE.com", "example.com.", "user@host", "host/path", "[::1]", "fe80::1%en0", "127.0.0.1/8"} {
		value := p
		value.Bypass = []ProxyBypass{{Host: bad}}
		if value.Validate() == nil {
			t.Errorf("accepted bypass %s", bad)
		}
	}
	for _, mode := range []ProxyMode{"", "socks5h", "auto"} {
		value := p
		value.Mode = mode
		if value.Validate() == nil {
			t.Error("accepted mode", mode)
		}
	}
	value := p
	value.Mode = ProxyDirect
	if value.Validate() == nil {
		t.Error("Direct with endpoint")
	}
	for _, c := range []ProxyCredential{{Username: "user:pass", Password: "x"}, {Username: "user", Password: ""}, {Username: "user", Password: "line\nbreak"}} {
		if c.Validate() == nil {
			t.Error("accepted credential")
		}
	}
}

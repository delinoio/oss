// SPDX-License-Identifier: Apache-2.0
package domain

import "testing"

func TestMCPMetadataExcludesCredentialEndpointsAndMixedTransports(t *testing.T) {
	good := MCPDefinition{ID: NewID(), Name: "fixture", Transport: MCPHTTP, Endpoint: "https://mcp.example.test/api", Authentication: MCPAnonymous}
	if e := good.Validate(); e != nil {
		t.Fatal(e)
	}
	for _, endpoint := range []string{"https://user:password@mcp.example.test", "https://mcp.example.test?token=secret", "https://mcp.example.test#secret", "http://untrusted.example.test", "file:///tmp/mcp"} {
		bad := good
		bad.Endpoint = endpoint
		if bad.Validate() == nil {
			t.Fatal("unsafe endpoint accepted", endpoint)
		}
	}
	mixed := good
	mixed.Executable = "/bin/mcp"
	if mixed.Validate() == nil {
		t.Fatal("mixed transport accepted")
	}
	good.Endpoint = "http://127.0.0.1:1234/mcp"
	if good.Validate() != nil {
		t.Fatal("explicit loopback fixture rejected")
	}
}

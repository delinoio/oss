package domain

import "testing"

func TestMcpServerValidate(t *testing.T) {
	cases := []struct {
		name  string
		value McpServer
		ok    bool
	}{
		{"stdio", McpServer{Name: "fs", Transport: McpTransportStdio, Command: "npx", Args: []string{"-y", "srv"}, WorkingDirectory: "/work"}, true},
		{"http", McpServer{Name: "docs", Transport: McpTransportStreamableHTTP, URL: "https://example.com/mcp"}, true},
		{"missing name", McpServer{Transport: McpTransportStdio, Command: "x"}, false},
		{"unknown transport", McpServer{Name: "a", Transport: "sse", URL: "https://e.com"}, false},
		{"stdio without command", McpServer{Name: "a", Transport: McpTransportStdio}, false},
		{"stdio with url", McpServer{Name: "a", Transport: McpTransportStdio, Command: "x", URL: "https://e.com"}, false},
		{"relative cwd", McpServer{Name: "a", Transport: McpTransportStdio, Command: "x", WorkingDirectory: "rel"}, false},
		{"http with command", McpServer{Name: "a", Transport: McpTransportStreamableHTTP, URL: "https://e.com", Command: "x"}, false},
		{"http credentials", McpServer{Name: "a", Transport: McpTransportStreamableHTTP, URL: "https://u:p@e.com"}, false},
		{"http bad scheme", McpServer{Name: "a", Transport: McpTransportStreamableHTTP, URL: "ftp://e.com"}, false},
		{"http without url", McpServer{Name: "a", Transport: McpTransportStreamableHTTP}, false},
	}
	for _, c := range cases {
		if err := c.value.Validate(); (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.name, err, c.ok)
		}
	}
}

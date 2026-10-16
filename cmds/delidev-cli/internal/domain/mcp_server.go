package domain

import (
	"net/url"
	"path/filepath"
	"strings"
)

// McpTransport is the closed set of MCP server transports a Worker can own.
type McpTransport string

const (
	McpTransportStdio          McpTransport = "stdio"
	McpTransportStreamableHTTP McpTransport = "streamable_http"
)

func (t McpTransport) Valid() bool {
	return t == McpTransportStdio || t == McpTransportStreamableHTTP
}

const (
	maxMcpArgs      = 64
	maxMcpArgLength = 4096
)

// McpServer is a Worker-owned MCP server definition. It carries no secret
// material: environment values, headers and OAuth credentials are managed by
// a later credential-reference change. Storing a definition never starts the
// server, signs in or runs inference.
type McpServer struct {
	Name             string       `json:"name"`
	Transport        McpTransport `json:"transport"`
	Enabled          bool         `json:"enabled"`
	Command          string       `json:"command,omitempty"`
	Args             []string     `json:"args,omitempty"`
	WorkingDirectory string       `json:"working_directory,omitempty"`
	URL              string       `json:"url,omitempty"`
}

func (m McpServer) Validate() error {
	if err := Text(m.Name, "MCP server name", 256, true); err != nil {
		return err
	}
	switch m.Transport {
	case McpTransportStdio:
		if err := Text(m.Command, "MCP server command", 4096, true); err != nil {
			return err
		}
		if len(m.Args) > maxMcpArgs {
			return Fail(InvalidArgument, "Too many MCP server arguments.", "Use at most 64 arguments.")
		}
		for _, arg := range m.Args {
			if err := Text(arg, "MCP server argument", maxMcpArgLength, false); err != nil {
				return err
			}
		}
		if err := Text(m.WorkingDirectory, "MCP server working directory", 4096, false); err != nil {
			return err
		}
		if m.WorkingDirectory != "" && !filepath.IsAbs(m.WorkingDirectory) {
			return Fail(InvalidArgument, "The MCP working directory must be an absolute path.", "Use an absolute directory on the selected Runner Device.")
		}
		if m.URL != "" {
			return Fail(InvalidArgument, "A stdio MCP server cannot have an endpoint URL.", "Remove the URL or choose Streamable HTTP.")
		}
	case McpTransportStreamableHTTP:
		if m.Command != "" || len(m.Args) > 0 || m.WorkingDirectory != "" {
			return Fail(InvalidArgument, "A Streamable HTTP MCP server cannot have a local command.", "Remove the command fields or choose stdio.")
		}
		if err := Text(m.URL, "MCP server URL", 2048, true); err != nil {
			return err
		}
		parsed, err := url.Parse(m.URL)
		if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsAny(m.URL, " \t\r\n") {
			return Fail(InvalidArgument, "Enter an HTTP or HTTPS MCP endpoint without credentials.", "Use a credential-free endpoint URL; secrets are stored separately.")
		}
	default:
		return Fail(InvalidArgument, "Choose a supported MCP transport.", "Use stdio or Streamable HTTP.")
	}
	return nil
}

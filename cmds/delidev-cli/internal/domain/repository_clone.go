// SPDX-License-Identifier: Apache-2.0
package domain

import (
	"net"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
)

type RepositoryCloneTransport string

const (
	RepositoryCloneHTTPS RepositoryCloneTransport = "https"
	RepositoryCloneSSH   RepositoryCloneTransport = "ssh"
)

// The original URL is an argv operand, never a shell command. Derived identity
// is metadata only; it cannot supply a PAT, helper transport or remote authority.
type RepositoryCloneURL struct {
	Transport     RepositoryCloneTransport
	Host          string
	Path          string
	DirectoryName string
	GitHubOwner   string
	GitHubName    string
}

func cloneInvalidURL() error {
	return Fail(InvalidArgument, "Enter a credential-free HTTPS or SSH Git URL.", "Use HTTPS, ssh:// or SCP-style SSH. Local paths, passwords, tokens and helper transports are unsupported.")
}

func cloneHost(value string) bool {
	if net.ParseIP(value) != nil {
		return true
	}
	if value == "" || len(value) > 253 || strings.HasPrefix(value, "-") || strings.HasSuffix(value, ".") {
		return false
	}
	for _, part := range strings.Split(value, ".") {
		if part == "" || len(part) > 63 || strings.HasPrefix(part, "-") || strings.HasSuffix(part, "-") {
			return false
		}
		for _, c := range part {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

func ParseRepositoryCloneURL(value string) (RepositoryCloneURL, error) {
	var result RepositoryCloneURL
	if Text(value, "Git URL", 4096, true) != nil || strings.TrimSpace(value) != value || strings.ContainsAny(value, "\\?#") {
		return result, cloneInvalidURL()
	}
	for _, c := range value {
		if unicode.IsControl(c) || unicode.IsSpace(c) {
			return result, cloneInvalidURL()
		}
	}
	if strings.Contains(value, "://") {
		u, err := url.Parse(value)
		if err != nil || u.Opaque != "" || u.RawQuery != "" || u.Fragment != "" || strings.HasSuffix(u.Host, ":") || !cloneHost(u.Hostname()) || (u.Scheme != "https" && u.Scheme != "ssh") {
			return result, cloneInvalidURL()
		}
		if u.Port() != "" {
			p, err := strconv.Atoi(u.Port())
			if err != nil || p < 1 || p > 65535 {
				return result, cloneInvalidURL()
			}
		}
		if u.User != nil {
			if u.Scheme != "ssh" {
				return result, cloneInvalidURL()
			}
			if _, password := u.User.Password(); password || !cloneSSHUser(u.User.Username()) {
				return result, cloneInvalidURL()
			}
		}
		if u.Path == "" || u.Path == "/" {
			return result, cloneInvalidURL()
		}
		result.Transport = RepositoryCloneHTTPS
		if u.Scheme == "ssh" {
			result.Transport = RepositoryCloneSSH
		}
		result.Host, result.Path = u.Hostname(), u.Path
		if u.Port() != "" && !((u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "ssh" && u.Port() == "22")) {
			result.Host = u.Host
		}
	} else {
		endpoint, remotePath, ok := strings.Cut(value, ":")
		if !ok || len(endpoint) < 2 || strings.Contains(remotePath, ":") || strings.HasPrefix(value, "-") {
			return result, cloneInvalidURL()
		}
		host := endpoint
		if user, rest, hasUser := strings.Cut(endpoint, "@"); hasUser {
			if !cloneSSHUser(user) {
				return result, cloneInvalidURL()
			}
			host = rest
		}
		if !cloneHost(host) || remotePath == "" {
			return result, cloneInvalidURL()
		}
		result.Transport, result.Host, result.Path = RepositoryCloneSSH, host, remotePath
	}
	if Text(result.Path, "remote repository path", 4096, true) != nil {
		return RepositoryCloneURL{}, cloneInvalidURL()
	}
	for _, c := range result.Path {
		if unicode.IsControl(c) || c == '\\' {
			return RepositoryCloneURL{}, cloneInvalidURL()
		}
	}
	for _, component := range strings.Split(result.Path, "/") {
		if component == "." || component == ".." {
			return RepositoryCloneURL{}, cloneInvalidURL()
		}
	}
	result.DirectoryName = strings.TrimSuffix(path.Base(strings.TrimSuffix(result.Path, "/")), ".git")
	if strings.EqualFold(result.Host, "github.com") {
		parts := strings.Split(strings.Trim(result.Path, "/"), "/")
		if len(parts) == 2 && ValidateGitHubRepository(parts[0], strings.TrimSuffix(parts[1], ".git")) == nil {
			result.GitHubOwner, result.GitHubName = parts[0], strings.TrimSuffix(parts[1], ".git")
		}
	}
	return result, nil
}

func cloneSSHUser(value string) bool {
	if value == "" || len(value) > 100 || strings.HasPrefix(value, "-") {
		return false
	}
	for _, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("_.-", c)) {
			return false
		}
	}
	return true
}

func ValidateRepositoryCloneDirectory(value string) error {
	invalid := func() error {
		return Fail(InvalidArgument, "Enter a portable repository folder name.", "Use one folder name without separators, reserved device names, trailing dots or spaces.")
	}
	if Text(value, "repository folder name", 255, true) != nil || value == "." || value == ".." || strings.ContainsAny(value, "<>:\"/\\|?*") || strings.HasSuffix(value, ".") || strings.TrimSpace(value) != value {
		return invalid()
	}
	for _, c := range value {
		if unicode.IsControl(c) {
			return invalid()
		}
	}
	stem := strings.ToUpper(strings.SplitN(value, ".", 2)[0])
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" || (len(stem) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) && stem[3] >= '1' && stem[3] <= '9') {
		return invalid()
	}
	return nil
}

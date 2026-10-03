// SPDX-License-Identifier: Apache-2.0
// Package sshsetup owns the closed installation-only SSH transport.
package sshsetup

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/updates"
	"golang.org/x/crypto/ssh"
)

const OutputLimit = 64 << 10
const Timeout = 30 * time.Second

var hostKeyAlgorithms = []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256}

type Target struct {
	Host string `json:"host"`
	Port uint16 `json:"port"`
	User string `json:"user"`
}
type Identity struct {
	Fingerprint string `json:"fingerprint"`
	Algorithm   string `json:"algorithm"`
}
type Authentication string

const (
	PrivateKey Authentication = "private-key"
	Password   Authentication = "password"
)

type Credential struct {
	Method     Authentication `json:"method"`
	Secret     []byte         `json:"secret"`
	Passphrase []byte         `json:"passphrase,omitempty"`
}

func failure(code domain.Code) error {
	return domain.Fail(code, "The SSH Worker setup could not complete.", "Inspect the original setup and explicitly confirmed host identity. Preserve existing Worker registrations and workspaces.")
}
func (t Target) Validate() error {
	if t.Port == 0 || len(t.Host) == 0 || len(t.Host) > 253 || len(t.User) == 0 || len(t.User) > 64 {
		return failure(domain.InvalidArgument)
	}
	for _, s := range []string{t.Host, t.User} {
		for _, r := range s {
			if r < 33 || r > 126 || strings.ContainsRune("'\"`$;|&<>/", r) {
				return failure(domain.InvalidArgument)
			}
		}
	}
	if ip, e := netip.ParseAddr(t.Host); e == nil {
		if ip.Zone() != "" || ip.IsUnspecified() || ip.IsMulticast() {
			return failure(domain.InvalidArgument)
		}
	} else {
		if strings.ContainsAny(t.Host, ":[]%\\") || strings.Trim(t.Host, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-") != "" || strings.Contains(t.Host, "..") || strings.HasPrefix(t.Host, ".") || strings.HasSuffix(t.Host, ".") {
			return failure(domain.InvalidArgument)
		}
	}
	return nil
}
func (i Identity) Validate() error {
	digest, err := base64.RawStdEncoding.Strict().DecodeString(strings.TrimPrefix(i.Fingerprint, "SHA256:"))
	if !strings.HasPrefix(i.Fingerprint, "SHA256:") || len(digest) != 32 || err != nil || "SHA256:"+base64.RawStdEncoding.EncodeToString(digest) != i.Fingerprint {
		return failure(domain.InvalidArgument)
	}
	for _, a := range hostKeyAlgorithms {
		if i.Algorithm == a || a == ssh.KeyAlgoRSASHA256 && i.Algorithm == ssh.KeyAlgoRSA {
			return nil
		}
	}
	return failure(domain.Unsupported)
}
func (c *Credential) Clear() {
	clear(c.Secret)
	clear(c.Passphrase)
	c.Secret = nil
	c.Passphrase = nil
}
func (c Credential) Validate() error { _, err := c.auth(); return err }
func (c Credential) auth() (ssh.AuthMethod, error) {
	if len(c.Secret) == 0 || len(c.Secret) > 48<<10 || len(c.Passphrase) > 8<<10 {
		return nil, failure(domain.InvalidArgument)
	}
	switch c.Method {
	case Password:
		if len(c.Passphrase) != 0 || bytes.ContainsRune(c.Secret, 0) {
			return nil, failure(domain.InvalidArgument)
		}
		return ssh.Password(string(c.Secret)), nil
	case PrivateKey:
		var signer ssh.Signer
		var e error
		if len(c.Passphrase) > 0 {
			signer, e = ssh.ParsePrivateKeyWithPassphrase(c.Secret, c.Passphrase)
		} else {
			signer, e = ssh.ParsePrivateKey(c.Secret)
		}
		if e != nil {
			return nil, failure(domain.InvalidArgument)
		}
		return ssh.PublicKeys(signer), nil
	default:
		return nil, failure(domain.Unsupported)
	}
}
func dial(ctx context.Context, t Target) (net.Conn, error) {
	if t.Validate() != nil {
		return nil, failure(domain.InvalidArgument)
	}
	conn, e := (&net.Dialer{Timeout: Timeout}).DialContext(ctx, "tcp", net.JoinHostPort(t.Host, strconv.Itoa(int(t.Port))))
	if e != nil {
		return nil, failure(domain.Unavailable)
	}
	deadline := time.Now().Add(Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	if conn.SetDeadline(deadline) != nil {
		conn.Close()
		return nil, failure(domain.Unavailable)
	}
	return conn, nil
}
func joinCancellation(ctx context.Context, close func()) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { defer closeChannel(done); close() })
	return func() {
		if !stop() {
			<-done
		}
	}
}
func closeChannel(done chan struct{}) { close(done) }

// Observe aborts the SSH handshake at the host-key callback, before any
// authentication. It never accepts that observed key as future authority.
func Observe(ctx context.Context, t Target) (Identity, error) {
	conn, e := dial(ctx, t)
	if e != nil {
		return Identity{}, e
	}
	defer conn.Close()
	join := joinCancellation(ctx, func() { conn.Close() })
	defer join()
	var identity Identity
	observed := errors.New("host identity observed")
	config := &ssh.ClientConfig{User: t.User, Timeout: Timeout, HostKeyAlgorithms: hostKeyAlgorithms, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		identity = Identity{ssh.FingerprintSHA256(key), key.Type()}
		return observed
	}}
	_, _, _, e = ssh.NewClientConn(conn, net.JoinHostPort(t.Host, strconv.Itoa(int(t.Port))), config)
	if identity.Validate() != nil || e == nil {
		return Identity{}, failure(domain.Unavailable)
	}
	if ctx.Err() != nil {
		return Identity{}, domain.SafeError(ctx.Err())
	}
	return identity, nil
}

type Connection struct {
	client *ssh.Client
	conn   net.Conn
	join   func()
	once   sync.Once
}

func Connect(ctx context.Context, t Target, pin Identity, credential Credential) (*Connection, error) {
	if pin.Validate() != nil {
		return nil, failure(domain.InvalidArgument)
	}
	auth, e := credential.auth()
	if e != nil {
		return nil, e
	}
	conn, e := dial(ctx, t)
	if e != nil {
		return nil, e
	}
	join := joinCancellation(ctx, func() { conn.Close() })
	config := &ssh.ClientConfig{User: t.User, Auth: []ssh.AuthMethod{auth}, Timeout: Timeout, HostKeyAlgorithms: hostKeyAlgorithms, HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
		actual := Identity{ssh.FingerprintSHA256(key), key.Type()}
		if subtle.ConstantTimeCompare([]byte(actual.Fingerprint), []byte(pin.Fingerprint)) != 1 || actual.Algorithm != pin.Algorithm {
			return failure(domain.PermissionDenied)
		}
		return nil
	}}
	client, chans, requests, e := ssh.NewClientConn(conn, net.JoinHostPort(t.Host, strconv.Itoa(int(t.Port))), config)
	if e != nil {
		conn.Close()
		join()
		return nil, failure(domain.PermissionDenied)
	}
	if conn.SetDeadline(time.Time{}) != nil {
		client.Close()
		join()
		return nil, failure(domain.Unavailable)
	}
	return &Connection{client: ssh.NewClient(client, chans, requests), conn: conn, join: join}, nil
}
func (c *Connection) Close() { c.once.Do(func() { c.client.Close(); c.conn.Close(); c.join() }) }

type boundedBuffer struct {
	bytes.Buffer
	over bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.Len()+n > OutputLimit {
		b.over = true
		return 0, failure(domain.ResourceExhausted)
	}
	return b.Buffer.Write(p)
}

// Command is private to the installation state machine. Product callers supply
// closed operations, never shell strings or arbitrary target paths.
func (c *Connection) command(ctx context.Context, command string, input io.Reader) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	session, e := c.client.NewSession()
	if e != nil {
		return nil, failure(domain.Unavailable)
	}
	defer session.Close()
	join := joinCancellation(ctx, func() { session.Close() })
	defer join()
	var output, discard boundedBuffer
	session.Stdout = &output
	session.Stderr = &discard
	session.Stdin = input
	e = session.Run(command)
	if ctx.Err() != nil {
		return nil, domain.SafeError(ctx.Err())
	}
	if output.over || discard.over {
		return nil, failure(domain.Unavailable)
	}
	if e != nil {
		// Only the closed setup parser may inspect this bounded envelope on exit
		// failure. No raw stdout/stderr is exposed to callers or diagnostics.
		return append([]byte(nil), output.Bytes()...), failure(domain.Unavailable)
	}
	return append([]byte(nil), output.Bytes()...), nil
}

// Inspection uses fixed read-only OS probes; no product input becomes a command.
func (c *Connection) Inspect(ctx context.Context) (updates.Target, error) {
	ctx, cancel := context.WithTimeout(ctx, Timeout)
	defer cancel()
	raw, e := c.command(ctx, "uname -s; uname -m", nil)
	parts := strings.Fields(string(raw))
	if e != nil || len(parts) != 2 || (parts[0] != "Darwin" && parts[0] != "Linux") {
		if ctx.Err() != nil {
			return "", domain.SafeError(ctx.Err())
		}
		raw, e = c.command(ctx, `powershell.exe -NoProfile -NonInteractive -Command "Write-Output 'windows'; Write-Output ([System.Runtime.InteropServices.RuntimeInformation]::OSArchitecture); Write-Output ([System.Environment]::OSVersion.Version.Build)"`, nil)
		parts = strings.Fields(string(raw))
		if e != nil || len(parts) != 3 || parts[0] != "windows" {
			return "", failure(domain.Unsupported)
		}
		build, e := strconv.ParseUint(parts[2], 10, 32)
		if e != nil || build < 19045 {
			return "", failure(domain.Unsupported)
		}
		architecture := ""
		switch parts[1] {
		case "X64":
			architecture = "amd64"
		case "Arm64":
			architecture = "arm64"
		default:
			return "", failure(domain.Unsupported)
		}
		return updates.SelectTarget("windows", architecture)
	}
	platform := "linux"
	if parts[0] == "Darwin" {
		platform = "darwin"
	}
	architecture := ""
	switch parts[1] {
	case "x86_64":
		architecture = "amd64"
	case "arm64", "aarch64":
		architecture = "arm64"
	default:
		return "", failure(domain.Unsupported)
	}
	if platform == "darwin" {
		raw, e = c.command(ctx, "sw_vers -productVersion", nil)
		major, parseErr := strconv.ParseUint(strings.Split(strings.TrimSpace(string(raw)), ".")[0], 10, 16)
		if e != nil || parseErr != nil || major < 13 {
			return "", failure(domain.Unsupported)
		}
	} else {
		raw, e = c.command(ctx, `sh -c '. /etc/os-release; printf "%s\n%s\n" "$ID" "$VERSION_ID"'`, nil)
		version := strings.Fields(string(raw))
		if e != nil || len(version) != 2 || version[0] != "ubuntu" {
			return "", failure(domain.Unsupported)
		}
		split := strings.Split(version[1], ".")
		if len(split) != 2 {
			return "", failure(domain.Unsupported)
		}
		major, me := strconv.ParseUint(split[0], 10, 16)
		minor, ne := strconv.ParseUint(split[1], 10, 16)
		if me != nil || ne != nil || major < 22 || major == 22 && minor < 4 {
			return "", failure(domain.Unsupported)
		}
	}
	return updates.SelectTarget(platform, architecture)
}

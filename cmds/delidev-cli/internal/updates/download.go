// SPDX-License-Identifier: Apache-2.0
package updates

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type Client struct {
	verifier Verifier
	http     *http.Client
}

func NewClient() (*Client, error) {
	v, err := NewVerifier()
	if err != nil {
		return nil, err
	}
	return &Client{v, newHTTPClient()}, nil
}
func newHTTPClient() *http.Client {
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, TLSHandshakeTimeout: 15 * time.Second, ResponseHeaderTimeout: 30 * time.Second, MaxConnsPerHost: 2, DisableCompression: true, ForceAttemptHTTP2: true}
	return &http.Client{Transport: transport, Timeout: 10 * time.Minute, CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 3 || req.URL.Scheme != "https" || req.URL.Host != "release-assets.githubusercontent.com" || req.URL.User != nil || req.URL.Fragment != "" {
			return failure(domain.PermissionDenied)
		}
		if len(via) == 0 || via[0].URL.Host != "github.com" || !strings.HasPrefix(via[0].URL.Path, "/"+Repository+"/releases/download/"+TagPrefix) {
			return failure(domain.PermissionDenied)
		}
		return nil
	}}
}
func (c *Client) Close() { c.http.CloseIdleConnections() }
func (c *Client) read(ctx context.Context, source string, limit int64) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return nil, failure(domain.InvalidArgument)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", "DeliDev-Updater/1")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, failure(domain.Unavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return nil, failure(domain.Unavailable)
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, failure(domain.ResourceExhausted)
	}
	return raw, nil
}

// Latest scans bounded release metadata because the repository's global latest
// release can belong to an unrelated project. It never trusts API asset URLs.
func (c *Client) Latest(ctx context.Context, current string, now time.Time) (Verified, error) {
	selected := ""
	for page := 1; page <= 10; page++ {
		source := "https://api.github.com/repos/" + Repository + "/releases?per_page=100&page=" + jsonNumber(page)
		raw, err := c.read(ctx, source, 2<<20)
		if err != nil {
			return Verified{}, err
		}
		var releases []map[string]json.RawMessage
		if domain.DecodeWithLimit(raw, &releases, 2<<20) != nil || len(releases) > 100 {
			return Verified{}, failure(domain.InvalidArgument)
		}
		for _, entry := range releases {
			var tag string
			var draft, pre bool
			if json.Unmarshal(entry["tag_name"], &tag) != nil || json.Unmarshal(entry["draft"], &draft) != nil || json.Unmarshal(entry["prerelease"], &pre) != nil {
				return Verified{}, failure(domain.InvalidArgument)
			}
			if draft || pre || !strings.HasPrefix(tag, TagPrefix) {
				continue
			}
			v := strings.TrimPrefix(tag, TagPrefix)
			newer, e := Newer(v, current)
			if e != nil {
				continue
			}
			if !newer {
				continue
			}
			if selected == "" {
				selected = v
			} else if higher, _ := Newer(v, selected); higher {
				selected = v
			}
		}
		if len(releases) < 100 {
			if selected == "" {
				return Verified{}, failure(domain.NotFound)
			}
			raw, err := c.read(ctx, ReleaseURL(selected, ManifestName), ManifestLimit)
			if err != nil {
				return Verified{}, err
			}
			verified, err := c.verifier.Verify(raw, current, now)
			if err != nil || verified.Payload.Version != selected {
				return Verified{}, failure(domain.PermissionDenied)
			}
			return verified, nil
		}
	}
	return Verified{}, failure(domain.ResourceExhausted)
}
func jsonNumber(n int) string { b, _ := json.Marshal(n); return string(b) }
func ValidateArtifactURL(source, version, name string) error {
	u, err := url.Parse(source)
	if err != nil || u.String() != ReleaseURL(version, name) || u.RawPath != "" || u.RawQuery != "" || u.User != nil || u.Fragment != "" {
		return failure(domain.PermissionDenied)
	}
	return nil
}

// Download publishes immutable content by digest only after length, digest and
// file synchronization succeed. The caller must retain its original candidate.
func (c *Client) Download(ctx context.Context, v Verified, component Component, target Target, root string) (string, error) {
	verified, err := c.verifier.Verify(v.Canonical, "0.0.0", time.Now().UTC())
	if err != nil || verified.ManifestSHA256 != v.ManifestSHA256 {
		return "", failure(domain.PermissionDenied)
	}
	// Retained typed metadata cannot substitute for the original signed bytes.
	v = verified
	a, err := v.Artifact(component, target)
	if err != nil {
		return "", err
	}
	if ValidateArtifactURL(a.URL, v.Payload.Version, a.Name) != nil {
		return "", failure(domain.PermissionDenied)
	}
	if err := security.PrivateDir(root); err != nil {
		return "", err
	}
	path := filepath.Join(root, a.SHA256+filepath.Ext(a.Name))
	if _, err := os.Lstat(path); err == nil {
		if err := VerifyFile(path, a); err != nil {
			return "", err
		}
		return path, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", failure(domain.RecoveryRequired)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return "", failure(domain.InvalidArgument)
	}
	request.Header.Set("Accept", "application/octet-stream")
	request.Header.Set("User-Agent", "DeliDev-Updater/1")
	response, err := c.http.Do(request)
	if err != nil {
		return "", failure(domain.Unavailable)
	}
	defer response.Body.Close()
	if response.StatusCode != 200 || (response.ContentLength >= 0 && response.ContentLength != a.Size) {
		return "", failure(domain.InvalidArgument)
	}
	f, err := os.CreateTemp(root, ".download-")
	if err != nil {
		return "", failure(domain.Unavailable)
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(response.Body, a.Size+1))
	if err != nil || n != a.Size || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return "", failure(domain.PermissionDenied)
	}
	if ctx.Err() != nil {
		return "", domain.SafeError(ctx.Err())
	}
	if f.Sync() != nil || f.Close() != nil {
		return "", failure(domain.Unavailable)
	}
	// Hard-link publication does not replace an existing verified generation.
	if err := security.PublishImmutable(tmp, path); err != nil {
		if VerifyFile(path, a) != nil {
			return "", failure(domain.RecoveryRequired)
		}
	}
	if security.SyncParent(path) != nil {
		return "", failure(domain.Unavailable)
	}
	return path, nil
}
func VerifyFile(path string, a Artifact) error {
	f, err := OpenArtifact(path, a)
	if err != nil {
		return err
	}
	return f.Close()
}

// OpenArtifact retains the exact verified file descriptor for the owned consumer.
func OpenArtifact(path string, a Artifact) (*os.File, error) {
	if a.Size <= 0 || a.Size > ArtifactLimit || !digestValid(a.SHA256) {
		return nil, failure(domain.InvalidArgument)
	}
	if security.RegularPrivate(path) != nil {
		return nil, failure(domain.PermissionDenied)
	}
	before, err := os.Lstat(path)
	if err != nil || before.Size() != a.Size {
		return nil, failure(domain.PermissionDenied)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, failure(domain.Unavailable)
	}
	accepted := false
	defer func() {
		if !accepted {
			f.Close()
		}
	}()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(before, opened) {
		return nil, failure(domain.PermissionDenied)
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, a.Size+1))
	after, e := f.Stat()
	named, ne := os.Lstat(path)
	if err != nil || e != nil || ne != nil || n != a.Size || !os.SameFile(before, after) || !os.SameFile(before, named) || after.Size() != a.Size || named.Size() != a.Size || !before.ModTime().Equal(after.ModTime()) || hex.EncodeToString(h.Sum(nil)) != a.SHA256 {
		return nil, failure(domain.PermissionDenied)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, failure(domain.Unavailable)
	}
	accepted = true
	return f, nil
}

// Release selects the server's exact compatible Worker version for first SSH
// pairing. It cannot weaken the ordinary exact-version gate with a latest tag.
func (c *Client) Release(ctx context.Context, version string, now time.Time) (Verified, error) {
	if _, e := Newer(version, "0.0.0"); e != nil {
		return Verified{}, e
	}
	raw, e := c.read(ctx, ReleaseURL(version, ManifestName), ManifestLimit)
	if e != nil {
		return Verified{}, e
	}
	verified, e := c.verifier.Verify(raw, "0.0.0", now)
	if e != nil || verified.Payload.Version != version {
		return Verified{}, failure(domain.PermissionDenied)
	}
	return verified, nil
}

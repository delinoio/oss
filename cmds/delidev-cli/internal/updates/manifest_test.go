// SPDX-License-Identifier: Apache-2.0
package updates

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/gowebpki/jcs"
)

func fixture(t *testing.T) (Verifier, ed25519.PrivateKey, Payload) {
	t.Helper()
	pub, key, e := ed25519.GenerateKey(rand.Reader)
	if e != nil {
		t.Fatal(e)
	}
	p := Payload{SchemaVersion: 1, Version: "0.2.0", SourceRevision: strings.Repeat("a", 40), ProtocolVersion: 1, PublishedAt: "2026-10-01T00:00:00Z"}
	data := []byte("signed-worker-fixture")
	sum := sha256.Sum256(data)
	for _, c := range []Component{Desktop, Worker} {
		for _, target := range Targets {
			name := ArtifactName(c, target)
			p.Artifacts = append(p.Artifacts, Artifact{c, target, name, int64(len(data)), hex.EncodeToString(sum[:]), ReleaseURL(p.Version, name)})
		}
	}
	return Verifier{Root{1, "fixture", base64.StdEncoding.EncodeToString(pub), true}}, key, p
}
func sign(t *testing.T, key ed25519.PrivateKey, p Payload) []byte {
	t.Helper()
	raw, _ := json.Marshal(p)
	canon, e := jcs.Transform(raw)
	if e != nil {
		t.Fatal(e)
	}
	raw, e = json.Marshal(Manifest{"fixture", canon, base64.StdEncoding.EncodeToString(ed25519.Sign(key, append([]byte(SignatureDomain), canon...)))})
	if e != nil {
		t.Fatal(e)
	}
	return raw
}
func now() time.Time { return time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC) }
func TestProductionRootCannotActivateFixture(t *testing.T) {
	if ProductionReady() {
		t.Fatal("Pending production root unexpectedly active")
	}
	if _, e := NewClient(); e == nil {
		t.Fatal("Missing root permitted network")
	}
}
func TestSignedCompleteInventoryAndRejections(t *testing.T) {
	v, k, p := fixture(t)
	raw := sign(t, k, p)
	good, e := v.Verify(raw, "0.1.0", now())
	if e != nil || len(good.Payload.Artifacts) != 12 {
		t.Fatal(e)
	}
	cases := map[string]func(*Payload){"missing": func(p *Payload) { p.Artifacts = p.Artifacts[:11] }, "duplicate": func(p *Payload) { p.Artifacts[1] = p.Artifacts[0] }, "foreign-project": func(p *Payload) { p.Artifacts[0].URL = strings.Replace(p.Artifacts[0].URL, "delidev-v", "devhud-v", 1) }, "unknown-target": func(p *Payload) { p.Artifacts[0].Target = "linux-386" }, "uppercase-hash": func(p *Payload) { p.Artifacts[0].SHA256 = strings.ToUpper(p.Artifacts[0].SHA256) }, "oversized": func(p *Payload) { p.Artifacts[0].Size = ArtifactLimit + 1 }, "wrong-protocol": func(p *Payload) { p.ProtocolVersion = 2 }, "future": func(p *Payload) { p.PublishedAt = "2027-01-01T00:00:00Z" }, "bad-source": func(p *Payload) { p.SourceRevision = "main" }, "downgrade": func(p *Payload) { p.Version = "0.0.1" }, "suffix": func(p *Payload) { p.Version = "0.2.0-beta" }}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			_, _, p := fixture(t)
			edit(&p)
			if _, e := v.Verify(sign(t, k, p), "0.1.0", now()); e == nil {
				t.Fatal("Invalid signed candidate accepted")
			}
		})
	}
	for _, bad := range [][]byte{bytes.Replace(raw, []byte("fixture"), []byte("other"), 1), bytes.Replace(raw, []byte(`"version":"0.2.0"`), []byte(`"version":"0.3.0"`), 1), append(raw, raw...), bytes.Replace(raw, []byte(`"keyId":"fixture"`), []byte(`"keyId":"fixture","keyId":"fixture"`), 1)} {
		if _, e := v.Verify(bad, "0.1.0", now()); e == nil {
			t.Fatal("Tampered envelope accepted")
		}
	}
	if _, e := v.Verify(raw, "0.2.0", now()); e == nil {
		t.Fatal("Same-version update accepted")
	}
}

type routingTransport struct {
	server string
	base   http.RoundTripper
}

func (r routingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	copy.URL.Scheme = "http"
	copy.URL.Host = strings.TrimPrefix(r.server, "http://")
	return r.base.RoundTrip(copy)
}
func TestActualHTTPNamespaceSelectionDownloadAndCancellation(t *testing.T) {
	v, k, p := fixture(t)
	raw := sign(t, k, p)
	data := []byte("signed-worker-fixture")
	var tamper bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("Credential sent")
		}
		switch {
		case r.URL.Path == "/repos/"+Repository+"/releases":
			io.WriteString(w, `[{"tag_name":"react-forge-v99.0.0","draft":false,"prerelease":false},{"tag_name":"delidev-v0.2.0","draft":false,"prerelease":false}]`)
		case strings.HasSuffix(r.URL.Path, ManifestName):
			w.Write(raw)
		default:
			if tamper {
				w.Write(bytes.Repeat([]byte("x"), len(data)))
			} else {
				w.Write(data)
			}
		}
	}))
	defer server.Close()
	c := &Client{v, &http.Client{Transport: routingTransport{server.URL, http.DefaultTransport}}}
	verified, e := c.Latest(context.Background(), "0.1.0", now())
	if e != nil {
		t.Fatal(e)
	}
	root := filepath.Join(t.TempDir(), "private")
	path, e := c.Download(context.Background(), verified, Worker, "darwin-arm64", root)
	if e != nil {
		t.Fatal(e)
	}
	if b, e := os.ReadFile(path); e != nil || !bytes.Equal(b, data) {
		t.Fatal(e)
	}
	again, e := c.Download(context.Background(), verified, Worker, "darwin-arm64", root)
	if e != nil || again != path {
		t.Fatal("Content replay failed", e)
	}
	os.Remove(path)
	tamper = true
	if _, e = c.Download(context.Background(), verified, Worker, "darwin-arm64", root); e == nil {
		t.Fatal("Changed artifact accepted")
	}
	if _, e = os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("Tampered download published")
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, e = c.Download(canceled, verified, Worker, "darwin-arm64", root); e == nil {
		t.Fatal("Canceled download published")
	}
	files, _ := os.ReadDir(root)
	if len(files) != 0 {
		t.Fatal("Failed download staging leaked")
	}
	os.WriteFile(path, data, 0600)
	a, _ := verified.Artifact(Worker, "darwin-arm64")
	if e := VerifyFile(path, a); e != nil {
		t.Fatal(e)
	}
	os.Remove(path)
	os.Symlink(filepath.Join(root, "missing"), path)
	if VerifyFile(path, a) == nil {
		t.Fatal("Symlink accepted")
	}
}
func TestClosedTargetsAndVersionPrecision(t *testing.T) {
	for _, target := range Targets {
		parts := strings.Split(string(target), "-")
		actual, e := SelectTarget(parts[0], parts[1])
		if e != nil || actual != target {
			t.Fatal(e)
		}
	}
	if _, e := SelectTarget("freebsd", "amd64"); e == nil {
		t.Fatal("Unsupported OS accepted")
	}
	if _, e := Newer("01.2.0", "0.1.0"); e == nil {
		t.Fatal("Noncanonical version")
	}
	e := failure(domain.Unsupported)
	if e == nil {
		t.Fatal("Missing error")
	}
}

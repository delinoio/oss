// SPDX-License-Identifier: Apache-2.0
// Package updates verifies only the independently signed DeliDev release namespace.
package updates

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/gowebpki/jcs"
)

const ManifestLimit = 128 << 10
const ArtifactLimit = int64(2 << 30)
const Repository = "delinoio/oss"
const TagPrefix = "delidev-v"
const ManifestName = "delidev-update-manifest.json"
const SignatureDomain = "delidev-update-manifest-v1\x00"

//go:embed trust-root.json
var rootDeclaration []byte

type Component string

const (
	Desktop Component = "desktop"
	Worker  Component = "worker"
)

type Target string

var Targets = [...]Target{"darwin-amd64", "darwin-arm64", "windows-amd64", "windows-arm64", "linux-amd64", "linux-arm64"}

func (t Target) Valid() bool {
	for _, v := range Targets {
		if v == t {
			return true
		}
	}
	return false
}
func (c Component) Valid() bool { return c == Desktop || c == Worker }
func SelectTarget(os, architecture string) (Target, error) {
	t := Target(os + "-" + architecture)
	if !t.Valid() {
		return "", failure(domain.Unsupported)
	}
	return t, nil
}

type Root struct {
	SchemaVersion   uint32 `json:"schemaVersion"`
	KeyID           string `json:"keyId"`
	PublicKey       string `json:"publicKey"`
	ProductionReady bool   `json:"productionReady"`
}
type Artifact struct {
	Component Component `json:"component"`
	Target    Target    `json:"target"`
	Name      string    `json:"name"`
	Size      int64     `json:"size"`
	SHA256    string    `json:"sha256"`
	URL       string    `json:"url"`
}
type Payload struct {
	SchemaVersion   uint32     `json:"schemaVersion"`
	Version         string     `json:"version"`
	SourceRevision  string     `json:"sourceRevision"`
	ProtocolVersion uint32     `json:"protocolVersion"`
	PublishedAt     string     `json:"publishedAt"`
	Artifacts       []Artifact `json:"artifacts"`
}
type Manifest struct {
	KeyID     string          `json:"keyId"`
	Payload   json.RawMessage `json:"payload"`
	Signature string          `json:"signature"`
}
type Verified struct {
	Payload        Payload
	ManifestSHA256 string
	Canonical      []byte
}
type Verifier struct{ root Root }

func failure(code domain.Code) error {
	return domain.Fail(code, "The DeliDev release could not be verified.", "Preserve the current version; check the release signing declaration, target and compatible protocol before retrying.")
}
func NewVerifier() (Verifier, error) {
	var r Root
	if domain.Decode(rootDeclaration, &r) != nil || r.SchemaVersion != 1 || r.KeyID != "delidev-release-root-v1" || !r.ProductionReady {
		return Verifier{}, failure(domain.Unsupported)
	}
	if _, err := decodeBase64(r.PublicKey, ed25519.PublicKeySize); err != nil {
		return Verifier{}, err
	}
	return Verifier{r}, nil
}
func ProductionReady() bool { _, err := NewVerifier(); return err == nil }
func decodeBase64(value string, size int) ([]byte, error) {
	b, err := base64.StdEncoding.Strict().DecodeString(value)
	if err != nil || len(b) != size || base64.StdEncoding.EncodeToString(b) != value {
		return nil, failure(domain.InvalidArgument)
	}
	return b, nil
}
func digestValid(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == 32 && hex.EncodeToString(b) == s
}
func version(v string) ([3]uint64, error) {
	var result [3]uint64
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return result, failure(domain.InvalidArgument)
	}
	for i, p := range parts {
		n, e := strconv.ParseUint(p, 10, 32)
		if e != nil || strconv.FormatUint(n, 10) != p {
			return result, failure(domain.InvalidArgument)
		}
		result[i] = n
	}
	return result, nil
}
func Newer(candidate, current string) (bool, error) {
	a, err := version(candidate)
	if err != nil {
		return false, err
	}
	b, err := version(current)
	if err != nil {
		return false, err
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i], nil
		}
	}
	return false, nil
}
func ArtifactName(c Component, t Target) string {
	ext := ""
	if c == Desktop {
		switch {
		case strings.HasPrefix(string(t), "darwin-"):
			ext = ".dmg"
		case strings.HasPrefix(string(t), "windows-"):
			ext = ".exe"
		default:
			ext = ".AppImage"
		}
	} else if strings.HasPrefix(string(t), "windows-") {
		ext = ".exe"
	}
	return "delidev-" + string(c) + "-" + string(t) + ext
}
func ReleaseURL(v, name string) string {
	return "https://github.com/" + Repository + "/releases/download/" + TagPrefix + v + "/" + name
}
func (v Verifier) Verify(raw []byte, current string, now time.Time) (Verified, error) {
	var m Manifest
	var p Payload
	if domain.DecodeWithLimit(raw, &m, ManifestLimit) != nil || m.KeyID != v.root.KeyID {
		return Verified{}, failure(domain.InvalidArgument)
	}
	key, err := decodeBase64(v.root.PublicKey, 32)
	if err != nil {
		return Verified{}, err
	}
	sig, err := decodeBase64(m.Signature, 64)
	if err != nil {
		return Verified{}, err
	}
	canonical, err := jcs.Transform(m.Payload)
	if err != nil || !bytes.Equal(bytes.TrimSpace(m.Payload), canonical) {
		return Verified{}, failure(domain.InvalidArgument)
	}
	if !ed25519.Verify(key, append([]byte(SignatureDomain), canonical...), sig) {
		return Verified{}, failure(domain.PermissionDenied)
	}
	if domain.DecodeWithLimit(canonical, &p, ManifestLimit) != nil || p.SchemaVersion != 1 || p.ProtocolVersion != 1 || !digestValidSource(p.SourceRevision) || len(p.Artifacts) != 12 {
		return Verified{}, failure(domain.Unsupported)
	}
	newer, err := Newer(p.Version, current)
	if err != nil || !newer {
		return Verified{}, failure(domain.Conflict)
	}
	published, err := time.Parse(time.RFC3339, p.PublishedAt)
	if err != nil || published.After(now.Add(10*time.Minute)) || published.Before(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)) {
		return Verified{}, failure(domain.InvalidArgument)
	}
	seen := map[string]bool{}
	for _, a := range p.Artifacts {
		pair := string(a.Component) + ":" + string(a.Target)
		if !a.Component.Valid() || !a.Target.Valid() || seen[pair] || a.Name != ArtifactName(a.Component, a.Target) || a.URL != ReleaseURL(p.Version, a.Name) || a.Size <= 0 || a.Size > ArtifactLimit || !digestValid(a.SHA256) {
			return Verified{}, failure(domain.InvalidArgument)
		}
		seen[pair] = true
	}
	sum := sha256.Sum256(raw)
	return Verified{p, hex.EncodeToString(sum[:]), append([]byte(nil), raw...)}, nil
}
func digestValidSource(s string) bool {
	b, e := hex.DecodeString(s)
	return e == nil && len(b) == 20 && hex.EncodeToString(b) == s
}
func (v Verified) Artifact(c Component, t Target) (Artifact, error) {
	for _, a := range v.Payload.Artifacts {
		if a.Component == c && a.Target == t {
			return a, nil
		}
	}
	return Artifact{}, failure(domain.Unsupported)
}
func (a Artifact) String() string { return fmt.Sprintf("%s/%s", a.Component, a.Target) }

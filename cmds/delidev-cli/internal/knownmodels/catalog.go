// SPDX-License-Identifier: Apache-2.0
// Package knownmodels owns advisory subscription metadata, without account or
// model-resource authority. Only the reviewed repository main catalog is read.
package knownmodels

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxBytes = 1 << 20
const CatalogURL = "https://raw.githubusercontent.com/delinoio/oss/main/cmds/delidev-cli/internal/knownmodels/catalog.json"

//go:embed catalog.json
var bundled embed.FS

type Source uint8

const (
	Bundled Source = iota + 1
	Cache
	Online
)

type Model struct {
	NativeID              string   `json:"native_id"`
	DisplayName           string   `json:"display_name"`
	Order                 uint32   `json:"order"`
	SourceKeys            []string `json:"source_keys"`
	MinimumHarnessVersion string   `json:"minimum_harness_version,omitempty"`
	RetirementDate        string   `json:"retirement_date,omitempty"`
}
type Provenance struct {
	Key      string `json:"key"`
	URL      string `json:"url"`
	Revision string `json:"revision"`
	SHA256   string `json:"sha256"`
}
type Service struct {
	Service string  `json:"service"`
	Models  []Model `json:"models"`
}
type Catalog struct {
	SchemaVersion  int          `json:"schema_version"`
	CatalogVersion string       `json:"catalog_version"`
	UpdatedAt      string       `json:"updated_at"`
	Sources        []Provenance `json:"sources"`
	Services       []Service    `json:"services"`
}

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._:-]{0,255}$`)
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var keyPattern = regexp.MustCompile(`^[a-z-]+$`)
var ErrInvalid = errors.New("invalid known subscription model catalog")
var sourceHosts = map[string]map[string]bool{
	"codex":             {"github.com": true, "raw.githubusercontent.com": true},
	"openai-retirement": {"learn.chatgpt.com": true},
	"claude-code":       {"code.claude.com": true},
	"claude-models":     {"platform.claude.com": true},
	"grok-build":        {"docs.x.ai": true},
}

// Strict JSON rejects duplicate keys as well as unknown fields and trailing
// documents. A partial or ambiguous catalog can never replace a valid one.
func strictJSON(raw []byte, target any) error {
	if len(raw) == 0 || len(raw) > MaxBytes || !utf8.Valid(raw) {
		return ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	var walk func(int) error
	walk = func(depth int) error {
		if depth > 32 {
			return ErrInvalid
		}
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		delimiter, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delimiter {
		case '{':
			seen := map[string]bool{}
			for decoder.More() {
				token, err := decoder.Token()
				if err != nil {
					return err
				}
				key, ok := token.(string)
				if !ok || seen[key] {
					return ErrInvalid
				}
				seen[key] = true
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for decoder.More() {
				if err := walk(depth + 1); err != nil {
					return err
				}
			}
		default:
			return ErrInvalid
		}
		_, err = decoder.Token()
		return err
	}
	if err := walk(0); err != nil {
		return ErrInvalid
	}
	if _, err := decoder.Token(); err != io.EOF {
		return ErrInvalid
	}
	decoder = json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return ErrInvalid
	}
	return nil
}
func validDate(value string) bool {
	parsed, err := time.Parse(time.DateOnly, value)
	return err == nil && parsed.Format(time.DateOnly) == value
}
func exactShape(value map[string]any, required, optional []string) bool {
	for _, key := range required {
		if item, ok := value[key]; !ok || item == nil {
			return false
		}
	}
	for key, item := range value {
		allowed := false
		for _, field := range append(append([]string(nil), required...), optional...) {
			if key == field {
				allowed = true
				break
			}
		}
		if !allowed || item == nil {
			return false
		}
	}
	return true
}
func Decode(raw []byte) (Catalog, error) {
	var catalog Catalog
	if err := strictJSON(raw, &catalog); err != nil {
		return Catalog{}, err
	}
	if catalog.SchemaVersion != 1 || !validDate(catalog.UpdatedAt) || len(catalog.CatalogVersion) != 71 || catalog.CatalogVersion[:7] != "sha256:" || !digestPattern.MatchString(catalog.CatalogVersion[7:]) || len(catalog.Sources) == 0 || len(catalog.Sources) > 10 || len(catalog.Services) != 3 {
		return Catalog{}, ErrInvalid
	}
	keys := map[string]bool{}
	for _, source := range catalog.Sources {
		parsed, err := url.Parse(source.URL)
		if err != nil || parsed.Scheme != "https" || parsed.User != nil || parsed.Port() != "" {
			return Catalog{}, ErrInvalid
		}
		if hosts, ok := sourceHosts[source.Key]; !ok || !hosts[parsed.Hostname()] {
			return Catalog{}, ErrInvalid
		}
		if !keyPattern.MatchString(source.Key) || keys[source.Key] || source.Revision == "" || len(source.Revision) > 128 || !digestPattern.MatchString(source.SHA256) {
			return Catalog{}, ErrInvalid
		}
		keys[source.Key] = true
	}
	if len(keys) != len(sourceHosts) {
		return Catalog{}, ErrInvalid
	}
	for index, service := range catalog.Services {
		if service.Service != []string{"chatgpt", "claude", "grok"}[index] || len(service.Models) == 0 || len(service.Models) > 200 {
			return Catalog{}, ErrInvalid
		}
		ids := map[string]bool{}
		for order, model := range service.Models {
			if !idPattern.MatchString(model.NativeID) || ids[model.NativeID] || strings.TrimSpace(model.DisplayName) == "" || len(model.DisplayName) > 256 || int(model.Order) != order || len(model.SourceKeys) == 0 || len(model.MinimumHarnessVersion) > 32 || model.MinimumHarnessVersion != "" && !versionPattern.MatchString(model.MinimumHarnessVersion) || model.RetirementDate != "" && !validDate(model.RetirementDate) {
				return Catalog{}, ErrInvalid
			}
			for _, ch := range model.DisplayName {
				if ch < 32 || ch == 127 {
					return Catalog{}, ErrInvalid
				}
			}
			seen := map[string]bool{}
			for _, key := range model.SourceKeys {
				if !keys[key] || seen[key] {
					return Catalog{}, ErrInvalid
				}
				seen[key] = true
			}
			ids[model.NativeID] = true
		}
	}
	// Hash the original service JSON through sorted object keys, shared with the
	// collector. Unknown/missing values cannot evade its semantic version.
	var original map[string]any
	_ = json.Unmarshal(raw, &original)
	if !exactShape(original, []string{"schema_version", "catalog_version", "updated_at", "services", "sources"}, nil) {
		return Catalog{}, ErrInvalid
	}
	inventory, ok := original["services"].([]any)
	if !ok {
		return Catalog{}, ErrInvalid
	}
	for _, entry := range inventory {
		service, ok := entry.(map[string]any)
		if !ok || !exactShape(service, []string{"service", "models"}, nil) {
			return Catalog{}, ErrInvalid
		}
		models, ok := service["models"].([]any)
		if !ok {
			return Catalog{}, ErrInvalid
		}
		for _, row := range models {
			model, ok := row.(map[string]any)
			if !ok || !exactShape(model, []string{"native_id", "display_name", "order", "source_keys"}, []string{"minimum_harness_version", "retirement_date"}) {
				return Catalog{}, ErrInvalid
			}
			for _, key := range []string{"minimum_harness_version", "retirement_date"} {
				if item, present := model[key]; present && item == "" {
					return Catalog{}, ErrInvalid
				}
			}
		}
	}
	provenance, ok := original["sources"].([]any)
	if !ok {
		return Catalog{}, ErrInvalid
	}
	for _, entry := range provenance {
		source, ok := entry.(map[string]any)
		if !ok || !exactShape(source, []string{"key", "url", "revision", "sha256"}, nil) {
			return Catalog{}, ErrInvalid
		}
	}
	canonical, _ := json.Marshal(inventory)
	digest := sha256.Sum256(canonical)
	if catalog.CatalogVersion != "sha256:"+hex.EncodeToString(digest[:]) {
		return Catalog{}, ErrInvalid
	}
	return catalog, nil
}
func BundledCatalog() Catalog {
	raw, err := bundled.ReadFile("catalog.json")
	if err != nil {
		panic(err)
	}
	value, err := Decode(raw)
	if err != nil {
		panic(fmt.Errorf("embedded catalog: %w", err))
	}
	return value
}

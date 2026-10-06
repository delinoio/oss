// SPDX-License-Identifier: Apache-2.0
package security

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"strings"
)

// ProtectedJSON holds transient protected forms. It checks original bytes and
// decoded JSON names/values before a native frame can enter a durable outbox.
// This is finite literal/JSON/Base64 reflection protection, not a guarantee
// against arbitrary native transformations or malicious covert encodings.
type ProtectedJSON struct{ forms []string }

func NewProtectedJSON(values []string) ProtectedJSON {
	guard := ProtectedJSON{}
	for _, value := range values {
		if value != "" {
			guard.forms = append(guard.forms, value, base64.StdEncoding.EncodeToString([]byte(value)), base64.RawStdEncoding.EncodeToString([]byte(value)), base64.URLEncoding.EncodeToString([]byte(value)), base64.RawURLEncoding.EncodeToString([]byte(value)))
		}
	}
	return guard
}

func (g ProtectedJSON) Safe(raw []byte) bool {
	if len(g.forms) == 0 {
		return true
	}
	if !json.Valid(raw) {
		return false
	}
	contains := func(value string) bool {
		for _, secret := range g.forms {
			if strings.Contains(value, secret) {
				return true
			}
		}
		return false
	}
	if contains(string(raw)) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return true
		}
		if err != nil {
			return false
		}
		if value, ok := token.(string); ok && contains(value) {
			return false
		}
	}
}

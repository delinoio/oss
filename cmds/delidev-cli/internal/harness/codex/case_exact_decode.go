// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"encoding/json"
	"reflect"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

var jsonUnmarshalerType = reflect.TypeOf((*json.Unmarshaler)(nil)).Elem()

// decodeCaseExact applies the shared strict decode (unknown and duplicate keys
// rejected) and then requires every object key that targets a typed struct
// field to use the field's canonical JSON name. encoding/json matches struct
// tags case-insensitively, so `Status` would otherwise be accepted for
// `status`. Raw-message, map and interface fields remain opaque.
func decodeCaseExact(raw []byte, target any) error {
	if err := domain.Decode(raw, target); err != nil {
		return err
	}
	if !canonicalKeys(raw, reflect.TypeOf(target)) {
		return incompatible()
	}
	return nil
}

// requireExactKeys reports whether the raw JSON object contains exactly the
// listed keys using case-sensitive comparison.
func requireExactKeys(raw []byte, keys ...string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil || len(object) != len(keys) {
		return false
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			return false
		}
	}
	return true
}

func canonicalKeys(raw []byte, t reflect.Type) bool {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if string(raw) == "null" {
		return true
	}
	switch t.Kind() {
	case reflect.Struct:
		if reflect.PointerTo(t).Implements(jsonUnmarshalerType) {
			return true
		}
		var object map[string]json.RawMessage
		if json.Unmarshal(raw, &object) != nil {
			return false
		}
		fields := map[string]reflect.Type{}
		collectJSONFields(t, fields)
		for key, value := range object {
			field, ok := fields[key]
			if !ok || !canonicalKeys(value, field) {
				return false
			}
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return true
		}
		var items []json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			return false
		}
		for _, item := range items {
			if !canonicalKeys(item, t.Elem()) {
				return false
			}
		}
	}
	return true
}

func collectJSONFields(t reflect.Type, fields map[string]reflect.Type) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		tag, tagged := field.Tag.Lookup("json")
		name, _, _ := strings.Cut(tag, ",")
		if name == "-" && !strings.Contains(tag, ",") {
			continue
		}
		if field.Anonymous && name == "" {
			embedded := field.Type
			for embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				collectJSONFields(embedded, fields)
				continue
			}
		}
		if !field.IsExported() {
			continue
		}
		if !tagged || name == "" {
			name = field.Name
		}
		fields[name] = field.Type
	}
}

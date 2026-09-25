package grok

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

var rawType = reflect.TypeOf(json.RawMessage{})

// Native facts use exact keys, including inside nested objects and arrays.
// encoding/json otherwise accepts case aliases and null scalar values. All
// fields are required unless explicitly tagged omitempty; nullable unions use
// RawMessage and receive separate validation at their profile boundary.
func decode(raw []byte, target any) error {
	if domain.Decode(raw, target) != nil || !shape(raw, reflect.TypeOf(target).Elem()) {
		return incompatible()
	}
	return nil
}

func shape(raw []byte, typ reflect.Type) bool {
	if typ == rawType {
		return len(raw) != 0
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return false
	}
	switch typ.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if json.Unmarshal(raw, &fields) != nil || fields == nil {
			return false
		}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			name, options, _ := strings.Cut(field.Tag.Get("json"), ",")
			value, exists := fields[name]
			if !exists {
				if options == "omitempty" {
					continue
				}
				return false
			}
			if !shape(value, field.Type) {
				return false
			}
			delete(fields, name)
		}
		return len(fields) == 0
	case reflect.Slice:
		var values []json.RawMessage
		if json.Unmarshal(raw, &values) != nil || values == nil {
			return false
		}
		for _, value := range values {
			if !shape(value, typ.Elem()) {
				return false
			}
		}
	case reflect.Pointer:
		return shape(raw, typ.Elem())
	}
	return true
}

func isNull(raw json.RawMessage) bool { return bytes.Equal(bytes.TrimSpace(raw), []byte("null")) }

func emptyArray(raw json.RawMessage) bool {
	var list []json.RawMessage
	return json.Unmarshal(raw, &list) == nil && list != nil && len(list) == 0
}

func text(value string, limit int) bool {
	return domain.Text(value, "native descriptor", limit, true) == nil
}

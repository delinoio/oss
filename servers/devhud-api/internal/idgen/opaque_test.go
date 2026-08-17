package idgen

import (
	"encoding/base64"
	"testing"
)

func TestOpaqueIDsCarry256BitsAndDoNotCollide(t *testing.T) {
	seen := make(map[string]struct{}, 4096)
	for range 4096 {
		value, err := Opaque()
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil || len(value) != 43 || len(decoded) != OpaqueIDBytes {
			t.Fatalf("opaque ID shape = %q (%d bytes), decode error %v", value, len(decoded), err)
		}
		if _, exists := seen[value]; exists {
			t.Fatalf("opaque ID collision: %q", value)
		}
		seen[value] = struct{}{}
	}
}

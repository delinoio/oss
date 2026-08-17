package rpc

import (
	"testing"
)

func TestValidateCanonicalJSON(t *testing.T) {
	for _, value := range [][]byte{
		[]byte(`{"language":"en","theme":"system"}`),
		[]byte(`{}`),
		[]byte(`[1,true,null,"value"]`),
	} {
		if err := validateCanonicalJSON(value); err != nil {
			t.Errorf("validateCanonicalJSON(%s): %v", value, err)
		}
	}
	for name, value := range map[string][]byte{
		"whitespace":     []byte(`{ "a": 1 }`),
		"property order": []byte(`{"z":1,"a":2}`),
		"bom":            append([]byte{0xef, 0xbb, 0xbf}, []byte(`{}`)...),
		"invalid utf8":   {0xff},
		"too large":      append([]byte(`"`), append(make([]byte, 1_048_576), '"')...),
	} {
		t.Run(name, func(t *testing.T) {
			if err := validateCanonicalJSON(value); err == nil {
				t.Fatal("validation succeeded")
			}
		})
	}
}

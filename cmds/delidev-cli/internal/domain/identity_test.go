package domain

import (
	"strings"
	"testing"
)

func TestCanonicalV7(t *testing.T) {
	good := NewID()
	if err := good.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []ID{"", ID(strings.ToUpper(string(good))), "00000000-0000-4000-8000-000000000001", ID("{" + string(good) + "}"), "01890abc-abcd-7000-0000-0123456789ab"} {
		if bad == good {
			continue
		}
		if err := bad.Validate(); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}
func TestStrictJSON(t *testing.T) {
	var target struct {
		Name string `json:"name"`
	}
	for _, input := range []string{`{"name":"ok","secret":"ignored?"}`, `{"name":2}`, `{"name":"first","name":"last"}`, `{"name":"ok"} {}`, string([]byte{0xff})} {
		if err := Decode([]byte(input), &target); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
	if err := Decode([]byte(`{"name":"ok"}`), &target); err != nil {
		t.Fatal(err)
	}
}

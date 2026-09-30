package userservice

import (
	"bytes"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestTaskIdentityAndEntireCanonicalDefinition(t *testing.T) {
	s := Spec{Platform: "windows", Kind: Worker, ID: domain.NewID(), Name: "io.delino.delidev.fixture", User: "S-1-5-21-123", Binary: `C:\Program Files\DeliDev\delidev.exe`, Root: `C:\Users\fixture\private`, DefinitionPath: `\io.delino.delidev.fixture`}
	d := definition(s)
	if validateTask(s, d) != nil {
		t.Fatal("owned task invalid")
	}
	for _, pair := range [][2]string{{"LeastPrivilege", "HighestAvailable"}, {"InteractiveToken", "Password"}, {"IgnoreNew", "Parallel"}, {"S-1-5-21-123", "S-1-5-21-999"}, {"PT0S", "PT1H"}} {
		if validateTask(s, []byte(strings.ReplaceAll(string(d), pair[0], pair[1]))) == nil {
			t.Fatal("foreign task accepted", pair)
		}
	}
	expected, e := taskCanonical(d)
	enabled, e2 := taskCanonical([]byte(strings.Replace(string(d), "<Enabled>false</Enabled>", "<Enabled>true</Enabled>", 1)))
	if e != nil || e2 != nil || !bytes.Equal(expected, enabled) {
		t.Fatal("controlled enable invalidated ownership")
	}
	changed, e := taskCanonical([]byte(strings.Replace(string(d), "<Priority>7</Priority>", "<Priority>9</Priority>", 1)))
	if e != nil || bytes.Equal(expected, changed) {
		t.Fatal("unselected task state was ignored")
	}
}

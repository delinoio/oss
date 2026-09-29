package userservice

import (
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestLaunchdCachedDefinitionIsVerifiedEvenWithoutPID(t *testing.T) {
	s := Spec{Kind: Server, ID: domain.NewID(), Name: "io.delino.delidev.fixture", Binary: "/Applications/DeliDev/delidev", Root: "/private/fixture", DefinitionPath: "/Users/fixture/Library/LaunchAgents/service.plist"}
	text := "service = {\n path = " + s.DefinitionPath + "\n program = " + s.Binary + "\n arguments = {\n " + strings.Join(append([]string{s.Binary}, s.args()...), "\n ") + "\n }\n}\n"
	if pid, e := launchInspection([]byte(text), s); e != nil || pid != 0 {
		t.Fatal("inactive owned definition unavailable", pid, e)
	}
	for _, bad := range []string{strings.Replace(text, s.Root, "/foreign", 1), strings.Replace(text, "program = "+s.Binary, "program = /foreign", 1), strings.Replace(text, "arguments = {", "arguments = changed {", 1), text + "pid = 10\npid = 11\n"} {
		if _, e := launchInspection([]byte(bad), s); e == nil {
			t.Fatal("foreign/ambiguous loaded registration accepted")
		}
	}
}

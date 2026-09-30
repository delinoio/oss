package userservice

import (
	"encoding/xml"
	"io"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestDefinitionsContainOnlyPinnedCurrentUserReferences(t *testing.T) {
	s := Spec{ID: domain.NewID(), Name: "io.delino.delidev.worker.fixture", Kind: Worker, User: "S-1-5-21-123", Binary: `C:\Program Files\DeliDev\delidev.exe`, Root: `C:\Users\user\private <scope>`, DefinitionPath: `\io.delino.delidev.worker.fixture`}
	for _, platform := range []string{"darwin", "linux", "windows"} {
		s.Platform = platform
		d := string(definition(s))
		for _, forbidden := range []string{"Token=", "Password", "sudo ", "--token-stdin", "Environment=", "linger"} {
			if strings.Contains(d, forbidden) {
				t.Fatalf("%s definition contained %s", platform, forbidden)
			}
		}
		if !strings.Contains(d, string(s.ID)) {
			t.Fatal("missing exact service identity")
		}
		if platform != "linux" {
			decoder := xml.NewDecoder(strings.NewReader(d))
			for {
				_, err := decoder.Token()
				if err != nil {
					if err != io.EOF {
						t.Fatal(err)
					}
					break
				}
			}
		}

	}
	if quoteUnit("a % $ \\") != `"a %% $$ \\"` {
		t.Fatal("unsafe systemd quoting")
	}
}

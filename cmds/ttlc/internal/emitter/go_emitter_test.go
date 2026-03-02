package emitter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/ttlc/internal/sema"
)

func TestEmitGoDeterministic(t *testing.T) {
	outDir := t.TempDir()
	types := []sema.TypeDecl{
		{
			Name: "Artifact",
			Fields: []sema.TypeField{
				{Name: "Path", Type: "string"},
			},
		},
	}
	tasks := []sema.Task{
		{
			ID: "Build",
			Params: []sema.TaskParam{
				{Name: "target", Type: "string"},
			},
			ReturnType: "Vc[Artifact]",
		},
	}

	first, err := EmitGo("build", types, tasks, outDir)
	if err != nil {
		t.Fatalf("emit first: %v", err)
	}
	second, err := EmitGo("build", types, tasks, outDir)
	if err != nil {
		t.Fatalf("emit second: %v", err)
	}
	if string(first.Content) != string(second.Content) {
		t.Fatal("expected deterministic emitter output")
	}

	payload, err := os.ReadFile(first.Path)
	if err != nil {
		t.Fatalf("read emitted file: %v", err)
	}
	if !strings.Contains(string(payload), "func Build(target string) Vc[Artifact]") {
		t.Fatalf("unexpected generated content: %s", string(payload))
	}
	if !strings.Contains(string(payload), "type Artifact struct") {
		t.Fatalf("expected generated type declaration, got=%s", string(payload))
	}
	if filepath.Base(first.Path) != "build_ttl_gen.go" {
		t.Fatalf("unexpected generated file name: %s", filepath.Base(first.Path))
	}
}

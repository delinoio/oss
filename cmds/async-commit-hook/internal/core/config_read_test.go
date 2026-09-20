package core

import (
	"errors"
	"io"
	"strings"
	"testing"
)

type configStream struct{ read int }

func (s *configStream) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = ' '
	}
	s.read += len(p)
	return len(p), nil
}

func TestProjectReaderBoundsBeforeParsing(t *testing.T) {
	stream := &configStream{}
	_, err := ReadProject(stream)
	if err == nil || !strings.Contains(err.Error(), "exceeds 1 MiB") || stream.read != maxProjectConfigBytes+1 {
		t.Fatalf("unbounded or incorrectly classified read: bytes=%d error=%v", stream.read, err)
	}
	base := "version=1\n[checks.test]\ncommand='unused'\n#"
	exact := base + strings.Repeat("x", maxProjectConfigBytes-len(base))
	if _, err := ReadProject(strings.NewReader(exact)); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadProject(io.MultiReader(strings.NewReader(exact), strings.NewReader("x"))); err == nil {
		t.Fatal("overflow accepted")
	}
	want := errors.New("injected read failure")
	if _, err := ReadProject(failingConfigReader{want}); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

type failingConfigReader struct{ err error }

func (r failingConfigReader) Read([]byte) (int, error) { return 0, r.err }

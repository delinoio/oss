package runmoor

import (
	"archive/zip"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsPoolLabels(t *testing.T) {
	for _, b := range []Backend{Host, Docker} {
		name, labels, e := initialPoolLabelsForOS(b, "amd64", "windows")
		if e != nil || name == "" || labels[1] != "Windows" || labels[2] != "x64" {
			t.Fatalf("%s: %v %v", b, labels, e)
		}
	}
	if _, _, e := initialPoolLabelsForOS(Host, "arm64", "windows"); e == nil {
		t.Fatal("ARM64 accepted")
	}
}
func TestLocalDockerEndpoints(t *testing.T) {
	for _, tc := range []struct {
		endpoint, goos string
		valid          bool
	}{{"npipe:////./pipe/docker_engine", "windows", true}, {"npipe:////server/pipe/docker_engine", "windows", false}, {"npipe:////./pipe/../other", "windows", false}, {"tcp://localhost:2375", "windows", false}, {"unix:///var/run/docker.sock", "windows", false}, {"unix:///var/run/docker.sock", "linux", true}} {
		if validLocalDockerEndpoint(tc.endpoint, tc.goos) != tc.valid {
			t.Errorf("%s %s", tc.goos, tc.endpoint)
		}
	}
}
func TestWindowsRunnerZIPRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"../escape", "C:/escape", "a/b:stream", "CON.txt", "a./b", "a /b", `a\b`, "bin/runner.listener.EXE", "bin/Runner.Listener.exe/child", "a\x00b"} {
		t.Run(name, func(t *testing.T) { testWindowsZIP(t, []string{"bin/Runner.Listener.exe", "run.cmd", name}, false) })
	}
	testWindowsZIP(t, []string{"bin/Runner.Listener.exe", "run.cmd", "externals/node.exe"}, true)
}
func testWindowsZIP(t *testing.T, names []string, valid bool) {
	t.Helper()
	dir := t.TempDir()
	in, e := os.Create(filepath.Join(dir, "in.zip"))
	if e != nil {
		t.Fatal(e)
	}
	defer in.Close()
	z := zip.NewWriter(in)
	for _, name := range names {
		f, e := z.Create(name)
		if e != nil {
			t.Fatal(e)
		}
		_, _ = f.Write([]byte("fixture"))
	}
	if e = z.Close(); e != nil {
		t.Fatal(e)
	}
	out, e := os.Create(filepath.Join(dir, "out.tar"))
	if e != nil {
		t.Fatal(e)
	}
	defer out.Close()
	e = repackRunnerZip(context.Background(), in, out)
	if (e == nil) != valid {
		t.Fatalf("valid=%v error=%v", valid, e)
	}
}

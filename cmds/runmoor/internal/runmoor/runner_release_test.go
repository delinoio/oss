package runmoor

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func runnerArchiveFixture(t *testing.T, extra *tar.Header) []byte {
	t.Helper()
	var body bytes.Buffer
	gz := gzip.NewWriter(&body)
	tw := tar.NewWriter(gz)
	for _, file := range []struct{ name, body string }{{"bin/Runner.Listener", "#!/bin/sh\necho 2.338.0\n"}, {"run.sh", "#!/bin/sh\nexit 0\n"}} {
		if err := tw.WriteHeader(&tar.Header{Name: file.name, Mode: 0755, Typeflag: tar.TypeReg, Size: int64(len(file.body))}); err != nil {
			t.Fatal(err)
		}
		tw.Write([]byte(file.body))
	}
	if extra != nil {
		tw.WriteHeader(extra)
	}
	tw.Close()
	gz.Close()
	return body.Bytes()
}
func archiveClient(body []byte) *http.Client {
	return &http.Client{Transport: runnerReleaseTransport(func(req *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body))}, nil
	})}
}
func fixtureRelease(t *testing.T, backend Backend, arch string) (RunnerRelease, []byte) {
	body := runnerArchiveFixture(t, nil)
	hash := sha256.Sum256(body)
	platform := "linux"
	if backend == Tart {
		platform = "osx"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	name := "actions-runner-" + platform + "-" + arch + "-2.338.0.tar.gz"
	return RunnerRelease{Tag: "v2.338.0", Published: time.Now(), Assets: []RunnerAsset{{Name: name, URL: "https://github.com/actions/runner/releases/download/v2.338.0/" + name, Digest: "sha256:" + hex.EncodeToString(hash[:]), Size: int64(len(body))}}}, body
}
func TestRunnerArchiveVerificationAndSafeRepack(t *testing.T) {
	c := fixtureConfig(t)
	release, body := fixtureRelease(t, Docker, "arm64")
	asset, err := runnerArchiveAsset(release, Docker, "arm64")
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(c.Storage.Data, newID())
	archive, err := downloadRunnerArchive(context.Background(), archiveClient(body), asset, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = validatedRunnerTar(context.Background(), archive, filepath.Join(dir, "verified.tar")); err != nil {
		t.Fatal(err)
	}
	linked := runnerArchiveFixture(t, &tar.Header{Name: "bin/listener", Typeflag: tar.TypeSymlink, Linkname: "Runner.Listener"})
	linkedFile := filepath.Join(dir, "linked.tar.gz")
	if err = os.WriteFile(linkedFile, linked, 0600); err != nil {
		t.Fatal(err)
	}
	if err = validatedRunnerTar(context.Background(), linkedFile, filepath.Join(dir, "linked.tar")); err != nil {
		t.Fatal(err)
	}
	asset.Digest = "sha256:" + strings.Repeat("a", 64)
	_, err = downloadRunnerArchive(context.Background(), archiveClient(body), asset, filepath.Join(c.Storage.Data, newID()))
	requireCode(t, err, ErrImage)
	for _, h := range []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}, {Name: "/absolute", Typeflag: tar.TypeReg}, {Name: "bin/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}, {Name: "bin", Typeflag: tar.TypeSymlink, Linkname: "run.sh"}, {Name: "missing", Typeflag: tar.TypeSymlink, Linkname: "absent"}, {Name: "run.sh", Typeflag: tar.TypeReg}, {Name: "hard", Typeflag: tar.TypeLink, Linkname: "/outside"}, {Name: "fifo", Typeflag: tar.TypeFifo}} {
		b := runnerArchiveFixture(t, h)
		input := filepath.Join(c.Storage.Data, newID()+".gz")
		os.WriteFile(input, b, 0600)
		output := input + ".tar"
		err = validatedRunnerTar(context.Background(), input, output)
		requireCode(t, err, ErrImage)
		if _, err = os.Stat(output); !os.IsNotExist(err) {
			t.Fatal("invalid archive published a partial output")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = validatedRunnerTar(ctx, archive, filepath.Join(dir, "cancelled.tar")); err == nil {
		t.Fatal("ignored cancellation")
	}
}
func TestRunnerReleaseAssetRejectsWrongAuthorityAndMissingChecksum(t *testing.T) {
	for _, mutate := range []func(*RunnerAsset){func(a *RunnerAsset) { a.URL = "https://example.com/runner.tar.gz" }, func(a *RunnerAsset) { a.Digest = "" }, func(a *RunnerAsset) { a.Size = 2 << 30 }, func(a *RunnerAsset) { a.URL = strings.Replace(a.URL, "https:", "http:", 1) }} {
		release, _ := fixtureRelease(t, Tart, "arm64")
		mutate(&release.Assets[0])
		if _, err := runnerArchiveAsset(release, Tart, "arm64"); err == nil {
			t.Fatal("accepted unverified asset")
		}
	}
}

func TestOfficialRunnerArchive(t *testing.T) {
	if os.Getenv("RUNMOOR_RUNNER_DOWNLOAD_TEST") != "1" {
		t.Skip("set RUNMOOR_RUNNER_DOWNLOAD_TEST=1 for an official runner download without registration")
	}
	c := fixtureConfig(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	client := defaultReleaseClient()
	releases, err := fetchRunnerReleases(ctx, client)
	if err != nil || len(releases) == 0 {
		t.Fatalf("release lookup: %v", err)
	}
	backend := Docker
	if runtime.GOOS == "darwin" {
		backend = Tart
	}
	asset, err := runnerArchiveAsset(releases[0], backend, runtime.GOARCH)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(c.Storage.Data, newID())
	archive, err := downloadRunnerArchive(ctx, client, asset, dir)
	if err != nil {
		t.Fatal(err)
	}
	if err = validatedRunnerTar(ctx, archive, filepath.Join(dir, "verified.tar")); err != nil {
		f, _ := os.Open(archive)
		defer f.Close()
		gz, _ := gzip.NewReader(f)
		defer gz.Close()
		tr := tar.NewReader(gz)
		for {
			h, e := tr.Next()
			if e != nil {
				break
			}
			if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir || strings.Contains(h.Name, "../") {
				t.Logf("Official archive entry: type=%d name=%q link=%q", h.Typeflag, h.Name, h.Linkname)
			}
		}
		t.Fatal(err)
	}
	t.Logf("Verified official %s archive, %d bytes", asset.Name, asset.Size)
}

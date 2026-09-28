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
	asset.Digest = "sha256:" + strings.Repeat("a", 64)
	_, err = downloadRunnerArchive(context.Background(), archiveClient(body), asset, filepath.Join(c.Storage.Data, newID()))
	requireCode(t, err, ErrImage)
	for _, h := range []*tar.Header{{Name: "../escape", Typeflag: tar.TypeReg}, {Name: "/absolute", Typeflag: tar.TypeReg}, {Name: "bin/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside"}, {Name: "hard", Typeflag: tar.TypeLink, Linkname: "/outside"}, {Name: "fifo", Typeflag: tar.TypeFifo}} {
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

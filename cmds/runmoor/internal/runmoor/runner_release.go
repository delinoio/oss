package runmoor

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

type RunnerAsset struct {
	Name   string `json:"name"`
	URL    string `json:"browser_download_url"`
	Digest string `json:"digest"`
	Size   int64  `json:"size"`
}
type RunnerRelease struct {
	Tag        string        `json:"tag_name"`
	Published  time.Time     `json:"published_at"`
	Draft      bool          `json:"draft"`
	Prerelease bool          `json:"prerelease"`
	Assets     []RunnerAsset `json:"assets"`
}

func (r RunnerRelease) Version() string { return strings.TrimPrefix(r.Tag, "v") }
func releaseProblem() *Problem {
	return problem(ErrRetry, "Official runner release metadata is unavailable or invalid.", "Runmoor will retry automatically; inspect runner update status.")
}
func fetchRunnerReleases(ctx context.Context, client *http.Client) ([]RunnerRelease, error) {
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "https://api.github.com/repos/actions/runner/releases?per_page=100", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "runmoor/"+Version)
	resp, err := client.Do(req)
	if err != nil {
		return nil, problem(ErrRetry, "Runner release freshness is unknown while GitHub is unavailable.", "Retry doctor or runner update after connectivity recovers.")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		p := problem(ErrRetry, "Runner release freshness could not be checked.", "Retry after GitHub rate limits or connectivity recover.")
		p.HTTPStatus = resp.StatusCode
		if seconds, e := strconv.Atoi(resp.Header.Get("Retry-After")); e == nil && seconds > 0 && seconds <= 86400 {
			p.RetryAt = time.Now().Add(time.Duration(seconds) * time.Second)
		}
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			if reset, e := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); e == nil {
				at := time.Unix(reset, 0)
				if at.After(time.Now()) && at.Before(time.Now().Add(24*time.Hour)) {
					p.RetryAt = at
				}
			}
		}
		return nil, p
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, runnerReleaseResponseLimit+1))
	if len(body) > runnerReleaseResponseLimit {
		return nil, problem(ErrRetry, "Runner release metadata exceeds the 8 MiB response limit.", "Check official runner releases manually and retry doctor after the metadata service recovers.")
	}
	if err != nil {
		return nil, problem(ErrRetry, "Runner release metadata could not be read.", "Retry after connectivity recovers.")
	}
	var releases []RunnerRelease
	if json.Unmarshal(body, &releases) != nil {
		return nil, problem(ErrRetry, "Runner release metadata is invalid.", "Retry the official release metadata request.")
	}
	var stable []RunnerRelease
	for _, r := range releases {
		if r.Draft || r.Prerelease {
			continue
		}
		if !versionPattern.MatchString(r.Version()) || r.Published.IsZero() {
			return nil, releaseProblem()
		}
		stable = append(stable, r)
	}
	sort.SliceStable(stable, func(i, j int) bool { return newerVersion(stable[i].Version(), stable[j].Version()) })
	return stable, nil
}
func newerVersion(a, b string) bool {
	aa, bb := strings.Split(a, "."), strings.Split(b, ".")
	if len(aa) != 3 || len(bb) != 3 {
		return false
	}
	for i := range aa {
		av, ea := strconv.ParseUint(aa[i], 10, 64)
		bv, eb := strconv.ParseUint(bb[i], 10, 64)
		if ea != nil || eb != nil {
			return false
		}
		if av != bv {
			return av > bv
		}
	}
	return false
}
func updateDeadline(releases []RunnerRelease, version string) time.Time {
	var first time.Time
	for _, r := range releases {
		if newerVersion(r.Version(), version) && (first.IsZero() || r.Published.Before(first)) {
			first = r.Published
		}
	}
	if first.IsZero() {
		return time.Time{}
	}
	return first.Add(30 * 24 * time.Hour)
}
func runnerArchiveAsset(release RunnerRelease, backend Backend, arch string) (RunnerAsset, error) {
	osName := "linux"
	if backend == Tart {
		osName = "osx"
	}
	if arch == "amd64" {
		arch = "x64"
	}
	name := "actions-runner-" + osName + "-" + arch + "-" + release.Version() + ".tar.gz"
	for _, asset := range release.Assets {
		if asset.Name != name {
			continue
		}
		u, err := url.Parse(asset.URL)
		expected := "/actions/runner/releases/download/" + release.Tag + "/" + name
		digest, decodeErr := hex.DecodeString(strings.TrimPrefix(asset.Digest, "sha256:"))
		if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.Path != expected || u.RawQuery != "" || u.Fragment != "" || asset.Size <= 0 || asset.Size > 1<<30 || !strings.HasPrefix(asset.Digest, "sha256:") || decodeErr != nil || len(digest) != 32 {
			return RunnerAsset{}, releaseProblem()
		}
		return asset, nil
	}
	return RunnerAsset{}, problem(ErrRetry, "The selected runner release has no compatible verified archive.", "Wait for the official release assets to finish publishing; Runmoor will retry.")
}

// Archives stay host-side until checksum and extraction validation finish. No
// authenticated API client or management credential is used for asset delivery.
func downloadRunnerArchive(ctx context.Context, client *http.Client, asset RunnerAsset, dir string) (string, error) {
	if err := privateDir(dir); err != nil {
		return "", err
	}
	file := filepath.Join(dir, "runner.tar.gz")
	f, err := openPrivate(file, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return "", err
	}
	good := false
	defer func() {
		f.Close()
		if !good {
			os.Remove(file)
		}
	}()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return "", releaseProblem()
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", releaseProblem()
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", releaseProblem()
	}
	hash := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, hash), &contextReader{ctx, io.LimitReader(resp.Body, asset.Size+1)})
	if err != nil || n != asset.Size || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != asset.Digest {
		return "", problem(ErrImage, "Runner archive size or SHA-256 verification failed.", "The candidate was not activated; retry the official download.")
	}
	if err = f.Sync(); err != nil {
		return "", err
	}
	good = true
	return file, nil
}

// Repack regular files/directories and verified in-tree file symlinks. Links
// are emitted last, so no subsequent extraction can traverse a created link.
// Compressed/uncompressed sizes and entry counts are independently bounded.
func validatedRunnerTar(ctx context.Context, archive string, output string) error {
	in, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer in.Close()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return archiveProblem()
	}
	defer gz.Close()
	reader := tar.NewReader(gz)
	out, err := openPrivate(output, os.O_CREATE|os.O_EXCL|os.O_WRONLY)
	if err != nil {
		return err
	}
	writer := tar.NewWriter(out)
	good := false
	defer func() {
		writer.Close()
		out.Close()
		if !good {
			os.Remove(output)
		}
	}()
	var total int64
	count := 0
	listener, run := false, false
	entries := map[string]byte{}
	var links []*tar.Header
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		h, e := reader.Next()
		if e == io.EOF {
			break
		}
		if e != nil {
			return archiveProblem()
		}
		count++
		total += h.Size
		name := strings.TrimPrefix(h.Name, "./")
		if name == "" || name == "." {
			if h.Typeflag == tar.TypeDir {
				continue
			}
			return archiveProblem()
		}
		if count > 100000 || h.Size < 0 || total > 4<<30 || path.IsAbs(name) || path.Clean(name) != strings.TrimSuffix(name, "/") || strings.ContainsAny(name, "\\\x00\r\n") || name == ".." || strings.HasPrefix(name, "../") {
			return archiveProblem()
		}
		if h.Typeflag != tar.TypeReg && h.Typeflag != tar.TypeDir && h.Typeflag != tar.TypeSymlink {
			return archiveProblem()
		}
		key := strings.TrimSuffix(name, "/")
		if _, exists := entries[key]; exists {
			return archiveProblem()
		}
		entries[key] = h.Typeflag
		// The target is a dedicated clean runner directory, never a host bind mount.
		h.Name = name
		h.Uid = 1001
		h.Gid = 1001
		h.Uname = ""
		h.Gname = ""
		h.PAXRecords = nil
		h.Xattrs = nil
		h.Mode &= 0777
		if h.Typeflag == tar.TypeSymlink {
			copy := *h
			links = append(links, &copy)
			continue
		}
		if e = writer.WriteHeader(h); e != nil {
			return e
		}
		if h.Typeflag == tar.TypeReg {
			if name == "bin/Runner.Listener" {
				listener = true
			}
			if name == "run.sh" {
				run = true
			}
			if _, e = io.Copy(writer, &contextReader{ctx, reader}); e != nil {
				return archiveProblem()
			}
		}
	}
	for _, h := range links {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if h.Size != 0 || h.Linkname == "" || path.IsAbs(h.Linkname) || strings.ContainsAny(h.Linkname, "\\\x00\r\n") {
			return archiveProblem()
		}
		target := path.Clean(path.Join(path.Dir(h.Name), h.Linkname))
		if target == ".." || strings.HasPrefix(target, "../") || entries[target] != tar.TypeReg {
			return archiveProblem()
		}
		for name := range entries {
			if strings.HasPrefix(name, h.Name+"/") {
				return archiveProblem()
			}
		}
		if err = writer.WriteHeader(h); err != nil {
			return err
		}
	}
	if !listener || !run {
		return archiveProblem()
	}
	if err = writer.Close(); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	good = true
	return nil
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r *contextReader) Read(b []byte) (int, error) {
	if e := r.ctx.Err(); e != nil {
		return 0, e
	}
	return r.r.Read(b)
}
func archiveProblem() *Problem {
	return problem(ErrImage, "The runner archive contains invalid or unsupported entries.", "Use a complete official runner release; the existing environment was preserved.")
}

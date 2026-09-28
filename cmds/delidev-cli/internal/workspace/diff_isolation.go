package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func diffIsolationUnavailable() error {
	return domain.Fail(domain.Unavailable, "The isolated Git comparison could not be prepared.", "Check temporary storage on the execution machine and retry.")
}

func (g Git) gitAdminPath(ctx context.Context, root, name string) (string, error) {
	raw, err := g.run(ctx, root, "rev-parse", "--path-format=absolute", "--git-path", name)
	if err != nil {
		return "", err
	}
	path := strings.TrimSuffix(strings.TrimSuffix(string(raw), "\n"), "\r")
	if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n\x00") {
		return "", diffIsolationUnavailable()
	}
	return path, nil
}

// An ordinary Git worktree diff can run a clean/process filter after a prior
// attribute check. A private Git admin directory gives filter=unspecified the
// highest precedence for every path, regardless of live attribute or index
// changes. The real index and objects remain read-only inputs; remove this
// isolation only if Git gains a documented per-command filter disable switch.
func (g Git) filterFreeDiff(ctx context.Context, root string, args []string) (patch []byte, returned error) {
	index, err := g.gitAdminPath(ctx, root, "index")
	if err != nil {
		return nil, err
	}
	objects, err := g.gitAdminPath(ctx, root, "objects")
	if err != nil {
		return nil, err
	}
	config, err := g.gitAdminPath(ctx, root, "config")
	if err != nil {
		return nil, err
	}
	worktreeConfig, err := g.gitAdminPath(ctx, root, "config.worktree")
	if err != nil {
		return nil, err
	}
	rawFormat, err := g.run(ctx, root, "rev-parse", "--show-object-format")
	if err != nil {
		return nil, err
	}
	format := strings.TrimSpace(string(rawFormat))
	if format != "sha1" && format != "sha256" {
		return nil, diffIsolationUnavailable()
	}
	if info, err := os.Stat(objects); err != nil || !info.IsDir() {
		return nil, diffIsolationUnavailable()
	}

	private, err := os.MkdirTemp("", "delidev-diff-")
	if err != nil {
		return nil, diffIsolationUnavailable()
	}
	defer func() {
		if os.RemoveAll(private) != nil {
			patch, returned = nil, ResultUncertain()
		}
	}()
	for _, dir := range []string{"info", filepath.Join("objects", "info"), filepath.Join("objects", "pack"), filepath.Join("refs", "heads")} {
		if os.MkdirAll(filepath.Join(private, dir), 0700) != nil {
			return nil, diffIsolationUnavailable()
		}
	}
	settings := "[include]\n\tpath = " + strconv.Quote(config) + "\n"
	if _, err := os.Stat(worktreeConfig); err == nil {
		settings += "[include]\n\tpath = " + strconv.Quote(worktreeConfig) + "\n"
	} else if !os.IsNotExist(err) {
		return nil, diffIsolationUnavailable()
	}
	version := "0"
	if format == "sha256" {
		version = "1"
		settings += "[extensions]\n\tobjectFormat = sha256\n"
	}
	settings += "[core]\n\trepositoryformatversion = " + version + "\n\tbare = false\n"
	files := map[string]string{
		"HEAD":                              "ref: refs/heads/delidev-diff\n",
		"config":                            settings,
		filepath.Join("info", "attributes"): "* !filter\n",
		filepath.Join("objects", "info", "alternates"): filepath.ToSlash(objects) + "\n",
	}
	for name, contents := range files {
		if os.WriteFile(filepath.Join(private, name), []byte(contents), 0600) != nil {
			return nil, diffIsolationUnavailable()
		}
	}
	isolated := g
	isolated.diffIndexFile = index
	command := append([]string{"--git-dir=" + private, "--work-tree=" + root}, args...)
	return isolated.run(ctx, root, command...)
}

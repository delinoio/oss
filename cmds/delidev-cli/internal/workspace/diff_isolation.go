package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strconv"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func diffIsolationUnavailable() error {
	return domain.Fail(domain.Unavailable, "The isolated Git comparison could not be prepared.", "Check temporary storage on the execution machine and retry.")
}

// An ordinary Git worktree diff can run a clean/process filter after a prior
// attribute check. A private Git admin directory gives filter=unspecified the
// highest precedence for every path, regardless of live attribute or index
// changes. The real index and objects remain read-only inputs; remove this
// isolation only if Git gains a documented per-command filter disable switch.
func (g Git) filterFreeDiff(ctx context.Context, root string, args []string) (patch []byte, returned error) {
	// Resolve all administration paths in one owned process. Each independent
	// launch includes durable ownership setup; repeating it consumed much of
	// the bounded observation on Windows before Git could compare any files.
	fields, err := g.revParseFields(ctx, root, 5, "--git-path", "index", "--git-path", "objects", "--git-path", "config", "--git-path", "config.worktree", "--show-object-format")
	if err != nil {
		return nil, err
	}
	for _, path := range fields[:4] {
		if !filepath.IsAbs(path) {
			return nil, diffIsolationUnavailable()
		}
	}
	index, objects, config, worktreeConfig, format := fields[0], fields[1], fields[2], fields[3], fields[4]
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

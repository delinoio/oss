//go:build !windows

package userservice

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func readDefinition(s Spec) (bool, error) {
	b, err := security.ReadPrivate(s.DefinitionPath, 64<<10)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !bytes.Equal(b, definition(s)) {
		return false, failure()
	}
	return true, nil
}
func createDefinition(s Spec) error {
	dir := filepath.Dir(s.DefinitionPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return failure()
	}
	real, err := filepath.EvalSymlinks(dir)
	info, e := os.Lstat(dir)
	if err != nil || e != nil || real != dir || !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return failure()
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || st.Uid != uint32(os.Geteuid()) {
		return failure()
	}
	f, err := os.CreateTemp(dir, ".delidev-definition-")
	if err != nil {
		return failure()
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(definition(s))
	}
	if err == nil {
		err = f.Sync()
	}
	ce := f.Close()
	if err != nil || ce != nil {
		return failure()
	}
	// A hard-link publication is atomic and refuses an existing registration.
	if os.Link(f.Name(), s.DefinitionPath) != nil {
		return failure()
	}
	return security.SyncParent(s.DefinitionPath)
}
func removeDefinition(s Spec) error {
	if ok, e := readDefinition(s); e != nil || !ok {
		return failure()
	}
	original, e := os.Lstat(s.DefinitionPath)
	if e != nil {
		return failure()
	}
	claim := s.DefinitionPath + ".removal-" + string(domain.NewID())
	if renameExclusive(s.DefinitionPath, claim) != nil {
		return failure()
	}
	claimed := s
	claimed.DefinitionPath = claim
	actual, e := os.Lstat(claim)
	ok, e2 := readDefinition(claimed)
	if e != nil || e2 != nil || !ok || !os.SameFile(original, actual) {
		// Preserve a raced/replaced claim without overwriting a newly registered path.
		return failure()
	}
	if security.SyncParent(claim) != nil || os.Remove(claim) != nil {
		return failure()
	}
	return security.SyncParent(claim)
}

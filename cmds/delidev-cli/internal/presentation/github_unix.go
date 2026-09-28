//go:build !windows

package presentation

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os/exec"
)

func configureCommand(_ *exec.Cmd) {}
func DispatchGitHub(_ string) error {
	return domain.Fail(domain.Unsupported, "The native Windows helper is unavailable on this platform.", "Use presentation open-github.")
}

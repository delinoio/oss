// SPDX-License-Identifier: Apache-2.0

package process

import (
	"errors"
	"os/exec"
	"path/filepath"
)

// NewExecCommand constructs a command without starting it. Path and Dir must be
// absolute, Args includes argv[0], and nil Env means an empty environment.
// On Windows, batch wrappers use the system command interpreter. Arguments
// that cannot safely survive batch expansion are rejected before execution.
func NewExecCommand(c Command) (*exec.Cmd, error) {
	if c.Noop || !filepath.IsAbs(c.Path) || !filepath.IsAbs(c.Dir) || len(c.Args) == 0 {
		return nil, errors.New("resolved absolute executable, cwd, and argv are required")
	}
	cmd := exec.Command(c.Path)
	cmd.Args = append([]string(nil), c.Args...)
	cmd.Dir = c.Dir
	cmd.Env = append([]string{}, c.Env...)
	if err := configureCommand(cmd); err != nil {
		return nil, err
	}
	return cmd, nil
}

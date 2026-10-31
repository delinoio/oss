// SPDX-License-Identifier: Apache-2.0

package process

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureCommand(cmd *exec.Cmd) error {
	ext := filepath.Ext(cmd.Path)
	if !strings.EqualFold(ext, ".cmd") && !strings.EqualFold(ext, ".bat") {
		return nil
	}
	info, err := os.Stat(cmd.Path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return os.ErrPermission
	}
	// npm wrappers forward %* through another cmd parsing pass. Quotes, percent
	// expansion and control characters cannot be passed transparently by generic
	// batch scripts. Fail closed rather than execute altered or injected commands.
	quote := func(arg string) (string, error) {
		if strings.ContainsAny(arg, "\"%\r\n\x00") {
			return "", fmt.Errorf("batch wrapper arguments containing quotes, percent signs, or control characters are unavailable; invoke the underlying executable directly")
		}
		// Quote every argument, including empty strings and shell metacharacters.
		// Double trailing backslashes for the receiving executable's argv parser.
		trailing := len(arg) - len(strings.TrimRight(arg, `\`))
		return `"` + arg + strings.Repeat(`\`, trailing) + `"`, nil
	}
	path, err := quote(cmd.Path)
	if err != nil {
		return err
	}
	parts := []string{path}
	for _, arg := range cmd.Args[1:] {
		value, err := quote(arg)
		if err != nil {
			return err
		}
		parts = append(parts, value)
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	cmd.Path = filepath.Join(system, "cmd.exe")
	cmd.Args = []string{cmd.Path}
	// /d disables AutoRun; /v:off disables !variable! expansion. SysProcAttr
	// avoids os/exec applying executable quoting rules to cmd's command string.
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `cmd.exe /d /s /v:off /c "` + strings.Join(parts, " ") + `"`}
	return nil
}

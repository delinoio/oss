//go:build darwin

// SPDX-License-Identifier: Apache-2.0
package claude

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"time"
)

const (
	usageSecurityTool    = "/usr/bin/security"
	usageKeychainTimeout = 10 * time.Second
	// errSecItemNotFound is the exit status of the tool for an absent item.
	usageKeychainNotFound = 44
)

type systemUsageKeychain struct{}

// SystemUsageKeychain returns the macOS Keychain adapter. It reads and deletes
// exactly one item named by the caller and never enumerates the Keychain.
func SystemUsageKeychain() UsageKeychain { return systemUsageKeychain{} }

func usageKeychainCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, usageSecurityTool, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=" + os.Getenv("HOME")}
	return cmd
}

func (systemUsageKeychain) Find(ctx context.Context, service, account string) ([]byte, bool, error) {
	bounded, cancel := context.WithTimeout(ctx, usageKeychainTimeout)
	defer cancel()
	out, err := usageKeychainCommand(bounded, "find-generic-password", "-s", service, "-a", account, "-w").Output()
	if err != nil {
		clear(out)
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == usageKeychainNotFound {
			return nil, false, nil
		}
		return nil, false, errors.New("keychain lookup failed")
	}
	for len(out) != 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	return out, true, nil
}

func (systemUsageKeychain) Delete(ctx context.Context, service, account string) error {
	bounded, cancel := context.WithTimeout(ctx, usageKeychainTimeout)
	defer cancel()
	out, err := usageKeychainCommand(bounded, "delete-generic-password", "-s", service, "-a", account).CombinedOutput()
	clear(out)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == usageKeychainNotFound {
			return nil
		}
		return errors.New("keychain removal failed")
	}
	return nil
}

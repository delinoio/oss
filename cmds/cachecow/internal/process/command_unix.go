// SPDX-License-Identifier: Apache-2.0

//go:build darwin || linux

package process

import "os/exec"

func configureCommand(*exec.Cmd) error { return nil }

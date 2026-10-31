// SPDX-License-Identifier: Apache-2.0

//go:build !darwin && !linux && !windows

package clone

import (
	"context"
	"os"
)

const sourceOpenFlags = os.O_RDONLY

func nativeClone(ctx context.Context, src *os.File, dst string) error { return ErrUnsupported }

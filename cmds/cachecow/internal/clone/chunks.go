// SPDX-License-Identifier: Apache-2.0

package clone

import (
	"context"
	"errors"
	"math"
)

// ReFS clone requests must be cluster-aligned and strictly smaller than 4 GiB.
// Keep the planner platform-independent so those boundaries can be tested on CI.
func alignedLength(size, cluster int64) (int64, error) {
	if size < 0 || cluster <= 0 || cluster >= 1<<32 || size > math.MaxInt64-(cluster-1) {
		return 0, errors.New("invalid ReFS file length or cluster size")
	}
	return (size + cluster - 1) / cluster * cluster, nil
}

func cloneChunks(ctx context.Context, length, cluster int64, clone func(int64, int64) error) error {
	aligned, err := alignedLength(length, cluster)
	if err != nil {
		return err
	}
	if aligned != length {
		return errors.New("ReFS clone length is not cluster-aligned")
	}
	limit := ((int64(1) << 32) - 1) / cluster * cluster
	for offset := int64(0); offset < length; {
		if err := ctx.Err(); err != nil {
			return err
		}
		size := min(limit, length-offset)
		if err := clone(offset, size); err != nil {
			return err
		}
		offset += size
	}
	return ctx.Err()
}

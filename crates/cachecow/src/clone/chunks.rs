// SPDX-License-Identifier: Apache-2.0
//! ReFS clone requests must be cluster-aligned and strictly smaller than 4 GiB.
//! The planner is platform-independent so those boundaries are tested on CI.
#![cfg_attr(
    not(windows),
    allow(dead_code, reason = "used by the Windows ReFS backend and tests")
)]

use anyhow::{Result, bail};

use crate::Context;

/// Rounds `size` up to a multiple of `cluster`.
pub(crate) fn aligned_length(size: i64, cluster: i64) -> Result<i64> {
    if size < 0 || cluster <= 0 || cluster >= 1 << 32 || size > i64::MAX - (cluster - 1) {
        bail!("invalid ReFS file length or cluster size");
    }
    Ok((size + cluster - 1) / cluster * cluster)
}

/// Calls `clone(offset, size)` for consecutive cluster-aligned chunks below
/// 4 GiB covering `length`, checking `ctx` before each chunk.
pub(crate) fn clone_chunks(
    ctx: &Context,
    length: i64,
    cluster: i64,
    clone: &mut dyn FnMut(i64, i64) -> Result<()>,
) -> Result<()> {
    let aligned = aligned_length(length, cluster)?;
    if aligned != length {
        bail!("ReFS clone length is not cluster-aligned");
    }
    let limit = ((1_i64 << 32) - 1) / cluster * cluster;
    let mut offset = 0;
    while offset < length {
        ctx.err()?;
        let size = limit.min(length - offset);
        clone(offset, size)?;
        offset += size;
    }
    ctx.err()?;
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::context::CancelError;

    #[test]
    fn refs_chunk_planning() {
        for cluster in [4096_i64, 65536] {
            for size in [0_i64, 1, 4095, 4096, 65537, 1 << 32, (1 << 33) + 1] {
                let length = aligned_length(size, cluster).unwrap();
                assert!(
                    length >= size && length - size < cluster,
                    "bad rounding {length}"
                );
                let mut end = 0_i64;
                clone_chunks(
                    &Context::background(),
                    length,
                    cluster,
                    &mut |offset, count| {
                        assert!(
                            offset == end
                                && offset % cluster == 0
                                && count % cluster == 0
                                && count > 0
                                && count < 1 << 32,
                            "bad chunk {offset} {count}"
                        );
                        end += count;
                        Ok(())
                    },
                )
                .unwrap();
                assert_eq!(end, length);
            }
        }
        for (size, cluster) in [(-1, 4096), (1, 0), (1, 1 << 32), (i64::MAX, 4096)] {
            assert!(
                aligned_length(size, cluster).is_err(),
                "accepted {size} {cluster}"
            );
        }
        let (ctx, cancel) = Context::background().with_cancel();
        let mut calls = 0;
        let err = clone_chunks(&ctx, 1 << 33, 4096, &mut |_, _| {
            calls += 1;
            cancel.cancel();
            Ok(())
        })
        .unwrap_err();
        assert!(crate::error::is(&err, &CancelError::Canceled), "{err}");
        assert_eq!(calls, 1);
        let err = clone_chunks(&Context::background(), 1 << 33, 4096, &mut |_, _| {
            Err(anyhow::anyhow!("chunk failure"))
        })
        .unwrap_err();
        assert_eq!(err.to_string(), "chunk failure");
    }
}

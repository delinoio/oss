// SPDX-License-Identifier: Apache-2.0
//! Platforms without a supported copy-on-write mechanism.

use std::fs::File;

use anyhow::Result;

use super::Unsupported;
use crate::Context;

pub(super) fn clone_file(_ctx: &Context, _src: &File, _dst: &str) -> Result<()> {
    Err(Unsupported.into())
}

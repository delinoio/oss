// Copyright 2026 Delino. SPDX-License-Identifier: Apache-2.0
//! `residual-dependency-removal`: not implemented yet. The pass is registered
//! so CLI and report identifiers stay stable; it proposes no candidates.
use anyhow::Result;

use super::super::analysis::Ctx;
use crate::{passes::PassSet, plan::Plan};

pub(in crate::compiler) fn plan(_ctx: &mut Ctx, _enabled: &PassSet, _out: &mut Plan) -> Result<()> {
    Ok(())
}

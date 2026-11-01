// SPDX-License-Identifier: Apache-2.0
//! CacheCow's public command boundaries (Go `internal/cli`).

use anyhow::Result;

use crate::session::{Plan, RecordHooks};

/// Process entry point after supervisor dispatch. Returns the exit code.
pub fn main(version: &str) -> i32 {
    let _ = version;
    todo!("cli::main")
}

/// Supervisor-side claim validation (filesystem reads only).
pub fn validate(plan: &Plan) -> Result<()> {
    let _ = plan;
    todo!("cli::validate")
}

/// Provenance hooks run by the supervisor under its leases.
pub fn records() -> RecordHooks {
    todo!("cli::records")
}

// SPDX-License-Identifier: Apache-2.0
//! Binds durable cache leases to the native process supervisor (Go
//! `internal/session`), including authenticated nested-session IPC.

use anyhow::Result;
use serde::{Deserialize, Serialize};

use crate::{
    Context,
    lease::Resources,
    lifecycle::{LifecycleResult, Pair},
    process::{Command, RunResult, Streams, SupervisorHooks},
};

/// Environment variable carrying the nested-session capability.
pub const ENV: &str = "CCOW_SESSION";

#[derive(Debug, Clone, Copy, PartialEq, Eq, Default, Serialize, Deserialize)]
pub enum Action {
    #[default]
    #[serde(rename = "prepare")]
    Prepare,
    #[serde(rename = "run")]
    Run,
    #[serde(rename = "clean")]
    Clean,
    #[serde(rename = "tool-clean")]
    ToolClean,
}

impl Action {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Prepare => "prepare",
            Self::Run => "run",
            Self::Clean => "clean",
            Self::ToolClean => "tool-clean",
        }
    }
}

/// One managed operation, persisted as `operation.json` and sent over IPC.
#[derive(Debug, Clone, Default, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Plan {
    /// Supervisor-owned evidence; never accepted over IPC.
    #[serde(skip)]
    pub prepared_all: bool,
    #[serde(rename = "schemaVersion")]
    pub schema_version: i64,
    pub root: String,
    pub action: Action,
    pub resources: Resources,
    pub pairs: Vec<Pair>,
    pub record: String,
    pub replace: bool,
    #[serde(rename = "allowCopy")]
    pub allow_copy: bool,
    /// Opaque, CLI-owned discovery claims.
    pub claims: serde_json::Value,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Response {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub ticket: String,
    pub result: LifecycleResult,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub error: String,
    #[serde(
        rename = "sourceFailure",
        default,
        skip_serializing_if = "std::ops::Not::not"
    )]
    pub source_failure: bool,
}

/// An unusable source after acquiring its lease; clients offer the same
/// source-recovery choices as discovery failures.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("{message}")]
pub struct SourceError {
    pub message: String,
}

/// Supervisor-side claim validation using filesystem reads only.
pub type Validate = fn(&Plan) -> Result<()>;

/// Provenance callbacks implemented by the CLI.
#[derive(Clone, Copy, Default)]
pub struct RecordHooks {
    pub validate_source: Option<fn(&Plan) -> Result<()>>,
    pub compatible: Option<fn(&Plan, &Plan) -> bool>,
    pub before: Option<fn(&Plan) -> Result<()>>,
    pub after: Option<fn(&Plan, i32) -> Result<()>>,
}

/// Supervisor hooks that bind leases, preparation and nested IPC.
pub fn hooks(validate: Validate, records: RecordHooks) -> SupervisorHooks {
    let _ = (validate, records);
    todo!("session::hooks")
}

/// Runs a top-level plan under a new native supervisor.
pub fn execute(
    ctx: &Context,
    plan: &Plan,
    command: Command,
    streams: Streams,
) -> (RunResult, LifecycleResult, Result<()>) {
    let _ = (ctx, plan, command, streams);
    todo!("session::execute")
}

/// Sends a nested plan to the authenticated outer session.
pub fn nested(ctx: &Context, capability: &str, plan: &Plan) -> Result<Response> {
    let _ = (ctx, capability, plan);
    todo!("session::nested")
}

/// Asks the authenticated native owner to drain the entire session.
pub fn cancel(ctx: &Context, capability: &str, ticket: &str) -> Result<()> {
    let _ = (ctx, capability, ticket);
    todo!("session::cancel")
}

/// Reports a nested command's completion.
pub fn complete(ctx: &Context, capability: &str, ticket: &str, code: i32) -> Result<()> {
    let _ = (ctx, capability, ticket, code);
    todo!("session::complete")
}

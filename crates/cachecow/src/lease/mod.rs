// SPDX-License-Identifier: Apache-2.0
//! Durable cache leases shared by every worktree and preset (Go
//! `internal/lease`).
//!
//! Durable intents survive owner death. The process supervisor must positively
//! verify tree termination before `finish` or `reconcile`; PID death is not
//! proof. This module is coordination, not a path sandbox.

use std::sync::Arc;

use anyhow::Result;
use serde::{Deserialize, Serialize};

use crate::Context;

/// Typed lease failures.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum LeaseError {
    /// A real overlapping lease record (never registry lock contention).
    #[error("cache is in use; wait for its managed process tree or reconcile its durable intent")]
    Busy,
    /// SQLite lock contention that outlived the bounded retry budget (#4583).
    #[error(
        "cache lease registry is temporarily locked by another ccow operation; retry the command"
    )]
    RegistryBusy,
    #[error(
        "nested cache request conflicts with another session; acquire the complete resource set \
         before launching the outer command or run this command after it exits"
    )]
    NestedConflict,
    #[error(
        "process-tree ownership is unverified; retain the lease and reconcile with the native \
         supervisor"
    )]
    Unverified,
}

/// Every source and destination of one request. Both kinds are exclusive.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Resources {
    #[serde(rename = "Sources", default)]
    pub sources: Vec<String>,
    #[serde(rename = "Destinations", default)]
    pub destinations: Vec<String>,
}

/// Durable lease record. Contains no command arguments or environment.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Record {
    pub version: i64,
    pub id: String,
    pub ownership: String,
    pub paths: Vec<String>,
}

/// Positive whole-tree completion check. Runs outside registry transactions.
pub type Verify<'a> = &'a (dyn Fn(&Context, &Record) -> Result<()> + Sync);

/// Session-supplied behavior used while an acquisition waits on a conflicting
/// record (#4582).
pub trait WaitObserver: Send + Sync {
    /// Positive native completion evidence for a conflicting record's owner.
    /// Returning `Ok` lets the lease layer delete that record.
    fn verify(&self, ctx: &Context, record: &Record) -> Result<()>;
    /// Called once per acquisition when it must wait for, or refuse, an
    /// unrecoverable conflicting record. `cache_path` is the requested path.
    fn blocked(&self, cache_path: &str, record: &Record);
}

/// Result of an acquisition.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Grant {
    /// The complete request was already protected; skip preparation.
    pub borrowed: bool,
    /// Newly protected resources, including sources.
    pub new_paths: Vec<String>,
}

/// Shared lease registry (`<dir>/leases.sqlite`).
pub struct Registry {
    _private: (),
}

impl Registry {
    /// Opens the shared registry, creating the schema when empty. Contention is
    /// retried for the bounded budget and then reported as `RegistryBusy`.
    pub fn open(ctx: &Context, dir: &str) -> Result<Arc<Self>> {
        let _ = (ctx, dir);
        todo!("lease::Registry::open")
    }

    /// Like [`Registry::open`], but waits through `RegistryBusy` until `ctx`
    /// ends.
    pub fn open_wait(ctx: &Context, dir: &str) -> Result<Arc<Self>> {
        let _ = (ctx, dir);
        todo!("lease::Registry::open_wait")
    }

    /// Creates an in-memory session capability owned by `ownership`.
    pub fn new_session(self: &Arc<Self>, ownership: &str) -> Result<Session> {
        let _ = ownership;
        todo!("lease::Registry::new_session")
    }

    /// Durable intents for diagnostics and positive native recovery.
    pub fn records(&self, ctx: &Context) -> Result<Vec<Record>> {
        let _ = ctx;
        todo!("lease::Registry::records")
    }

    /// Deletes `record` after `verify` succeeds and the stored record is equal.
    pub fn reconcile(&self, ctx: &Context, record: &Record, verify: Verify<'_>) -> Result<()> {
        let _ = (ctx, record, verify);
        todo!("lease::Registry::reconcile")
    }

    /// Directory that contains the registry database.
    pub fn directory(&self) -> &str {
        todo!("lease::Registry::directory")
    }
}

/// In-memory session capability. Cannot be reconstructed from an ID.
pub struct Session {
    _private: (),
}

impl Session {
    pub fn id(&self) -> String {
        todo!("lease::Session::id")
    }

    /// Installs the wait observer (#4582). Without one, conflicting records are
    /// never recovered automatically and no notice is emitted.
    pub fn set_observer(&self, observer: Arc<dyn WaitObserver>) {
        let _ = observer;
        todo!("lease::Session::set_observer")
    }

    /// Commits intent before returning. `wait=false` is the nonblocking clean.
    pub fn acquire(&self, ctx: &Context, resources: &Resources, wait: bool) -> Result<Grant> {
        let _ = (ctx, resources, wait);
        todo!("lease::Session::acquire")
    }

    /// Releases every resource after `verify` proves whole-session completion.
    pub fn finish(&self, ctx: &Context, verify: Verify<'_>) -> Result<()> {
        let _ = (ctx, verify);
        todo!("lease::Session::finish")
    }

    /// Releases newly acquired read-only sources after copying.
    pub fn release_prepared_sources(&self, paths: &[String], grant: &Grant) -> Result<()> {
        let _ = (paths, grant);
        todo!("lease::Session::release_prepared_sources")
    }

    /// Removes exactly `grant.new_paths` before any command used them (#4579).
    pub fn release_unstarted_grant(&self, grant: &Grant) -> Result<()> {
        let _ = grant;
        todo!("lease::Session::release_unstarted_grant")
    }
}

/// Creates (privately) or validates an existing private directory. Never
/// repairs an unsafe existing directory.
pub fn ensure_private_directory(path: &str) -> Result<()> {
    let _ = path;
    todo!("lease::ensure_private_directory")
}

/// Validates an existing private record file without creating it.
pub fn validate_private_file(path: &str) -> Result<()> {
    let _ = path;
    todo!("lease::validate_private_file")
}

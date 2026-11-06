// SPDX-License-Identifier: Apache-2.0
//! Durable cache leases shared by every worktree and preset (Go
//! `internal/lease`).
//!
//! Durable intents survive owner death. The process supervisor must positively
//! verify tree termination before `finish` or `reconcile`; PID death is not
//! proof. Callers validate repository membership, cache ownership and
//! protected-path exclusions before acquiring. This module is coordination,
//! not a path sandbox.

use std::{
    collections::{BTreeSet, HashSet},
    sync::{Arc, Mutex, MutexGuard, PoisonError},
    time::{Duration, Instant},
};

use anyhow::{Result, anyhow};
use rusqlite::{Connection, ErrorCode, OpenFlags};
use serde::{Deserialize, Serialize};

use crate::{Context, paths};

#[cfg(unix)]
mod privacy_unix;
#[cfg(unix)]
use privacy_unix as privacy;
#[cfg(windows)]
mod privacy_windows;
#[cfg(windows)]
use privacy_windows as privacy;

#[cfg(test)]
mod tests;

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

/// Total time one registry transaction retries SQLite contention (#4583).
pub const REGISTRY_CONTENTION_BUDGET: Duration = Duration::from_secs(1);
const CONTENTION_WAIT: Duration = Duration::from_millis(25);
const LEASE_POLL: Duration = Duration::from_millis(25);
const OPEN_WAIT_POLL: Duration = Duration::from_millis(20);

/// Every source and destination of one request. Both kinds are exclusive.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
pub struct Resources {
    #[serde(rename = "Sources", default)]
    pub sources: Vec<String>,
    #[serde(rename = "Destinations", default)]
    pub destinations: Vec<String>,
}

/// Durable lease record. Contains no command arguments or environment.
/// Ownership is a native supervisor recovery identifier, allocated before
/// spawning any child.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Record {
    pub version: i64,
    pub id: String,
    pub ownership: String,
    pub paths: Vec<String>,
}

/// Positive whole-tree completion check. Runs outside registry transactions.
/// A PID lookup, session environment variable or elapsed timeout alone cannot
/// establish completion. Unknown ownership must return an error.
pub type Verify<'a> = &'a (dyn Fn(&Context, &Record) -> Result<()> + Sync);

/// Session-supplied behavior used while an acquisition waits on a conflicting
/// record (#4582).
pub trait WaitObserver: Send + Sync {
    /// Positive native completion evidence for a conflicting record's owner.
    /// Returning `Ok` lets the lease layer delete that record.
    fn verify(&self, ctx: &Context, record: &Record) -> Result<()>;
    /// Called once per waiting acquisition when an unrecoverable conflicting
    /// record blocks it. `cache_path` is the requested path. Nonblocking
    /// acquisitions (clean) refuse without a waiting notice.
    fn blocked(&self, cache_path: &str, record: &Record);
}

/// Result of an acquisition.
///
/// Grants have no independent release once a command starts: accumulated
/// resources remain held until the entire session is finished. Before any
/// command uses them, [`Session::release_prepared_sources`] can drop copied
/// read-only sources and [`Session::release_unstarted_grant`] can undo a
/// nested grant whose request failed.
#[derive(Debug, Clone, Default, PartialEq, Eq)]
pub struct Grant {
    /// The complete request was already protected; skip preparation.
    pub borrowed: bool,
    /// Newly protected resources, including sources. Preparation may only
    /// target new destinations, never a destination borrowed from the parent.
    pub new_paths: Vec<String>,
}

/// Shared lease registry (`<dir>/leases.sqlite`).
pub struct Registry {
    path: String,
    dir: String,
    /// Idle connections. Transactions never wait for a local connection, so a
    /// nonblocking clean can observe contention instead of queueing locally.
    pool: Mutex<Vec<Connection>>,
}

fn lock<T>(m: &Mutex<T>) -> MutexGuard<'_, T> {
    m.lock().unwrap_or_else(PoisonError::into_inner)
}

fn contention(error: &anyhow::Error) -> bool {
    crate::error::find::<rusqlite::Error>(error).is_some_and(|e| {
        matches!(
            e.sqlite_error_code(),
            Some(ErrorCode::DatabaseBusy | ErrorCode::DatabaseLocked)
        )
    })
}

fn is_lease(error: &anyhow::Error, expected: LeaseError) -> bool {
    crate::error::is(error, &expected)
}

impl Registry {
    /// Opens the shared registry, creating the schema when empty. Use one
    /// stable, user-private directory shared by every worktree and preset.
    /// Contention is retried for the bounded budget and then reported as
    /// `RegistryBusy`.
    pub fn open(ctx: &Context, dir: &str) -> Result<Arc<Self>> {
        privacy::private_dir(dir)?;
        let dir = paths::abs(&paths::eval_symlinks(dir)?)?;
        let path = paths::join2(&dir, "leases.sqlite");
        privacy::private_database(&path)?;
        let registry = Arc::new(Self {
            path,
            dir,
            pool: Mutex::new(Vec::new()),
        });
        registry.transaction(ctx, |c| {
            let version: i64 = c.query_row("PRAGMA user_version", [], |r| r.get(0))?;
            if version != 0 && version != 1 {
                return Err(anyhow!(
                    "unsupported lease registry version {version}; use a compatible ccow"
                ));
            }
            if version == 0 {
                let count: i64 = c.query_row(
                    "SELECT count(*) FROM sqlite_master WHERE type='table'",
                    [],
                    |r| r.get(0),
                )?;
                if count != 0 {
                    return Err(anyhow!("unversioned lease registry is not empty"));
                }
                c.execute_batch(
                    "CREATE TABLE leases (id TEXT PRIMARY KEY, record TEXT NOT NULL); PRAGMA \
                     user_version=1",
                )?;
            }
            records(c).map(|_| ())
        })?;
        tracing::debug!(directory = %registry.dir, "lease registry opened");
        Ok(registry)
    }

    /// Like [`Registry::open`], but waits through `RegistryBusy` until `ctx`
    /// ends. Use [`Registry::open`] for nonblocking management such as clean.
    pub fn open_wait(ctx: &Context, dir: &str) -> Result<Arc<Self>> {
        loop {
            match Self::open(ctx, dir) {
                Err(e) if is_lease(&e, LeaseError::RegistryBusy) => {
                    tracing::debug!(directory = dir, "waiting for lease registry initialization");
                    ctx.sleep(OPEN_WAIT_POLL)?;
                }
                other => return other,
            }
        }
    }

    /// Creates an in-memory session capability owned by `ownership`.
    pub fn new_session(self: &Arc<Self>, ownership: &str) -> Result<Session> {
        if ownership.is_empty() || ownership.len() > 4096 {
            return Err(LeaseError::Unverified.into());
        }
        let id = uuid::Uuid::now_v7().to_string();
        Ok(Session {
            registry: self.clone(),
            state: Mutex::new(SessionState {
                record: Record {
                    version: 1,
                    id,
                    ownership: ownership.to_string(),
                    paths: Vec::new(),
                },
                finished: false,
            }),
            observer: Mutex::new(None),
        })
    }

    /// Durable intents for diagnostics and positive native recovery. All
    /// records are potentially poisoned: no PID-based reclamation exists.
    pub fn records(&self, ctx: &Context) -> Result<Vec<Record>> {
        self.transaction(ctx, records)
    }

    /// Deletes `record` after `verify` succeeds and the stored record is equal.
    pub fn reconcile(&self, ctx: &Context, record: &Record, verify: Verify<'_>) -> Result<()> {
        self.reconcile_with(ctx, record, verify, false)
    }

    /// Like [`Registry::reconcile`]. With `allow_missing`, an absent record is
    /// already released: only another process that verified the same complete
    /// journal (automatic recovery while waiting, or doctor) can delete it.
    fn reconcile_with(
        &self,
        ctx: &Context,
        record: &Record,
        verify: Verify<'_>,
        allow_missing: bool,
    ) -> Result<()> {
        let snapshot = record.clone();
        if let Err(e) = verify(ctx, &snapshot) {
            return Err(anyhow::Error::new(LeaseError::Unverified).context(format!(
                "{}: {}",
                LeaseError::Unverified,
                crate::error::chain_text(&e)
            )));
        }
        self.transaction(ctx, |c| {
            let all = records(c)?;
            let Some(stored) = all.iter().find(|v| v.id == record.id) else {
                if allow_missing {
                    tracing::debug!(lease = %record.id, "cache lease was already released by verified recovery");
                    return Ok(());
                }
                return Err(LeaseError::Unverified.into());
            };
            if stored != record {
                return Err(LeaseError::Unverified.into());
            }
            c.execute("DELETE FROM leases WHERE id=?1", [&stored.id])?;
            Ok(())
        })?;
        tracing::debug!(lease = %record.id, "cache lease released after verified completion");
        Ok(())
    }

    /// Directory that contains the registry database.
    pub fn directory(&self) -> &str {
        &self.dir
    }

    fn connection(&self) -> Result<Connection> {
        if let Some(c) = lock(&self.pool).pop() {
            return Ok(c);
        }
        // The filename is used literally (no URI parsing), so `?` and `#` in
        // the state path cannot select SQLite options.
        let c = Connection::open_with_flags(
            &self.path,
            OpenFlags::SQLITE_OPEN_READ_WRITE
                | OpenFlags::SQLITE_OPEN_CREATE
                | OpenFlags::SQLITE_OPEN_NO_MUTEX,
        )?;
        Ok(c)
    }

    /// Runs `f` in a `BEGIN IMMEDIATE` transaction. SQLite contention is
    /// retried every 25 ms for [`REGISTRY_CONTENTION_BUDGET`], measured from
    /// the first attempt, and then reported as `RegistryBusy`. Waiting stays
    /// here (busy timeout 0) so it honors `ctx`. `f` must be safe to repeat.
    fn transaction<T>(
        &self,
        ctx: &Context,
        mut f: impl FnMut(&Connection) -> Result<T>,
    ) -> Result<T> {
        let started = Instant::now();
        let mut attempt = 0u32;
        loop {
            ctx.err()?;
            attempt += 1;
            match self.attempt(&mut f) {
                Err(e) if contention(&e) || is_lease(&e, LeaseError::RegistryBusy) => {
                    if started.elapsed() >= REGISTRY_CONTENTION_BUDGET {
                        tracing::debug!(attempt, "lease registry contention budget exhausted");
                        return Err(LeaseError::RegistryBusy.into());
                    }
                    tracing::debug!(attempt, "waiting for lease registry");
                    ctx.sleep(CONTENTION_WAIT)?;
                }
                other => return other,
            }
        }
    }

    fn attempt<T>(&self, f: &mut impl FnMut(&Connection) -> Result<T>) -> Result<T> {
        let c = self.connection()?;
        let result = (|| {
            c.execute_batch("PRAGMA busy_timeout=0; PRAGMA synchronous=FULL")?;
            c.execute_batch("BEGIN IMMEDIATE")?;
            let value = f(&c)?;
            c.execute_batch("COMMIT")?;
            Ok(value)
        })();
        if !c.is_autocommit() {
            let _ignored = c.execute_batch("ROLLBACK");
        }
        if c.is_autocommit() {
            lock(&self.pool).push(c);
        }
        result
    }
}

fn records(c: &Connection) -> Result<Vec<Record>> {
    let version: i64 = c.query_row("PRAGMA user_version", [], |r| r.get(0))?;
    if version != 1 {
        return Err(anyhow!("unsupported lease registry version {version}"));
    }
    let mut stmt = c.prepare("SELECT id, record FROM leases")?;
    let rows = stmt.query_map([], |r| Ok((r.get::<_, String>(0)?, r.get::<_, String>(1)?)))?;
    let mut out = Vec::new();
    for row in rows {
        let (id, raw) = row?;
        let mut de = serde_json::Deserializer::from_str(&raw);
        let v = Record::deserialize(&mut de).map_err(|e| anyhow!("malformed lease record: {e}"))?;
        de.end()
            .map_err(|_| anyhow!("trailing data in lease record"))?;
        let uuid_ok = uuid::Uuid::parse_str(&v.id).is_ok_and(|u| u.get_version_num() == 7);
        if !uuid_ok || v.id != id || v.version != 1 || v.ownership.is_empty() || v.paths.is_empty()
        {
            return Err(anyhow!(
                "unsupported or malformed lease record; do not remove protection"
            ));
        }
        for p in &v.paths {
            if !paths::is_abs(p) || paths::clean(p) != *p {
                return Err(anyhow!("invalid lease resource path"));
            }
        }
        out.push(v);
    }
    Ok(out)
}

struct SessionState {
    record: Record,
    finished: bool,
}

/// In-memory session capability. It cannot be reconstructed from an ID or
/// environment. Cross-process nesting must use authenticated supervisor IPC
/// to invoke `acquire` on the original session.
pub struct Session {
    registry: Arc<Registry>,
    state: Mutex<SessionState>,
    observer: Mutex<Option<Arc<dyn WaitObserver>>>,
}

struct Attempt {
    borrowed: bool,
    added: Vec<String>,
    next: Record,
}

impl Session {
    pub fn id(&self) -> String {
        lock(&self.state).record.id.clone()
    }

    /// Installs the wait observer (#4582). Without one, conflicting records are
    /// never recovered automatically and no notice is emitted.
    pub fn set_observer(&self, observer: Arc<dyn WaitObserver>) {
        *lock(&self.observer) = Some(observer);
    }

    /// Commits intent before returning; only then may preparation or spawn
    /// begin. `wait=false` is the nonblocking clean. New top-level sessions can
    /// wait; nested extensions fail on a real conflict, preventing dynamic
    /// cycles.
    pub fn acquire(&self, ctx: &Context, resources: &Resources, wait: bool) -> Result<Grant> {
        let mut st = lock(&self.state);
        if st.finished {
            return Err(LeaseError::Unverified.into());
        }
        let requested = normalize(resources)?;
        for p in &requested {
            if overlap(p, &self.registry.dir) {
                return Err(anyhow!(
                    "lease registry must remain outside every cache resource"
                ));
            }
        }
        let observer = lock(&self.observer).clone();
        let mut attempted: HashSet<String> = HashSet::new();
        let mut notified = false;
        loop {
            ctx.err()?;
            let mut conflicts: Vec<(Record, String)> = Vec::new();
            let current = st.record.clone();
            let outcome = self.registry.transaction(ctx, |c| {
                // Every attempt derives its state again, so a retried attempt
                // never records a path twice.
                conflicts.clear();
                let all = records(c)?;
                let mut found = false;
                for v in &all {
                    if v.id == current.id {
                        found = true;
                        if *v != current {
                            return Err(LeaseError::Unverified.into());
                        }
                        continue;
                    }
                    if let Some(p) = requested
                        .iter()
                        .find(|p| v.paths.iter().any(|q| overlap(p, q)))
                    {
                        conflicts.push((v.clone(), p.clone()));
                    }
                }
                if !conflicts.is_empty() {
                    return Err(LeaseError::Busy.into());
                }
                if !current.paths.is_empty() && !found {
                    return Err(LeaseError::Unverified.into());
                }
                let mut attempt = Attempt {
                    borrowed: !current.paths.is_empty(),
                    added: Vec::new(),
                    next: current.clone(),
                };
                for p in &requested {
                    if current.paths.iter().any(|q| contains(q, p)) {
                        continue;
                    }
                    if current.paths.iter().any(|q| overlap(p, q)) {
                        return Err(LeaseError::NestedConflict.into());
                    }
                    attempt.added.push(p.clone());
                    attempt.borrowed = false;
                    attempt.next.paths.push(p.clone());
                }
                attempt.next.paths.sort();
                let raw = serde_json::to_string(&attempt.next)?;
                c.execute(
                    "INSERT INTO leases(id,record) VALUES(?1,?2) ON CONFLICT(id) DO UPDATE SET \
                     record=excluded.record",
                    [&attempt.next.id, &raw],
                )?;
                Ok(attempt)
            });
            let nested = !st.record.paths.is_empty();
            let error = match outcome {
                Ok(a) => {
                    st.record = a.next;
                    tracing::debug!(session = %st.record.id, borrowed = a.borrowed, new_paths = a.added.len(), "cache lease acquired");
                    return Ok(Grant {
                        borrowed: a.borrowed,
                        new_paths: a.added,
                    });
                }
                Err(e) => e,
            };
            if is_lease(&error, LeaseError::RegistryBusy) {
                // Contention is never reported as cache use or a nested
                // conflict.
                if nested || !wait {
                    return Err(error);
                }
                tracing::debug!(session = %st.record.id, "waiting for lease registry");
                ctx.sleep(LEASE_POLL)?;
                continue;
            }
            if !is_lease(&error, LeaseError::Busy) {
                return Err(error);
            }
            if let Some(observer) = &observer
                && self.recover(ctx, observer.as_ref(), &conflicts, &mut attempted)
            {
                continue;
            }
            if nested {
                return Err(LeaseError::NestedConflict.into());
            }
            if !wait {
                return Err(error);
            }
            if !notified {
                notified = true;
                if let (Some(observer), Some((record, path))) = (&observer, conflicts.first()) {
                    observer.blocked(path, record);
                }
            }
            tracing::debug!(session = %st.record.id, conflicts = conflicts.len(), "waiting for cache lease");
            ctx.sleep(LEASE_POLL)?;
        }
    }

    /// Attempts native recovery of each conflicting record once per
    /// acquisition. Returns true when at least one record was released.
    fn recover(
        &self,
        ctx: &Context,
        observer: &dyn WaitObserver,
        conflicts: &[(Record, String)],
        attempted: &mut HashSet<String>,
    ) -> bool {
        let mut recovered = false;
        for (record, _) in conflicts {
            if !attempted.insert(record.id.clone()) {
                continue;
            }
            let verify = |c: &Context, r: &Record| observer.verify(c, r);
            match self.registry.reconcile(ctx, record, &verify) {
                Ok(()) => {
                    tracing::debug!(lease = %record.id, ownership = %record.ownership, "recovered conflicting cache lease after native completion");
                    recovered = true;
                }
                Err(e) => {
                    if is_lease(&e, LeaseError::RegistryBusy) {
                        attempted.remove(&record.id);
                    }
                    tracing::debug!(lease = %record.id, error = %crate::error::chain_text(&e), "conflicting cache lease remains protected");
                }
            }
        }
        recovered
    }

    /// Releases only after the supervisor verifies the whole session,
    /// including nested invocations. A failed verification leaves every
    /// resource protected.
    pub fn finish(&self, ctx: &Context, verify: Verify<'_>) -> Result<()> {
        let mut st = lock(&self.state);
        if st.finished {
            return Ok(());
        }
        if st.record.paths.is_empty() {
            st.finished = true;
            return Ok(());
        }
        // Waiting commands may recover this record after the journal became
        // complete but before this deletion; that is the same verified release.
        self.registry
            .reconcile_with(ctx, &st.record, verify, true)?;
        st.finished = true;
        Ok(())
    }

    /// Releases newly acquired read resources after copying, before any
    /// command starts. Pass only source paths from this acquisition's
    /// `new_paths`, never a destination or a borrowed parent path.
    pub fn release_prepared_sources(&self, paths: &[String], grant: &Grant) -> Result<()> {
        let mut st = lock(&self.state);
        if st.finished {
            return Err(LeaseError::Unverified.into());
        }
        let allowed: BTreeSet<&String> = grant.new_paths.iter().collect();
        let mut remove = BTreeSet::new();
        for p in paths {
            if !allowed.contains(p) {
                return Err(LeaseError::Unverified.into());
            }
            remove.insert(p.clone());
        }
        self.release(&mut st, &remove)
    }

    /// Removes exactly `grant.new_paths` before any command used them (#4579).
    /// Paths borrowed from the parent stay held. Refuses to empty the record.
    pub fn release_unstarted_grant(&self, grant: &Grant) -> Result<()> {
        let mut st = lock(&self.state);
        if st.finished {
            return Err(LeaseError::Unverified.into());
        }
        let remove: BTreeSet<String> = grant.new_paths.iter().cloned().collect();
        self.release(&mut st, &remove)?;
        if !remove.is_empty() {
            tracing::debug!(session = %st.record.id, released = remove.len(), "released unstarted nested grant");
        }
        Ok(())
    }

    fn release(&self, st: &mut SessionState, remove: &BTreeSet<String>) -> Result<()> {
        if remove.is_empty() {
            return Ok(());
        }
        let mut next = st.record.clone();
        next.paths.retain(|p| !remove.contains(p));
        if next.paths.is_empty() {
            return Err(LeaseError::Unverified.into());
        }
        let current = st.record.clone();
        let raw = serde_json::to_string(&next)?;
        self.registry.transaction(&Context::background(), |c| {
            let all = records(c)?;
            let Some(stored) = all.iter().find(|r| r.id == current.id) else {
                return Err(LeaseError::Unverified.into());
            };
            if *stored != current {
                return Err(LeaseError::Unverified.into());
            }
            c.execute(
                "UPDATE leases SET record=?1 WHERE id=?2",
                [&raw, &stored.id],
            )?;
            Ok(())
        })?;
        st.record = next;
        Ok(())
    }
}

fn has_parent_traversal(p: &str) -> bool {
    p.split(paths::is_separator).any(|part| part == "..")
}

fn normalize(r: &Resources) -> Result<Vec<String>> {
    let mut out = Vec::new();
    for p in r.sources.iter().chain(&r.destinations) {
        if p.is_empty() {
            return Err(anyhow!("empty cache path"));
        }
        // Cleaning "symlink/../cache" before resolving the link can select a
        // different cache from the OS. Require the discovery layer to resolve
        // such traversal first rather than silently protecting the
        // wrong resource.
        if has_parent_traversal(p) {
            return Err(anyhow!(
                "resolve parent traversal in cache paths before acquiring a lease"
            ));
        }
        let mut p = paths::abs(p)?;
        let mut suffix = Vec::new();
        loop {
            match paths::eval_symlinks(&p) {
                Ok(resolved) => {
                    p = resolved;
                    break;
                }
                Err(e) if e.kind() == std::io::ErrorKind::NotFound => {
                    // A dangling symlink is not an absent cache: do not invent
                    // its identity.
                    if std::fs::symlink_metadata(&p).is_ok_and(|m| m.file_type().is_symlink()) {
                        return Err(anyhow!("dangling cache path alias"));
                    }
                    let parent = paths::dir(&p);
                    if parent == p {
                        return Err(e.into());
                    }
                    suffix.push(paths::base(&p));
                    p = parent;
                }
                Err(e) => return Err(e.into()),
            }
        }
        for s in suffix.iter().rev() {
            p = paths::join2(&p, s);
        }
        out.push(p);
    }
    if out.is_empty() {
        return Err(anyhow!("complete cache resource set is empty"));
    }
    out.sort();
    Ok(out)
}

fn ancestors(path: &str) -> impl Iterator<Item = String> {
    let mut next = Some(path.to_string());
    std::iter::from_fn(move || {
        let current = next.take()?;
        let parent = paths::dir(&current);
        if parent != current {
            next = Some(parent);
        }
        Some(current)
    })
}

fn contains(parent: &str, child: &str) -> bool {
    if privacy::path_contains(parent, child) {
        return true;
    }
    // Compare existing ancestors and their absent suffixes. This covers aliases
    // through mounted directories even before either cache leaf has been
    // created.
    for p in ancestors(parent) {
        let Ok(pi) = paths::file_id(&p, true) else {
            continue;
        };
        let ps = paths::rel(&p, parent).unwrap_or_default();
        for c in ancestors(child) {
            if paths::file_id(&c, true).is_ok_and(|ci| ci == pi) {
                let cs = paths::rel(&c, child).unwrap_or_default();
                if privacy::path_contains(&ps, &cs) {
                    return true;
                }
            }
        }
    }
    false
}

fn overlap(a: &str, b: &str) -> bool {
    contains(a, b) || contains(b, a)
}

/// Creates (privately) or validates an existing private directory. Never
/// repairs an unsafe existing directory. Shared by the registry, settings and
/// process journals.
pub fn ensure_private_directory(path: &str) -> Result<()> {
    privacy::private_dir(path)
}

/// SDDL that [`ensure_private_directory`] applies at creation and requires on
/// validation. Directories created outside that function and later checked by
/// it must be created with this descriptor.
#[cfg(windows)]
pub(crate) fn private_directory_sddl() -> Result<String> {
    privacy::state_sddl()
}

/// Validates an existing private record file without creating it. A missing
/// file returns its I/O `NotFound` cause.
pub fn validate_private_file(path: &str) -> Result<()> {
    privacy::validate_private_record(path)
}

/// Validates an existing private record file without creating it and returns
/// its bytes. On Unix, validation and reading use the same open descriptor,
/// so a record that another session replaces or removes meanwhile yields
/// either a complete record or `NotFound`.
pub fn read_private_file(path: &str) -> Result<Vec<u8>> {
    privacy::read_private_record(path)
}

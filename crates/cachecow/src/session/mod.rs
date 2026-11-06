// SPDX-License-Identifier: Apache-2.0
//! Binds durable cache leases to the native process supervisor (Go
//! `internal/session`), including authenticated nested-session IPC.

use std::{
    cell::RefCell,
    collections::{BTreeMap, BTreeSet, HashMap},
    io::Write,
    rc::Rc,
    sync::{Arc, Condvar, Mutex, PoisonError},
    time::{Duration, Instant},
};

use anyhow::{Result, anyhow};
use serde::{Deserialize, Serialize};

use crate::{
    Context, error,
    ipc::{Connection, Listener, ListenerCloser},
    lease::{self, Grant, LeaseError, Record, Registry, Resources, WaitObserver},
    lifecycle::{self, LifecycleResult, Pair},
    paths, process,
    process::{Command, RunResult, Scope, Streams, SupervisorHooks},
    state,
};

#[cfg(test)]
mod tests;

/// Environment variable carrying the nested-session capability.
pub const ENV: &str = "CCOW_SESSION";

/// Maximum size of one nested-session request.
const REQUEST_LIMIT: usize = 1024 * 1024;
/// Maximum size of one nested-session response.
const RESPONSE_LIMIT: usize = 16 * 1024 * 1024;

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
    /// Registry contention outlived its retry budget in the supervisor. The
    /// client restores [`LeaseError::RegistryBusy`] so it keeps its distinct
    /// remediation (#4583).
    #[serde(
        rename = "registryBusy",
        default,
        skip_serializing_if = "std::ops::Not::not"
    )]
    pub registry_busy: bool,
}

impl Response {
    fn failed(error: &anyhow::Error) -> Self {
        Self {
            error: error_text(error),
            source_failure: error::find::<SourceError>(error).is_some(),
            registry_busy: error::is(error, &LeaseError::RegistryBusy),
            ..Self::default()
        }
    }

    fn failure(&self) -> anyhow::Error {
        if self.source_failure {
            anyhow::Error::new(SourceError {
                message: self.error.clone(),
            })
        } else if self.registry_busy {
            anyhow::Error::new(LeaseError::RegistryBusy).context(self.error.clone())
        } else {
            anyhow!("{}", self.error)
        }
    }
}

/// An unusable source after acquiring its lease; clients offer the same
/// source-recovery choices as discovery failures.
#[derive(Debug, Clone, PartialEq, Eq, thiserror::Error)]
#[error("{message}")]
pub struct SourceError {
    pub message: String,
}

/// Nested-session capability rejected or unavailable.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
#[error("nested session authentication failed; use a live managed parent session")]
pub struct AuthError;

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

/// Renders an error with its causes, like Go's wrapped error strings. A cause
/// whose text is already part of the message is not repeated.
pub fn error_text(error: &anyhow::Error) -> String {
    crate::error::chain_text(error)
}

/// Replaces `key` in a `KEY=value` environment list.
pub fn with_env(env: &[String], key: &str, value: &str) -> Vec<String> {
    let prefix = format!("{key}=");
    let mut out: Vec<String> = env
        .iter()
        .filter(|v| !v.starts_with(&prefix))
        .cloned()
        .collect();
    out.push(format!("{prefix}{value}"));
    out
}

struct NestedRecord {
    pid: i64,
    plan: Plan,
    done: bool,
    code: i32,
}

#[derive(Default)]
struct ServerState {
    tickets: HashMap<String, NestedRecord>,
    pending: BTreeMap<String, Plan>,
    ambiguous: BTreeSet<String>,
}

/// Counts in-flight IPC handlers so quiescence can await them.
#[derive(Default)]
struct InFlight {
    count: Mutex<usize>,
    idle: Condvar,
}

impl InFlight {
    fn enter(self: &Arc<Self>) -> InFlightGuard {
        *self.count.lock().unwrap_or_else(PoisonError::into_inner) += 1;
        InFlightGuard(self.clone())
    }

    fn wait(&self) {
        let mut count = self.count.lock().unwrap_or_else(PoisonError::into_inner);
        while *count > 0 {
            count = self
                .idle
                .wait(count)
                .unwrap_or_else(PoisonError::into_inner);
        }
    }
}

struct InFlightGuard(Arc<InFlight>);

impl Drop for InFlightGuard {
    fn drop(&mut self) {
        let mut count = self.0.count.lock().unwrap_or_else(PoisonError::into_inner);
        *count -= 1;
        if *count == 0 {
            self.0.idle.notify_all();
        }
    }
}

struct Ipc {
    closer: ListenerCloser,
    serve: std::thread::JoinHandle<()>,
    in_flight: Arc<InFlight>,
}

/// Supervisor-side session state shared by the hooks and IPC handlers.
struct Server {
    state: Mutex<ServerState>,
    records: RecordHooks,
    validate: Validate,
    root: String,
    scope: Scope,
    session: lease::Session,
    token: Mutex<String>,
    ipc: Mutex<Option<Ipc>>,
    // Keeps the registry connection alive for the session's lifetime.
    _registry: Option<Arc<Registry>>,
}

/// Reconciles conflicting records with native journal evidence and reports
/// unrecoverable conflicts on the user's terminal (#4582).
struct JournalObserver {
    stderr: Option<Arc<std::fs::File>>,
}

impl WaitObserver for JournalObserver {
    fn verify(&self, _ctx: &Context, record: &Record) -> Result<()> {
        let scope = process::load_scope(&record.ownership)?;
        process::reconcile(&scope)
    }

    fn blocked(&self, cache_path: &str, record: &Record) {
        tracing::debug!(
            cache = cache_path,
            record = %record.id,
            journal = %record.ownership,
            "cache lease is held by another managed process tree"
        );
        let notice = format!(
            "ccow: cache {cache_path} is held by lease {} (journal {}); this command waits until \
             the owner finishes. Run ccow doctor --verbose for details.\n",
            record.id, record.ownership
        );
        if let Some(stderr) = &self.stderr {
            let _ = (&**stderr).write_all(notice.as_bytes());
        } else {
            let _ = std::io::stderr().write_all(notice.as_bytes());
        }
    }
}

fn result_path(scope: &Scope) -> String {
    paths::join2(&scope.directory, "result.json")
}

impl Server {
    fn new(
        scope: Scope,
        registry: Option<Arc<Registry>>,
        session: lease::Session,
        validate: Validate,
        records: RecordHooks,
        root: String,
    ) -> Self {
        Self {
            state: Mutex::new(ServerState::default()),
            records,
            validate,
            root,
            scope,
            session,
            token: Mutex::new(String::new()),
            ipc: Mutex::new(None),
            _registry: registry,
        }
    }

    /// Applies one plan under the session lock. `nested` requests release the
    /// resources they newly acquired when they fail after admission (#4579).
    fn apply(&self, ctx: &Context, mut plan: Plan, nested: bool) -> Result<LifecycleResult> {
        let mut state = self.state.lock().unwrap_or_else(PoisonError::into_inner);
        plan.prepared_all = false;
        let result = self.apply_locked(ctx, &plan, nested);
        if let Ok(result) = &result {
            plan.prepared_all = !plan.resources.destinations.is_empty()
                && result.trees.len() == plan.resources.destinations.len();
            if let (Some(old), Some(compatible)) =
                (state.pending.get(&plan.record), self.records.compatible)
                && !compatible(old, &plan)
            {
                state.ambiguous.insert(plan.record.clone());
            }
            state.pending.insert(plan.record.clone(), plan);
        }
        result
    }

    fn apply_locked(&self, ctx: &Context, plan: &Plan, nested: bool) -> Result<LifecycleResult> {
        if plan.schema_version != 1 || plan.root != self.root {
            return Err(anyhow!("invalid nested state root"));
        }
        (self.validate)(plan)?;
        let grant = self
            .session
            .acquire(ctx, &plan.resources, plan.action != Action::Clean)?;
        let result = self.admitted(ctx, plan, &grant);
        match result {
            Err(e) if nested => Err(self.release_failed_grant(&grant, e)),
            other => other,
        }
    }

    /// Undoes a nested grant that failed before any command used it.
    fn release_failed_grant(&self, grant: &Grant, failure: anyhow::Error) -> anyhow::Error {
        if grant.new_paths.is_empty() {
            return failure;
        }
        let class = if error::find::<SourceError>(&failure).is_some() {
            "source"
        } else if error::find::<LeaseError>(&failure).is_some() {
            "lease"
        } else {
            "preparation"
        };
        match self.session.release_unstarted_grant(grant) {
            Ok(()) => {
                tracing::debug!(
                    session = %self.session.id(),
                    released_paths = grant.new_paths.len(),
                    error_class = class,
                    "released resources of a failed nested request"
                );
                failure
            }
            Err(release) => {
                tracing::warn!(
                    session = %self.session.id(),
                    released_paths = grant.new_paths.len(),
                    error_class = class,
                    error = %error_text(&release),
                    "cannot release resources of a failed nested request; they stay protected until the session finishes"
                );
                error::join([Some(failure), Some(release)])
                    .unwrap_or_else(|| anyhow!("release failed"))
            }
        }
    }

    fn admitted(&self, ctx: &Context, plan: &Plan, grant: &Grant) -> Result<LifecycleResult> {
        (self.validate)(plan)?;
        if let Some(validate_source) = self.records.validate_source {
            validate_source(plan).map_err(|e| {
                anyhow::Error::new(SourceError {
                    message: error_text(&e),
                })
            })?;
        }
        if grant.borrowed {
            if plan.action == Action::Clean || plan.replace {
                return Err(anyhow!("cannot clean or replace an active parent cache"));
            }
            if let Some(before) = self.records.before {
                before(plan)?;
            }
            return Ok(LifecycleResult {
                reused: true,
                ..LifecycleResult::default()
            });
        }
        // A partly borrowed resource set must not replace an outer cache
        // either.
        let new_paths: BTreeSet<&str> = grant.new_paths.iter().map(String::as_str).collect();
        if plan
            .resources
            .sources
            .iter()
            .any(|s| !new_paths.contains(s.as_str()))
        {
            return Err(anyhow!(
                "nested source cache is already writable by the parent scope; prepare the derived \
                 cache before starting the outer build"
            ));
        }
        if plan
            .resources
            .destinations
            .iter()
            .any(|d| !new_paths.contains(d.as_str()))
        {
            return Err(anyhow::Error::new(LeaseError::NestedConflict));
        }
        if let Some(before) = self.records.before {
            before(plan)?;
        }
        lifecycle::recover(&plan.record, &plan.pairs)?;
        match plan.action {
            Action::Clean => {
                for destination in &plan.resources.destinations {
                    lifecycle::exists(destination)?;
                    // Cloned caches can contain read-only directories (#4576).
                    lifecycle::remove_tree(destination)?;
                }
                Ok(LifecycleResult::default())
            }
            Action::ToolClean => Ok(LifecycleResult {
                reused: true,
                ..LifecycleResult::default()
            }),
            Action::Run | Action::Prepare => {
                let result = lifecycle::prepare(
                    ctx,
                    &plan.record,
                    &plan.pairs,
                    plan.replace,
                    plan.allow_copy,
                )?;
                let reads: Vec<String> = plan
                    .resources
                    .sources
                    .iter()
                    .filter(|s| {
                        new_paths.contains(s.as_str()) && !plan.resources.destinations.contains(s)
                    })
                    .cloned()
                    .collect();
                self.session.release_prepared_sources(&reads, grant)?;
                Ok(result)
            }
        }
    }

    fn handle(&self, mut conn: Connection) {
        let _ = conn.set_deadline(Some(Instant::now() + Duration::from_secs(30)));
        let auth = Response {
            error: AuthError.to_string(),
            ..Response::default()
        };
        let pid = match conn.peer_pid() {
            Ok(pid) if process::contains(&self.scope, pid) => pid,
            _ => {
                let _ = conn.send(&auth);
                return;
            }
        };
        let Ok(request) = conn.recv::<Request>(REQUEST_LIMIT) else {
            return;
        };
        let token = self
            .token
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .clone();
        if !constant_time_eq(request.token.as_bytes(), token.as_bytes()) {
            let _ = conn.send(&auth);
            return;
        }
        let _ = conn.set_deadline(None);
        let mut out = Response::default();
        if request.ticket.is_empty() {
            let plan = request.plan.clone();
            match self.apply(&self.scope.context, request.plan, true) {
                Ok(result) => {
                    out.result = result;
                    out.ticket = uuid::Uuid::now_v7().to_string();
                    self.state
                        .lock()
                        .unwrap_or_else(PoisonError::into_inner)
                        .tickets
                        .insert(
                            out.ticket.clone(),
                            NestedRecord {
                                pid,
                                plan,
                                done: false,
                                code: 0,
                            },
                        );
                }
                Err(e) => out = Response::failed(&e),
            }
        } else {
            let mut state = self.state.lock().unwrap_or_else(PoisonError::into_inner);
            match state.tickets.get_mut(&request.ticket) {
                Some(ticket) if ticket.pid == pid && !ticket.done => {
                    if request.cancel {
                        if let Err(e) = process::request_termination(self.scope.owner_pid) {
                            out.error = error_text(&e);
                        }
                    } else {
                        ticket.done = true;
                        ticket.code = request.exit_code;
                    }
                }
                _ => out.error = AuthError.to_string(),
            }
        }
        let _ = conn.send(&out);
    }

    /// Stops new IPC requests and awaits in-flight handlers.
    fn stop_ipc(&self) {
        let ipc = self
            .ipc
            .lock()
            .unwrap_or_else(PoisonError::into_inner)
            .take();
        if let Some(ipc) = ipc {
            ipc.closer.close();
            let _ = ipc.serve.join();
            ipc.in_flight.wait();
        }
    }

    fn start_ipc(self: &Arc<Self>, command: &mut Command) -> Result<()> {
        let prefix = if cfg!(windows) {
            "ccow-session-"
        } else {
            "ccow-s-"
        };
        let (listener, endpoint) = Listener::bind(prefix)?;
        let mut token = [0u8; 32];
        getrandom::fill(&mut token).map_err(|e| anyhow!("cannot create session token: {e}"))?;
        let token = hex::encode(token);
        *self.token.lock().unwrap_or_else(PoisonError::into_inner) = token.clone();
        command.env = with_env(&command.env, ENV, &format!("{endpoint}|{token}"));
        let closer = listener.closer();
        let in_flight = Arc::new(InFlight::default());
        let server = self.clone();
        let tracker = in_flight.clone();
        let serve = std::thread::spawn(move || {
            while let Ok(conn) = listener.accept() {
                let guard = tracker.enter();
                let server = server.clone();
                std::thread::spawn(move || {
                    let _guard = guard;
                    server.handle(conn);
                });
            }
            listener.close();
        });
        *self.ipc.lock().unwrap_or_else(PoisonError::into_inner) = Some(Ipc {
            closer,
            serve,
            in_flight,
        });
        Ok(())
    }
}

fn constant_time_eq(a: &[u8], b: &[u8]) -> bool {
    if a.len() != b.len() {
        return false;
    }
    a.iter().zip(b).fold(0u8, |acc, (x, y)| acc | (x ^ y)) == 0
}

/// Completion is already durable, but a different session can hold the
/// registry transaction briefly. Keep ownership until deletion commits; only
/// registry contention is retryable (#4583).
fn finish_session(session: &lease::Session, verify: lease::Verify<'_>) -> Result<()> {
    loop {
        match session.finish(&Context::background(), verify) {
            Err(e) if error::is(&e, &LeaseError::RegistryBusy) => {
                std::thread::sleep(Duration::from_millis(25));
            }
            other => return other,
        }
    }
}

/// One user-facing notice line: `ccow: ` prefix, ANSI-colored when `color` is
/// set. Shared by the CLI and the supervisor so both print the same format.
pub(crate) fn notice_line(message: &str, color: bool) -> String {
    if color {
        format!("\x1b[36mccow:\x1b[0m {message}\n")
    } else {
        format!("ccow: {message}\n")
    }
}

/// Whether notices are colored for a process environment: colored unless
/// `NO_COLOR` is set (to any value).
pub(crate) fn color_enabled(env: &[String]) -> bool {
    !env.iter()
        .any(|entry| entry == "NO_COLOR" || entry.starts_with("NO_COLOR="))
}

/// Notice lines reporting ordinary copying, one per published tree that used
/// it. Empty when every tree was cloned.
pub(crate) fn copy_notice_lines(result: &LifecycleResult, color: bool) -> Vec<String> {
    result
        .trees
        .iter()
        .filter(|tree| tree.copied_files > 0)
        .map(|tree| {
            notice_line(
                &format!(
                    "Ordinary copying was used for {} files (--allow-copy).",
                    tree.copied_files
                ),
                color,
            )
        })
        .collect()
}

/// Reports ordinary copying on the user's stderr as received by the
/// supervisor, falling back to the process stderr. Called before the managed
/// command starts so long-running commands do not hide the copy mode. The
/// supervisor does not share the caller's environment, so `color` comes from
/// the command's environment.
fn report_copy(scope: &Scope, result: &LifecycleResult, color: bool) {
    for line in copy_notice_lines(result, color) {
        match &scope.stderr {
            Some(stderr) => {
                let _ = (&**stderr).write_all(line.as_bytes());
            }
            None => {
                let _ = std::io::stderr().write_all(line.as_bytes());
            }
        }
    }
}

/// Supervisor hooks that bind leases, preparation and nested IPC.
pub fn hooks(validate: Validate, records: RecordHooks) -> SupervisorHooks {
    let shared: Rc<RefCell<Option<Arc<Server>>>> = Rc::new(RefCell::new(None));
    let before_shared = shared.clone();
    let quiesce_shared = shared.clone();
    let drained_shared = shared;
    SupervisorHooks {
        ready: Some(Box::new(|scope: &Scope| {
            lease::ensure_private_directory(&scope.directory)
        })),
        before_start: Some(Box::new(move |scope: &Scope, command: &mut Command| {
            let plan: Plan = state::read(&paths::join2(&scope.directory, "operation.json"))?;
            let fail = |e: anyhow::Error| -> anyhow::Error {
                let _ = state::write(&result_path(scope), &Response::failed(&e));
                e
            };
            if plan.schema_version != 1 || paths::dir(&paths::dir(&scope.directory)) != plan.root {
                return Err(fail(anyhow!("invalid supervisor state root")));
            }
            validate(&plan).map_err(fail)?;
            let leases = paths::join2(&plan.root, "leases");
            let registry = if plan.action == Action::Clean {
                Registry::open(&scope.context, &leases)
            } else {
                Registry::open_wait(&scope.context, &leases)
            }
            .map_err(fail)?;
            let session = registry.new_session(&scope.directory).map_err(fail)?;
            session.set_observer(Arc::new(JournalObserver {
                stderr: scope.stderr.clone(),
            }));
            let server = Arc::new(Server::new(
                scope.clone(),
                Some(registry),
                session,
                validate,
                records,
                plan.root.clone(),
            ));
            *before_shared.borrow_mut() = Some(server.clone());
            let result = server.apply(&scope.context, plan, false).map_err(fail)?;
            report_copy(scope, &result, color_enabled(&command.env));
            state::write(
                &result_path(scope),
                &Response {
                    result,
                    ..Response::default()
                },
            )?;
            if command.noop {
                return Ok(());
            }
            server.start_ipc(command).map_err(fail)
        })),
        quiesce: Some(Box::new(move |scope: &Scope| {
            let Some(server) = quiesce_shared.borrow().clone() else {
                return Ok(());
            };
            server.stop_ipc();
            let mut state = server.state.lock().unwrap_or_else(PoisonError::into_inner);
            let unfinished: Vec<String> = state
                .tickets
                .values()
                .filter(|t| !t.done || t.code != 0)
                .map(|t| t.plan.record.clone())
                .collect();
            state.ambiguous.extend(unfinished);
            if let Some(after) = server.records.after {
                for (key, plan) in &state.pending {
                    if !state.ambiguous.contains(key) {
                        after(plan, scope.exit_code)?;
                    }
                }
            }
            Ok(())
        })),
        drained: Some(Box::new(move |scope: &Scope| {
            let Some(server) = drained_shared.borrow_mut().take() else {
                return Ok(());
            };
            let directory = scope.directory.clone();
            let completed = scope.clone();
            finish_session(&server.session, &move |_ctx: &Context, record: &Record| {
                if record.ownership != directory {
                    return Err(anyhow::Error::new(LeaseError::Unverified));
                }
                process::reconcile(&completed)
            })
        })),
    }
}

/// Runs a top-level plan under a new native supervisor.
pub fn execute(
    ctx: &Context,
    plan: &Plan,
    command: Command,
    streams: Streams,
) -> (RunResult, LifecycleResult, Result<()>) {
    let mut owned =
        |scope: &Scope| state::write(&paths::join2(&scope.directory, "operation.json"), plan);
    let mut drained = |_: &Scope| Ok(());
    let (run, mut result) = process::run(
        ctx,
        &paths::join2(&plan.root, "processes"),
        command,
        streams,
        process::Hooks {
            owned: &mut owned,
            drained: &mut drained,
        },
    );
    let mut prepared = LifecycleResult::default();
    if !run.scope.directory.is_empty()
        && let Ok(out) = state::read::<Response>(&result_path(&run.scope))
    {
        prepared = out.result.clone();
        if !out.error.is_empty() {
            result = Err(out.failure());
        }
    }
    (run, prepared, result)
}

#[derive(Debug, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Request {
    #[serde(rename = "Cancel", default)]
    cancel: bool,
    #[serde(rename = "Ticket", default)]
    ticket: String,
    #[serde(rename = "ExitCode", default)]
    exit_code: i32,
    #[serde(rename = "Token", default)]
    token: String,
    #[serde(rename = "Plan", default)]
    plan: Plan,
}

fn auth_failure(detail: impl std::fmt::Display) -> anyhow::Error {
    anyhow::Error::new(AuthError).context(format!("{AuthError}: {detail}"))
}

fn exchange(ctx: &Context, capability: &str, mut request: Request) -> Result<Response> {
    let Some((endpoint, token)) = capability.split_once('|') else {
        return Err(anyhow::Error::new(AuthError));
    };
    if token.len() != 64 {
        return Err(anyhow::Error::new(AuthError));
    }
    request.token = token.to_string();
    let mut conn = Connection::dial(endpoint, Duration::from_secs(10))
        .map_err(|e| auth_failure(error_text(&e)))?;
    let closer = conn.closer();
    let _stop = ctx.after_func(move || closer.close());
    conn.send(&request)
        .map_err(|e| auth_failure(error_text(&e)))?;
    let out: Response = conn.recv(RESPONSE_LIMIT)?;
    if !out.error.is_empty() {
        return Err(out.failure());
    }
    Ok(out)
}

/// Sends a nested plan to the authenticated outer session.
pub fn nested(ctx: &Context, capability: &str, plan: &Plan) -> Result<Response> {
    exchange(
        ctx,
        capability,
        Request {
            plan: plan.clone(),
            ..Request::default()
        },
    )
}

/// Asks the authenticated native owner to drain the entire session. Shared
/// outer protection remains held until native whole-tree completion.
pub fn cancel(ctx: &Context, capability: &str, ticket: &str) -> Result<()> {
    if ticket.is_empty() {
        return Err(anyhow::Error::new(AuthError));
    }
    exchange(
        ctx,
        capability,
        Request {
            ticket: ticket.to_string(),
            cancel: true,
            ..Request::default()
        },
    )
    .map(|_| ())
}

/// Reports a nested command's completion.
pub fn complete(ctx: &Context, capability: &str, ticket: &str, code: i32) -> Result<()> {
    exchange(
        ctx,
        capability,
        Request {
            ticket: ticket.to_string(),
            exit_code: code,
            ..Request::default()
        },
    )
    .map(|_| ())
}

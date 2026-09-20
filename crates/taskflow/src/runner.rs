use std::{
    collections::{BTreeMap, BTreeSet},
    fs::File,
    io::Write,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

use anyhow::{ensure, Context, Result};
use futures::{stream::FuturesUnordered, StreamExt};
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncRead, AsyncReadExt};
use tokio_util::sync::CancellationToken;

use crate::{
    cache,
    config::{self, Executor, Readiness},
    environment::{Environment, Redactor},
    files,
    graph::Graph,
    plan::{Cause, Plan},
    process::{ExitReason, OwnedProcess, ProcessExit},
    remote::Remote,
};

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum Outcome {
    Executed,
    LocalCache,
    RemoteCache,
    Restored,
    Suppressed,
    Ready,
    Failed,
    Blocked,
    Cancelled,
    Invalidated,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct Receipt {
    pub version: u32,
    pub task: String,
    pub execution: String,
    pub outcome: Outcome,
    pub changed: bool,
    pub key: String,
    pub output: String,
    pub causes: BTreeSet<Cause>,
    pub duration_ms: u64,
    pub exit_code: i32,
    pub diagnostic: Option<String>,
}
impl Receipt {
    pub fn success(&self) -> bool {
        matches!(
            self.outcome,
            Outcome::Executed
                | Outcome::LocalCache
                | Outcome::RemoteCache
                | Outcome::Restored
                | Outcome::Suppressed
                | Outcome::Ready
        )
    }

    fn skipped(task: &str, outcome: Outcome, causes: BTreeSet<Cause>) -> Self {
        Self {
            version: 1,
            task: task.into(),
            execution: uuid::Uuid::now_v7().to_string(),
            outcome,
            changed: false,
            key: String::new(),
            output: String::new(),
            causes,
            duration_ms: 0,
            exit_code: match outcome {
                Outcome::Suppressed | Outcome::Ready => 0,
                Outcome::Cancelled => 130,
                _ => 1,
            },
            diagnostic: None,
        }
    }
}
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct RunResult {
    pub version: u32,
    pub success: bool,
    pub results: BTreeMap<String, Receipt>,
}

pub struct Services {
    pub controls: Mutex<BTreeMap<String, CancellationToken>>,
    pub events: tokio::sync::mpsc::UnboundedSender<(String, ProcessExit)>,
    pub joins: Mutex<Vec<tokio::task::JoinHandle<Result<()>>>>,
}
impl Services {
    pub async fn shutdown(&self) -> Result<()> {
        for token in self.controls.lock().unwrap().values() {
            token.cancel();
        }
        let joins = std::mem::take(&mut *self.joins.lock().unwrap());
        let mut failures = 0;
        for join in joins {
            if !matches!(join.await, Ok(Ok(()))) {
                failures += 1;
                tracing::error!(
                    code = "service-cleanup-failed",
                    "Service owner could not confirm cleanup"
                );
            }
        }
        self.controls.lock().unwrap().clear();
        ensure!(
            failures == 0,
            "{failures} service owner(s) could not confirm cleanup"
        );
        Ok(())
    }
}
#[derive(Clone)]
pub struct RunOptions {
    pub jobs: usize,
    pub force: bool,
    pub env: BTreeMap<String, String>,
    pub no_dotenv: bool,
    pub show_secrets: bool,
    pub quiet: bool,
    pub provided: BTreeMap<String, Receipt>,
    pub services: Option<Arc<Services>>,
    pub task_cancellations: Arc<Mutex<BTreeMap<String, CancellationToken>>>,
    /// Receipts become visible only after the task releases execution
    /// ownership.
    pub completions: Option<tokio::sync::mpsc::UnboundedSender<Receipt>>,
    pub shard: Option<(usize, usize)>,
    pub os: Option<config::Os>,
    pub arch: Option<config::Arch>,
}
impl Default for RunOptions {
    fn default() -> Self {
        Self {
            jobs: std::thread::available_parallelism().map_or(1, usize::from),
            force: false,
            env: BTreeMap::new(),
            no_dotenv: false,
            show_secrets: false,
            quiet: false,
            provided: BTreeMap::new(),
            services: None,
            task_cancellations: Arc::new(Mutex::new(BTreeMap::new())),
            completions: None,
            shard: None,
            os: None,
            arch: None,
        }
    }
}

pub fn validate_shard_selection(graph: &Graph, plan: &Plan, options: &RunOptions) -> Result<()> {
    if let Some((index, count)) = options.shard {
        ensure!(count > 0 && index < count, "invalid shard index/count");
        let pending: Vec<_> = plan
            .order
            .iter()
            .filter(|id| !options.provided.contains_key(*id))
            .collect();
        if !pending.is_empty() {
            let shards: Vec<_> = pending
                .iter()
                .filter_map(|id| graph.tasks[*id].task.shard.as_ref())
                .collect();
            ensure!(
                !shards.is_empty(),
                "--shard requires a selected task with shard configuration"
            );
            ensure!(
                shards.iter().all(|shard| shard.count == count),
                "--shard count must match every selected sharded task"
            );
        }
    }
    Ok(())
}

pub async fn run_plan(
    graph: Arc<Graph>,
    plan: Plan,
    options: RunOptions,
    cancel: CancellationToken,
) -> Result<RunResult> {
    ensure!(
        plan.generation == graph.workspace.generation,
        "execution plan is stale"
    );
    validate_shard_selection(&graph, &plan, &options)?;
    for id in &plan.order {
        ensure!(
            !graph.unresolved.contains(id),
            "{id}: native selector cannot execute with unresolved metadata; run its installation \
             prerequisite first"
        );
        ensure!(
            !graph.tasks[id].task.service || options.services.is_some(),
            "service tasks require tflow start"
        );
    }
    let mut pending: BTreeSet<_> = plan.order.iter().cloned().collect();
    let mut results = BTreeMap::new();
    for (id, receipt) in &options.provided {
        if pending.remove(id) {
            record_completion(&mut results, &options, receipt.clone());
        }
    }
    let mut active = FuturesUnordered::new();
    while !pending.is_empty() || !active.is_empty() {
        if cancel.is_cancelled() {
            for id in std::mem::take(&mut pending) {
                record_completion(
                    &mut results,
                    &options,
                    Receipt::skipped(&id, Outcome::Cancelled, plan.causes[&id].clone()),
                );
            }
        }
        let mut eligible: Vec<_> = pending
            .iter()
            .filter(|id| {
                graph
                    .prerequisites(id)
                    .iter()
                    .all(|p| results.contains_key(p))
            })
            .cloned()
            .collect();
        eligible.sort_by_key(|id| {
            (
                std::cmp::Reverse(critical_duration(&graph, id, &mut BTreeSet::new())),
                id.clone(),
            )
        });
        for id in eligible {
            if active.len() >= options.jobs.max(1) {
                break;
            }
            pending.remove(&id);
            let prerequisites: BTreeMap<_, _> = graph
                .prerequisites(&id)
                .into_iter()
                .map(|p| (p.clone(), results[&p].clone()))
                .collect();
            if prerequisites.values().any(|r| !r.success()) {
                record_completion(
                    &mut results,
                    &options,
                    Receipt::skipped(&id, Outcome::Blocked, plan.causes[&id].clone()),
                );
                continue;
            }
            let causes = &plan.causes[&id];
            let previous = previous(&graph.workspace.root, &id);
            let own_outputs_invalid = !graph.tasks[&id]
                .task
                .output
                .as_ref()
                .is_none_or(Vec::is_empty)
                && !cache::output_state(
                    &graph.workspace.projects[&graph.tasks[&id].project],
                    &graph.tasks[&id].task,
                )
                .is_ok_and(|digest| {
                    previous
                        .as_ref()
                        .is_some_and(|r| r.success() && r.output == digest)
                });
            if !causes.iter().any(Cause::independent)
                && !prerequisites.values().any(|r| r.changed)
                && !own_outputs_invalid
                && previous.as_ref().is_some_and(Receipt::success)
            {
                let mut receipt = Receipt::skipped(&id, Outcome::Suppressed, causes.clone());
                if let Some(previous) = previous {
                    receipt.key = previous.key;
                    receipt.output = previous.output;
                }
                record_completion(&mut results, &options, receipt);
                continue;
            }
            let graph = graph.clone();
            let causes = causes.clone();
            let options = options.clone();
            let token = cancel.child_token();
            options
                .task_cancellations
                .lock()
                .unwrap()
                .insert(id.clone(), token.clone());
            active.push(async move {
                let result = run_task(
                    &graph,
                    &id,
                    causes.clone(),
                    &prerequisites,
                    &options,
                    token.clone(),
                )
                .await;
                options.task_cancellations.lock().unwrap().remove(&id);
                let receipt = match result {
                    Ok(receipt) => receipt,
                    Err(error) => {
                        // Setup can be cancelled before there is a normal process
                        // exit. Unverified cleanup still overrides cancellation.
                        let cancelled = token.is_cancelled()
                            && !error.is::<crate::docker::CleanupFailure>()
                            && !error.is::<crate::process::CleanupFailure>()
                            && !error.is::<cache::PublicationRollbackFailure>();
                        // Detailed native stderr already passes through the masker;
                        // engine errors contain identifiers and stable context only.
                        let node = &graph.tasks[&id];
                        if node.task.service {
                            if let Some(services) = &options.services {
                                let _ = services.events.send((
                                    id.clone(),
                                    ProcessExit {
                                        code: if cancelled { 130 } else { 1 },
                                        reason: if cancelled {
                                            ExitReason::Cancelled
                                        } else {
                                            ExitReason::Completed
                                        },
                                    },
                                ));
                            }
                        }
                        let secrets = Environment::build(
                            &graph.workspace,
                            &graph.workspace.projects[&node.project],
                            &node.task,
                            &options.env,
                            options.no_dotenv,
                        )
                        .map(|e| e.secrets)
                        .unwrap_or_default();
                        let message = String::from_utf8_lossy(&Redactor::mask(
                            error.to_string().as_bytes(),
                            secrets,
                        ))
                        .into_owned();
                        let outcome = if cancelled {
                            Outcome::Cancelled
                        } else {
                            Outcome::Failed
                        };
                        if cancelled {
                            tracing::info!(task = %id, outcome = ?outcome, "Task setup cancelled");
                        } else {
                            tracing::error!(task = %id, error = %message, "Task failed");
                        }
                        let mut receipt = Receipt::skipped(&id, outcome, causes);
                        receipt.exit_code = if cancelled { 130 } else { 1 };
                        receipt.diagnostic = Some(message);
                        receipt
                    }
                };
                (id, receipt)
            });
        }
        if let Some((_, receipt)) = active.next().await {
            record_completion(&mut results, &options, receipt);
        } else if !pending.is_empty() {
            // Skipped/blocked nodes may make another layer eligible without any
            // active child. The validated DAG guarantees progress on the next pass.
            ensure!(
                pending.iter().any(|id| graph
                    .prerequisites(id)
                    .iter()
                    .all(|p| results.contains_key(p))),
                "scheduler cannot satisfy prerequisites"
            );
        }
    }
    Ok(RunResult {
        version: 1,
        success: results.values().all(Receipt::success) && !cancel.is_cancelled(),
        results,
    })
}

fn record_completion(
    results: &mut BTreeMap<String, Receipt>,
    options: &RunOptions,
    receipt: Receipt,
) {
    if let Some(completions) = &options.completions {
        let _ = completions.send(receipt.clone());
    }
    results.insert(receipt.task.clone(), receipt);
}

fn critical_duration(graph: &Graph, id: &str, seen: &mut BTreeSet<String>) -> u64 {
    if !seen.insert(id.into()) {
        return 0;
    }
    let duration = previous(&graph.workspace.root, id).map_or(1, |r| r.duration_ms.max(1))
        + graph
            .dependents(id)
            .iter()
            .map(|next| critical_duration(graph, next, seen))
            .max()
            .unwrap_or(0);
    // Guard only the current ancestry. A diamond's shared suffix contributes
    // to every alternative path, including a longer branch visited later.
    seen.remove(id);
    duration
}
pub fn receipt_path(root: &Path, id: &str) -> PathBuf {
    root.join(".taskflow/results")
        .join(format!("{}.json", files::digest(id.as_bytes())))
}
pub fn previous(root: &Path, id: &str) -> Option<Receipt> {
    std::fs::read(receipt_path(root, id))
        .ok()
        .and_then(|b| serde_json::from_slice(&b).ok())
}

async fn run_task(
    graph: &Graph,
    id: &str,
    causes: BTreeSet<Cause>,
    prerequisites: &BTreeMap<String, Receipt>,
    options: &RunOptions,
    cancel: CancellationToken,
) -> Result<Receipt> {
    let node = &graph.tasks[id];
    let project = &graph.workspace.projects[&node.project];
    let task = &node.task;
    if task.service {
        if let Some(services) = &options.services {
            if services.controls.lock().unwrap().contains_key(id) {
                return Ok(Receipt::skipped(id, Outcome::Ready, causes));
            }
        }
    }
    if task.platform.executor == Executor::Host {
        ensure!(
            task.platform.resolved() == (config::host_os(), config::host_arch()),
            "task platform does not match host"
        );
    }
    let locks = acquire_locks(&graph.workspace.root, id, &task.resources, &cancel).await?;
    let old = previous(&graph.workspace.root, id).filter(Receipt::success);
    // Hold the old baseline only inside this attempt. A failed or interrupted
    // replacement must not leave an earlier success eligible for suppression.
    // The task lock serializes invalidation with execution and publication.
    match std::fs::remove_file(receipt_path(&graph.workspace.root, id)) {
        Ok(()) => {}
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {}
        Err(error) => return Err(error).context("cannot invalidate previous task receipt"),
    }
    let started = Instant::now();
    let environment = Environment::build(
        &graph.workspace,
        project,
        task,
        &options.env,
        options.no_dotenv,
    )?;
    let inputs = files::input_state(&graph.workspace, project, task)?;
    let mut tools = BTreeMap::new();
    for (name, command) in &task.tools {
        let identity = crate::process::tool_identity(
            &graph.workspace.root,
            &project.directory,
            task,
            command,
            &environment.values,
            &options.env,
            &cancel,
        )
        .await?;
        tools.insert(name, identity);
    }
    let prerequisite_outputs: BTreeMap<_, _> = prerequisites
        .iter()
        .map(|(id, r)| (id, &r.output))
        .collect();
    let result_key = files::digest(&serde_json::to_vec(&(
        1,
        id,
        task,
        &inputs,
        &environment.fingerprint,
        &tools,
        &prerequisite_outputs,
        task.platform.key(),
        None::<(usize, usize)>,
    ))?);
    let key = if task.shard.is_some() && options.shard.is_some() {
        files::digest(&serde_json::to_vec(&(&result_key, options.shard))?)
    } else {
        result_key.clone()
    };
    let remote = if task.cache {
        graph
            .workspace
            .config
            .remote
            .as_ref()
            .and_then(|c| match Remote::new(c) {
                Ok(remote) => remote,
                Err(_) => {
                    tracing::warn!(
                        task = id,
                        code = "remote-unavailable",
                        "Remote cache unavailable; using local execution"
                    );
                    None
                }
            })
    } else {
        None
    };
    if task.cache && !options.force && !cancel.is_cancelled() {
        let valid_artifact = |artifact: &cache::Artifact| {
            let valid_shards = match (&task.shard, &artifact.shards) {
                (None, None) => true,
                (Some(config), Some((inventory, reports))) => {
                    crate::shard::validate_reports(inventory, config.count, reports).is_ok_and(
                        |passed| {
                            passed
                                && match options.shard {
                                    Some((index, count)) => {
                                        reports.len() == 1
                                            && reports[0].index == index
                                            && reports[0].count == count
                                    }
                                    None => reports.len() == config.count,
                                }
                        },
                    )
                }
                _ => false,
            };
            valid_shards && artifact.validate(&key, id, project, task).is_ok()
        };
        let mut source = Outcome::LocalCache;
        let mut artifact = match cache::load(&graph.workspace.root, &key) {
            Ok(value) => value,
            Err(_) => {
                tracing::warn!(
                    task = id,
                    code = "cache-corrupt",
                    "Ignoring invalid local cache entry"
                );
                None
            }
        };
        artifact = artifact.filter(|artifact| {
            let valid = valid_artifact(artifact);
            if !valid {
                tracing::warn!(
                    task = id,
                    code = "cache-invalid",
                    "Ignoring incompatible local artifact before remote lookup"
                );
            }
            valid
        });
        if artifact.is_none() {
            if let Some(remote) = &remote {
                let fetched = tokio::select! {
                    biased;
                    _ = cancel.cancelled() => anyhow::bail!("task cancelled during cache lookup"),
                    result = remote.get(&key) => result,
                };
                match fetched {
                    Ok(value) => {
                        artifact = value;
                        source = Outcome::RemoteCache;
                    }
                    Err(_) => tracing::warn!(
                        task = id,
                        code = "remote-read-failed",
                        "Remote cache unavailable or invalid"
                    ),
                }
            }
        }
        if let Some(artifact) = artifact {
            if source == Outcome::LocalCache || valid_artifact(&artifact) {
                ensure!(
                    !cancel.is_cancelled(),
                    "task cancelled before cache restoration"
                );
                let before = cache::output_state(project, task).ok();
                if before.as_deref() != Some(&artifact.output_digest) {
                    ensure!(
                        files::input_state(&graph.workspace, project, task)? == inputs,
                        "inputs changed before cache restoration"
                    );
                    artifact.restore(&key, id, project, task)?;
                    source = Outcome::Restored;
                }
                ensure!(
                    files::input_state(&graph.workspace, project, task)? == inputs,
                    "inputs changed during cache restoration"
                );
                let receipt = Receipt {
                    version: 1,
                    task: id.into(),
                    execution: uuid::Uuid::now_v7().to_string(),
                    outcome: source,
                    changed: old
                        .as_ref()
                        .is_none_or(|r| r.output != artifact.result_output()),
                    key,
                    output: artifact.result_output().into(),
                    causes,
                    duration_ms: started.elapsed().as_millis() as u64,
                    exit_code: 0,
                    diagnostic: None,
                };
                if let Some((inventory, reports)) = &artifact.shards {
                    crate::shard::write_reports(
                        &graph
                            .workspace
                            .root
                            .join(".taskflow/runs")
                            .join(&receipt.execution),
                        inventory,
                        reports,
                    )?;
                }
                // Restoration and report writes are synchronous. Cancellation
                // can arrive from a signal or session owner while they run.
                ensure!(!cancel.is_cancelled(), "task cancelled during cache reuse");
                ensure!(
                    cache::store_if(&graph.workspace.root, &artifact, || {
                        Ok(!cancel.is_cancelled()
                            && files::input_state(&graph.workspace, project, task)? == inputs)
                    })?,
                    "task invalidated during cache publication"
                );
                ensure!(
                    !cancel.is_cancelled(),
                    "task cancelled during cache publication"
                );
                persist(&graph.workspace.root, &receipt)?;
                ensure!(
                    !cancel.is_cancelled(),
                    "task cancelled before returning cached result"
                );
                tracing::info!(task = id, outcome = ?receipt.outcome, changed = receipt.changed, "Reused task result");
                return Ok(receipt);
            }
            tracing::warn!(
                task = id,
                code = "cache-invalid",
                "Ignoring incompatible cache artifact"
            );
        }
    }
    let execution = uuid::Uuid::now_v7().to_string();
    let run_directory = graph.workspace.root.join(".taskflow/runs").join(&execution);
    std::fs::create_dir_all(&run_directory)?;
    let result_file = run_directory.join("result.json");
    let mut values = environment.values.clone();
    crate::environment::insert(
        &mut values,
        "TFLOW_RESULT_FILE".into(),
        result_file.to_string_lossy().into_owned(),
    );
    crate::environment::insert(&mut values, "TFLOW_EXECUTION_ID".into(), execution.clone());
    if let Ok(exe) = std::env::current_exe() {
        crate::environment::insert(
            &mut values,
            "TFLOW_BIN".into(),
            exe.to_string_lossy().into_owned(),
        );
    }
    let log = Arc::new(Mutex::new(File::create(run_directory.join("output.log"))?));
    let mut docker = None;
    let mut command = task.command.clone();
    if task.platform.executor == Executor::Docker && task.shard.is_none() {
        values = crate::docker::host_environment(&values);
        let prepared = crate::docker::prepare(
            &graph.workspace.root,
            &project.directory,
            task,
            &values,
            &options.env,
            &execution,
            &cancel,
        )
        .await?;
        command = prepared.0;
        docker = Some(prepared.1);
    }
    if task.shard.is_some() {
        let mut receipt = crate::shard::execute(
            graph,
            id,
            &values,
            &environment.secrets,
            options,
            &cancel,
            &execution,
            &key,
            causes,
            log.clone(),
        )
        .await?;
        // Shard selection partitions cache storage, not the semantic identity
        // of the tested input version passed to downstream tasks and CI jobs.
        receipt.output = result_key;
        publish(
            graph,
            id,
            &mut receipt,
            &inputs,
            old.as_ref(),
            remote.as_ref(),
            &cancel,
        )
        .await?;
        return Ok(receipt);
    }
    let mut process = OwnedProcess::spawn(
        &project.directory,
        &command,
        task.shell.as_deref(),
        Some(&values),
    )?;
    let process_started = Instant::now();
    let timeout = task
        .timeout
        .as_ref()
        .map(|s| config::duration(s))
        .transpose()?;
    let log_failed = CancellationToken::new();
    let stdout = tokio::spawn(monitored_log(
        process.child.stdout.take().unwrap(),
        log.clone(),
        environment.secrets.clone(),
        options.show_secrets,
        options.quiet,
        log_failed.clone(),
    ));
    let stderr = tokio::spawn(monitored_log(
        process.child.stderr.take().unwrap(),
        log,
        environment.secrets.clone(),
        options.show_secrets,
        options.quiet,
        log_failed.clone(),
    ));
    tracing::info!(task = id, execution = %execution, causes = ?causes, "Task started");
    if task.service {
        if let Some(readiness) = &task.readiness {
            let readiness_cancel = cancel.child_token();
            let result = {
                let ready = wait_ready(
                    &mut process,
                    readiness,
                    task.shell.as_deref(),
                    &project.directory,
                    &values,
                    &readiness_cancel,
                    timeout.map(|limit| limit.saturating_sub(process_started.elapsed())),
                );
                tokio::pin!(ready);
                tokio::select! {
                    biased;
                    _ = log_failed.cancelled() => {
                        readiness_cancel.cancel();
                        // A failed logger must not drop an active readiness probe.
                        let _ = ready.await;
                        Err(anyhow::anyhow!("service output failed before readiness"))
                    }
                    result = &mut ready => result,
                }
            };
            if let Err(error) = result {
                let reaped = process.terminate().await;
                finish_logs_and_container(stdout, stderr, cleanup_container(docker)).await?;
                reaped?;
                return Err(error);
            }
        }
        let services = options.services.as_ref().unwrap().clone();
        // Services outlive the finite activation wave; their token is owned by the
        // session registry and explicitly cancelled during ownership reconciliation.
        let stop = CancellationToken::new();
        services
            .controls
            .lock()
            .unwrap()
            .insert(id.into(), stop.clone());
        let event_id = id.to_owned();
        let events = services.events.clone();
        let join = tokio::spawn(async move {
            let _locks = locks;
            let remaining =
                timeout.map(|duration| duration.saturating_sub(process_started.elapsed()));
            let completed = finish_service(
                &mut process,
                (stdout, stderr),
                &stop,
                &log_failed,
                remaining,
                cleanup_container(docker),
            )
            .await;
            let result = completed.as_ref().copied().unwrap_or(ProcessExit {
                code: 1,
                reason: ExitReason::Completed,
            });
            if result.reason == ExitReason::TimedOut {
                tracing::warn!(task = %event_id, code = "service-timeout", "Service exceeded its timeout");
            }
            if completed.is_err() {
                tracing::error!(task = %event_id, code = "service-owner-failed", "Service process, output, or container owner failed");
            }
            let _ = events.send((event_id, result));
            completed.map(|_| ())
        });
        services.joins.lock().unwrap().push(join);
        return Ok(Receipt {
            version: 1,
            task: id.into(),
            execution,
            outcome: Outcome::Ready,
            changed: true,
            output: key.clone(),
            key,
            causes,
            duration_ms: started.elapsed().as_millis() as u64,
            exit_code: 0,
            diagnostic: None,
        });
    }
    let waited = wait_with_log_failure(&mut process, &cancel, &log_failed, timeout).await;
    let reaped = if waited.is_err() {
        process.terminate().await
    } else {
        Ok(())
    };
    finish_logs_and_container(stdout, stderr, cleanup_container(docker)).await?;
    reaped?;
    let status = waited?;
    let stable = (task.install && !task.cache)
        || files::input_state(&graph.workspace, project, task)? == inputs;
    let mut receipt = Receipt {
        version: 1,
        task: id.into(),
        execution: execution.clone(),
        outcome: if status.reason == ExitReason::TimedOut {
            Outcome::Failed
        } else if status.cancelled() || cancel.is_cancelled() {
            Outcome::Cancelled
        } else if status.code != 0 {
            Outcome::Failed
        } else if !stable {
            Outcome::Invalidated
        } else {
            Outcome::Executed
        },
        changed: true,
        key,
        output: String::new(),
        causes,
        duration_ms: started.elapsed().as_millis() as u64,
        exit_code: status.code,
        diagnostic: (status.reason == ExitReason::TimedOut).then(|| "task timeout elapsed".into()),
    };
    if receipt.outcome == Outcome::Cancelled {
        receipt.exit_code = 130;
    }
    if receipt.success() {
        let unchanged = if result_file.exists() {
            let report: TaskReport = serde_json::from_slice(&std::fs::read(&result_file)?)?;
            ensure!(
                report.version == 1 && report.execution == execution,
                "task result belongs to another execution"
            );
            report.result == TaskReported::Unchanged
        } else {
            false
        };
        receipt.changed = !unchanged;
        receipt.output = if task.output.as_ref().is_some_and(|v| !v.is_empty()) {
            cache::output_state(project, task)?
        } else if unchanged {
            old.as_ref()
                .map_or(receipt.key.clone(), |r| r.output.clone())
        } else {
            receipt.key.clone()
        };
        publish(
            graph,
            id,
            &mut receipt,
            &inputs,
            old.as_ref(),
            remote.as_ref(),
            &cancel,
        )
        .await?;
    }
    tracing::info!(task = id, outcome = ?receipt.outcome, changed = receipt.changed, duration_ms = receipt.duration_ms, "Task finished");
    Ok(receipt)
}

async fn publish(
    graph: &Graph,
    id: &str,
    receipt: &mut Receipt,
    inputs: &BTreeMap<String, String>,
    baseline: Option<&Receipt>,
    remote: Option<&Remote>,
    cancel: &CancellationToken,
) -> Result<()> {
    let node = &graph.tasks[id];
    let project = &graph.workspace.projects[&node.project];
    let task = &node.task;
    let valid = |receipt: &mut Receipt| -> Result<bool> {
        if cancel.is_cancelled() {
            receipt.outcome = Outcome::Cancelled;
            receipt.exit_code = 130;
        } else if (!task.install || task.cache)
            && files::input_state(&graph.workspace, project, task)? != *inputs
        {
            receipt.outcome = Outcome::Invalidated;
        }
        Ok(receipt.success())
    };
    if !valid(receipt)? {
        return Ok(());
    }
    let previous_output = baseline
        .filter(|receipt| receipt.success())
        .map(|receipt| receipt.output.clone());
    if task.output.as_ref().is_some_and(|v| !v.is_empty()) {
        receipt.output = cache::output_state(project, task)?;
        // An unchanged report cannot override observed artifact changes. With
        // no successful baseline, consumers must see the newly produced output.
        receipt.changed |= previous_output.as_ref() != Some(&receipt.output);
    }
    if task.cache {
        let mut artifact = cache::Artifact::capture(receipt.key.clone(), id.into(), project, task)?;
        if task.shard.is_some() {
            artifact.shards = Some(crate::shard::read_reports(
                &graph
                    .workspace
                    .root
                    .join(".taskflow/runs")
                    .join(&receipt.execution),
            )?);
        }
        if artifact.files.is_empty() {
            // Checks may preserve an older semantic identity via unchanged.
            artifact.result_identity = Some(receipt.output.clone());
        } else {
            receipt.output = artifact.output_digest.clone();
            receipt.changed |= previous_output.as_ref() != Some(&receipt.output);
        }
        let staged = if let Some(remote) = remote {
            tokio::select! {
                biased;
                _ = cancel.cancelled() => None,
                result = remote.stage(&artifact) => match result {
                    Ok(digest) => digest,
                    Err(_) => { tracing::warn!(task = id, code = "remote-write-failed", "Remote cache object upload failed"); None }
                }
            }
        } else {
            None
        };
        if !valid(receipt)? {
            return Ok(());
        }
        if !cache::store_if(&graph.workspace.root, &artifact, || valid(receipt))? {
            return Ok(());
        }
        // The guarded local commit completes this task. Persist that decision
        // before exposing a remote entry: an in-flight PUT may succeed even if
        // its response is lost, so cancellation cannot retract this completion.
        persist(&graph.workspace.root, receipt)?;
        if let (Some(remote), Some(digest)) = (remote, staged) {
            if !cancel.is_cancelled() && remote.commit(&artifact.key, &digest).await.is_err() {
                tracing::warn!(
                    task = id,
                    code = "remote-write-failed",
                    "Remote cache publication failed"
                );
            }
        }
        return Ok(());
    }
    if valid(receipt)? {
        persist(&graph.workspace.root, receipt)?;
    }
    Ok(())
}

pub fn persist(root: &Path, receipt: &Receipt) -> Result<()> {
    files::atomic_write(
        &receipt_path(root, &receipt.task),
        &serde_json::to_vec(receipt)?,
    )
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TaskReport {
    pub version: u32,
    pub execution: String,
    pub result: TaskReported,
}
#[derive(Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum TaskReported {
    Unchanged,
}
pub fn report_unchanged() -> Result<()> {
    let path = std::env::var("TFLOW_RESULT_FILE")
        .context("result reporting requires a running TaskFlow task")?;
    let execution = std::env::var("TFLOW_EXECUTION_ID").context("execution identity missing")?;
    uuid::Uuid::parse_str(&execution)?;
    files::atomic_write(
        Path::new(&path),
        &serde_json::to_vec(&TaskReport {
            version: 1,
            execution,
            result: TaskReported::Unchanged,
        })?,
    )
}

pub async fn acquire_locks(
    root: &Path,
    id: &str,
    resources: &[String],
    cancel: &CancellationToken,
) -> Result<Vec<File>> {
    let names: BTreeSet<_> = std::iter::once(format!("task:{id}"))
        .chain(resources.iter().map(|s| format!("resource:{s}")))
        .collect();
    std::fs::create_dir_all(root.join(".taskflow/locks"))?;
    let mut held = vec![];
    for name in names {
        let path = root
            .join(".taskflow/locks")
            .join(files::digest(name.as_bytes()));
        let file = std::fs::OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .open(path)?;
        loop {
            match file.try_lock() {
                Ok(()) => break,
                Err(std::fs::TryLockError::WouldBlock) => {
                    tokio::select! { _ = cancel.cancelled() => anyhow::bail!("cancelled while waiting for resource"), _ = tokio::time::sleep(Duration::from_millis(25)) => {} }
                }
                Err(error) => return Err(error.into()),
            }
        }
        held.push(file);
    }
    Ok(held)
}

pub(crate) async fn cleanup_container(docker: Option<crate::docker::Container>) -> Result<()> {
    if let Some(mut docker) = docker {
        docker.cleanup().await?;
    }
    Ok(())
}

pub(crate) async fn finish_logs_and_container(
    stdout: tokio::task::JoinHandle<Result<()>>,
    stderr: tokio::task::JoinHandle<Result<()>>,
    cleanup: impl std::future::Future<Output = Result<()>>,
) -> Result<()> {
    // Collect every owner before returning an output error. A failed logger
    // must never replace awaited daemon verification with best-effort Drop.
    let stdout = stdout.await.context("task stdout owner failed");
    let stderr = stderr.await.context("task stderr owner failed");
    cleanup.await?;
    if stdout.as_ref().map_or(true, |result| result.is_err())
        || stderr.as_ref().map_or(true, |result| result.is_err())
    {
        tracing::error!(
            code = "task-output-drain-failed",
            "Task output failed after completing owned container cleanup"
        );
    }
    stdout??;
    stderr??;
    Ok(())
}

async fn wait_with_log_failure(
    process: &mut OwnedProcess,
    cancel: &CancellationToken,
    log_failed: &CancellationToken,
    timeout: Option<Duration>,
) -> Result<ProcessExit> {
    let stop = cancel.child_token();
    let waited = process.wait(&stop, timeout);
    tokio::pin!(waited);
    tokio::select! {
        biased;
        _ = log_failed.cancelled() => {
            stop.cancel();
            // Keep the same ownership future alive through termination/reaping.
            waited.await
        }
        result = &mut waited => result,
    }
}

async fn finish_service(
    process: &mut OwnedProcess,
    logs: (
        tokio::task::JoinHandle<Result<()>>,
        tokio::task::JoinHandle<Result<()>>,
    ),
    stop: &CancellationToken,
    log_failed: &CancellationToken,
    timeout: Option<Duration>,
    cleanup: impl std::future::Future<Output = Result<()>>,
) -> Result<ProcessExit> {
    let waited = wait_with_log_failure(process, stop, log_failed, timeout).await;
    let reaped = if waited.is_err() {
        process.terminate().await
    } else {
        Ok(())
    };
    let output = finish_logs_and_container(logs.0, logs.1, cleanup).await;
    reaped?;
    let status = waited?;
    output?;
    Ok(status)
}

async fn monitored_log(
    reader: impl AsyncRead + Unpin,
    file: Arc<Mutex<File>>,
    secrets: Vec<Vec<u8>>,
    show: bool,
    quiet: bool,
    failed: CancellationToken,
) -> Result<()> {
    // Cancellation on drop also covers panics/aborted log owners. A successful
    // EOF is normal and must leave a still-running service alone.
    let failure = failed.drop_guard();
    let result = stream_log(reader, file, secrets, show, quiet).await;
    if result.is_ok() {
        failure.disarm();
    }
    result
}

pub async fn stream_log(
    mut reader: impl AsyncRead + Unpin,
    file: Arc<Mutex<File>>,
    secrets: Vec<Vec<u8>>,
    show: bool,
    quiet: bool,
) -> Result<()> {
    let mut masker = Redactor::new(secrets);
    let mut block = [0; 8192];
    loop {
        let n = reader.read(&mut block).await?;
        let masked = masker.push(&block[..n], n == 0);
        file.lock().unwrap().write_all(&masked)?;
        if !quiet {
            let live = if show { &block[..n] } else { &masked[..] };
            std::io::stderr().lock().write_all(live)?;
        }
        if n == 0 {
            break;
        }
    }
    Ok(())
}

async fn wait_ready(
    process: &mut OwnedProcess,
    readiness: &Readiness,
    shell: Option<&[String]>,
    directory: &Path,
    environment: &BTreeMap<String, String>,
    cancel: &CancellationToken,
    service_remaining: Option<Duration>,
) -> Result<()> {
    let timeout = match readiness {
        Readiness::Tcp { timeout, .. }
        | Readiness::Http { timeout, .. }
        | Readiness::Command { timeout, .. } => config::duration(timeout)?,
    };
    let deadline = tokio::time::Instant::now()
        + service_remaining.map_or(timeout, |remaining| remaining.min(timeout));
    let client = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(1))
        .build()?;
    loop {
        ensure!(
            process.child.try_wait()?.is_none(),
            "service exited before readiness"
        );
        let probe_cancel = cancel.child_token();
        let check = async {
            match readiness {
                Readiness::Tcp { address, .. } => tokio::select! {
                    _ = probe_cancel.cancelled() => Ok(false),
                    result = tokio::net::TcpStream::connect(address) => Ok(result.is_ok()),
                },
                Readiness::Http { url, .. } => tokio::select! {
                    _ = probe_cancel.cancelled() => Ok(false),
                    result = client.get(url).send() => Ok(result.is_ok_and(|r| r.status().is_success())),
                },
                Readiness::Command { command, .. } => {
                    crate::process::readiness_command(
                        directory,
                        command,
                        shell,
                        environment,
                        &probe_cancel,
                    )
                    .await
                }
            }
        };
        tokio::pin!(check);
        // Cancelling a readiness future is not cleanup: its process and pipe
        // owners must finish before the service/session can return or restart.
        let ready = tokio::select! {
            biased;
            _ = cancel.cancelled() => {
                probe_cancel.cancel();
                check.await?;
                anyhow::bail!("service readiness cancelled");
            }
            _ = tokio::time::sleep_until(deadline) => {
                probe_cancel.cancel();
                check.await?;
                anyhow::bail!("service readiness timed out");
            }
            exited = process.child.wait() => {
                probe_cancel.cancel();
                check.await?;
                exited?;
                anyhow::bail!("service exited before readiness");
            }
            result = &mut check => result?,
        };
        if ready {
            return Ok(());
        }
        ensure!(
            tokio::time::Instant::now() < deadline,
            "service readiness timed out"
        );
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
}

#[cfg(test)]
mod output_cleanup_tests {
    use std::sync::atomic::{AtomicBool, Ordering};

    use super::*;

    #[tokio::test]
    async fn publication_cancellation_replaces_success_exit_code() {
        let directory = tempfile::tempdir().unwrap();
        std::fs::write(
            directory.path().join("taskflow.yml"),
            "version: 1\nproject: app\ntasks:\n  check:\n    command: [unused]\n    input: []\n",
        )
        .unwrap();
        let graph = Graph::build(
            crate::discover::Workspace::discover(directory.path())
                .await
                .unwrap(),
        )
        .unwrap();
        let mut receipt = Receipt::skipped(
            "app#check",
            Outcome::Executed,
            BTreeSet::from([Cause::Direct]),
        );
        // The command has already succeeded; cancellation arrives at publication.
        receipt.exit_code = 0;
        let cancel = CancellationToken::new();
        cancel.cancel();
        publish(
            &graph,
            "app#check",
            &mut receipt,
            &BTreeMap::new(),
            None,
            None,
            &cancel,
        )
        .await
        .unwrap();
        assert_eq!(receipt.outcome, Outcome::Cancelled);
        assert_eq!(receipt.exit_code, 130);
        assert!(previous(directory.path(), "app#check").is_none());
    }

    #[tokio::test]
    async fn remote_publication_cannot_retract_completed_receipts() {
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        for phase in ["object", "entry", "lost-response"] {
            let directory = tempfile::tempdir().unwrap();
            std::fs::write(
                directory.path().join("taskflow.yml"),
                "version: 1\nproject: app\ntasks:\n  check:\n    command: [unused]\n    input: \
                 []\n    output: []\n    cache: true\n    tools: {fixture: [unused]}\n",
            )
            .unwrap();
            let graph = Graph::build(
                crate::discover::Workspace::discover(directory.path())
                    .await
                    .unwrap(),
            )
            .unwrap();
            let inputs = files::input_state(
                &graph.workspace,
                &graph.workspace.projects["app"],
                &graph.tasks["app#check"].task,
            )
            .unwrap();
            let mut receipt = Receipt::skipped(
                "app#check",
                Outcome::Executed,
                BTreeSet::from([Cause::Direct]),
            );
            receipt.exit_code = 0;
            receipt.key = files::digest(b"fixture");
            receipt.output = receipt.key.clone();
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let remote = Remote::fixture(format!("http://{}", listener.local_addr().unwrap()));
            let cancel = CancellationToken::new();
            let stop = cancel.clone();
            let root = directory.path().to_path_buf();
            let server = tokio::spawn(async move {
                for index in 0..if phase == "object" { 1 } else { 2 } {
                    let (mut socket, _) = listener.accept().await.unwrap();
                    let mut request = Vec::new();
                    let end = loop {
                        assert!(socket.read_buf(&mut request).await.unwrap() > 0);
                        if let Some(end) = request.windows(4).position(|part| part == b"\r\n\r\n") {
                            break end + 4;
                        }
                    };
                    let headers = String::from_utf8_lossy(&request[..end]).to_ascii_lowercase();
                    let length: usize = headers
                        .lines()
                        .find_map(|line| line.strip_prefix("content-length: "))
                        .unwrap()
                        .parse()
                        .unwrap();
                    while request.len() < end + length {
                        assert!(socket.read_buf(&mut request).await.unwrap() > 0);
                    }
                    assert!(headers.contains(if index == 0 { "/objects/" } else { "/entries/" }));
                    if index == 1 {
                        assert!(previous(&root, "app#check").unwrap().success());
                    }
                    if phase == "object" || index == 1 {
                        stop.cancel();
                    }
                    if phase != "lost-response" || index == 0 {
                        let _ = socket.write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\nConnection: close\r\n\r\n").await;
                    }
                }
            });
            publish(
                &graph,
                "app#check",
                &mut receipt,
                &inputs,
                None,
                Some(&remote),
                &cancel,
            )
            .await
            .unwrap();
            server.await.unwrap();
            if phase == "object" {
                assert_eq!(receipt.outcome, Outcome::Cancelled);
                assert!(!cache::entry_path(directory.path(), &receipt.key).exists());
                assert!(previous(directory.path(), "app#check").is_none());
            } else {
                assert_eq!(receipt.outcome, Outcome::Executed);
                assert_eq!(receipt.exit_code, 0);
                assert_eq!(
                    previous(directory.path(), "app#check").unwrap().outcome,
                    Outcome::Executed
                );
                assert!(cache::load(directory.path(), &receipt.key)
                    .unwrap()
                    .is_some());
            }
        }
    }

    #[tokio::test]
    async fn log_write_failure_awaits_both_streams_and_cleanup() {
        for cleanup_fails in [false, true] {
            let directory = tempfile::tempdir().unwrap();
            let path = directory.path().join("output.log");
            std::fs::write(&path, "retained").unwrap();
            let log = Arc::new(Mutex::new(File::open(&path).unwrap()));
            let stdout = tokio::spawn(stream_log(&b"output"[..], log, vec![], false, true));
            let sibling_finished = Arc::new(AtomicBool::new(false));
            let sibling = sibling_finished.clone();
            let stderr = tokio::spawn(async move {
                tokio::task::yield_now().await;
                sibling.store(true, Ordering::SeqCst);
                Ok(())
            });
            let cleaned = AtomicBool::new(false);
            let cleanup = async {
                assert!(sibling_finished.load(Ordering::SeqCst));
                tokio::task::yield_now().await;
                cleaned.store(true, Ordering::SeqCst);
                if cleanup_fails {
                    Err(anyhow::Error::new(crate::docker::CleanupFailure))
                } else {
                    Ok(())
                }
            };
            let error = finish_logs_and_container(stdout, stderr, cleanup)
                .await
                .unwrap_err();
            assert!(cleaned.load(Ordering::SeqCst));
            assert_eq!(error.is::<crate::docker::CleanupFailure>(), cleanup_fails);
            assert_eq!(std::fs::read(&path).unwrap(), b"retained");
        }
    }

    #[tokio::test]
    async fn failed_service_logs_stop_live_processes_and_await_cleanup() {
        let directory = tempfile::tempdir().unwrap();
        let source = directory.path().join("service.rs");
        let binary = directory.path().join(if cfg!(windows) {
            "service.exe"
        } else {
            "service"
        });
        std::fs::write(
            &source,
            r#"
            fn main() {
                let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
                std::fs::write("address", listener.local_addr().unwrap().to_string()).unwrap();
                if std::env::args().nth(1).unwrap() == "stdout" { println!("live"); }
                else { eprintln!("live"); }
                loop { std::thread::sleep(std::time::Duration::from_secs(1)); }
            }
        "#,
        )
        .unwrap();
        assert!(std::process::Command::new("rustc")
            .arg(&source)
            .arg("-o")
            .arg(&binary)
            .status()
            .unwrap()
            .success());
        for stream in ["stdout", "stderr"] {
            for cleanup_fails in [false, true] {
                let path = directory.path().join("output.log");
                std::fs::write(&path, "retained").unwrap();
                let broken = Arc::new(Mutex::new(File::open(&path).unwrap()));
                let good = Arc::new(Mutex::new(
                    File::create(directory.path().join("good.log")).unwrap(),
                ));
                let failed = CancellationToken::new();
                // A successful stream EOF alone does not stop a service.
                monitored_log(
                    tokio::io::empty(),
                    good.clone(),
                    vec![],
                    false,
                    true,
                    failed.clone(),
                )
                .await
                .unwrap();
                assert!(!failed.is_cancelled());
                let mut process = OwnedProcess::spawn(
                    directory.path(),
                    &crate::config::Command::Argv(vec![
                        binary.to_str().unwrap().into(),
                        stream.into(),
                    ]),
                    None,
                    None,
                )
                .unwrap();
                let stdout = tokio::spawn(monitored_log(
                    process.child.stdout.take().unwrap(),
                    if stream == "stdout" {
                        broken.clone()
                    } else {
                        good.clone()
                    },
                    vec![],
                    false,
                    true,
                    failed.clone(),
                ));
                let stderr = tokio::spawn(monitored_log(
                    process.child.stderr.take().unwrap(),
                    if stream == "stderr" { broken } else { good },
                    vec![],
                    false,
                    true,
                    failed.clone(),
                ));
                let cleaned = AtomicBool::new(false);
                let cleanup = async {
                    tokio::task::yield_now().await;
                    cleaned.store(true, Ordering::SeqCst);
                    if cleanup_fails {
                        Err(anyhow::Error::new(crate::docker::CleanupFailure))
                    } else {
                        Ok(())
                    }
                };
                let result = tokio::time::timeout(
                    Duration::from_secs(10),
                    finish_service(
                        &mut process,
                        (stdout, stderr),
                        &CancellationToken::new(),
                        &failed,
                        None,
                        cleanup,
                    ),
                )
                .await;
                if result.is_err() {
                    process.terminate().await.unwrap();
                }
                let error = result.unwrap().unwrap_err();
                assert_eq!(error.is::<crate::docker::CleanupFailure>(), cleanup_fails);
                assert!(cleaned.load(Ordering::SeqCst));
                assert!(process.child.try_wait().unwrap().is_some());
                let address = std::fs::read_to_string(directory.path().join("address")).unwrap();
                assert!(
                    std::net::TcpListener::bind(address).is_ok(),
                    "service retained its listener"
                );
                assert_eq!(std::fs::read(&path).unwrap(), b"retained");
            }
        }
    }
}

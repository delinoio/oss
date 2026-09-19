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
    process::{OwnedProcess, ProcessExit},
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
            exit_code: if matches!(outcome, Outcome::Suppressed | Outcome::Ready) {
                0
            } else {
                1
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
            results.insert(id.clone(), receipt.clone());
        }
    }
    let mut active = FuturesUnordered::new();
    while !pending.is_empty() || !active.is_empty() {
        if cancel.is_cancelled() {
            for id in std::mem::take(&mut pending) {
                results.insert(
                    id.clone(),
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
                results.insert(
                    id.clone(),
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
            {
                let mut receipt = Receipt::skipped(&id, Outcome::Suppressed, causes.clone());
                if let Some(previous) = previous {
                    receipt.key = previous.key;
                    receipt.output = previous.output;
                }
                results.insert(id, receipt);
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
                let result =
                    run_task(&graph, &id, causes.clone(), &prerequisites, &options, token).await;
                options.task_cancellations.lock().unwrap().remove(&id);
                let receipt = match result {
                    Ok(receipt) => receipt,
                    Err(error) => {
                        // Detailed native stderr already passes through the masker;
                        // engine errors contain identifiers and stable context only.
                        let node = &graph.tasks[&id];
                        if node.task.service {
                            if let Some(services) = &options.services {
                                let _ = services.events.send((
                                    id.clone(),
                                    ProcessExit {
                                        code: 1,
                                        cancelled: false,
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
                        tracing::error!(task = %id, error = %message, "Task failed");
                        let mut receipt = Receipt::skipped(&id, Outcome::Failed, causes);
                        receipt.diagnostic = Some(message);
                        receipt
                    }
                };
                (id, receipt)
            });
        }
        if let Some((id, receipt)) = active.next().await {
            results.insert(id, receipt);
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

fn critical_duration(graph: &Graph, id: &str, seen: &mut BTreeSet<String>) -> u64 {
    if !seen.insert(id.into()) {
        return 0;
    }
    previous(&graph.workspace.root, id).map_or(1, |r| r.duration_ms.max(1))
        + graph
            .dependents(id)
            .iter()
            .map(|next| critical_duration(graph, next, seen))
            .max()
            .unwrap_or(0)
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
        let bytes = crate::process::capture_task(
            &graph.workspace.root,
            &project.directory,
            task,
            command,
            &environment.values,
            &cancel,
        )
        .await?;
        tools.insert(name, files::digest(&bytes));
    }
    let prerequisite_outputs: BTreeMap<_, _> = prerequisites
        .iter()
        .map(|(id, r)| (id, &r.output))
        .collect();
    let key = files::digest(&serde_json::to_vec(&(
        1,
        id,
        task,
        &inputs,
        &environment.fingerprint,
        &tools,
        &prerequisite_outputs,
        task.platform.key(),
        options.shard,
    ))?);
    let old = previous(&graph.workspace.root, id);
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
            if valid_shards && artifact.validate(&key, id, project, task).is_ok() {
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
                        .is_none_or(|r| r.output != artifact.output_digest),
                    key,
                    output: artifact.output_digest.clone(),
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
                if !cancel.is_cancelled() {
                    cache::store(&graph.workspace.root, &artifact)?;
                    persist(&graph.workspace.root, &receipt)?;
                }
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
        publish(graph, id, &mut receipt, &inputs, remote.as_ref(), &cancel).await?;
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
    let stdout = tokio::spawn(stream_log(
        process.child.stdout.take().unwrap(),
        log.clone(),
        environment.secrets.clone(),
        options.show_secrets,
        options.quiet,
    ));
    let stderr = tokio::spawn(stream_log(
        process.child.stderr.take().unwrap(),
        log,
        environment.secrets.clone(),
        options.show_secrets,
        options.quiet,
    ));
    tracing::info!(task = id, execution = %execution, causes = ?causes, "Task started");
    if task.service {
        if let Some(readiness) = &task.readiness {
            let ready = wait_ready(
                &mut process,
                readiness,
                task.shell.as_deref(),
                &project.directory,
                &values,
                &cancel,
            );
            let result = if let Some(timeout) = timeout {
                tokio::time::timeout(timeout.saturating_sub(process_started.elapsed()), ready)
                    .await
                    .context("service timeout elapsed before readiness")
                    .and_then(|result| result)
            } else {
                ready.await
            };
            if let Err(error) = result {
                process.terminate().await?;
                stdout.await??;
                stderr.await??;
                if let Some(mut docker) = docker {
                    docker.cleanup().await?;
                }
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
            let waited = process.wait(&stop, remaining).await;
            let mut result = waited.as_ref().copied().unwrap_or(ProcessExit {
                code: 1,
                cancelled: false,
            });
            if result.cancelled && !stop.is_cancelled() {
                tracing::warn!(task = %event_id, code = "service-timeout", "Service exceeded its timeout");
                result = ProcessExit {
                    code: 124,
                    cancelled: false,
                };
            }
            // Reap every owner before propagating any failure. In particular,
            // cancellation is not successful cleanup without daemon verification.
            let stdout = stdout.await.context("service stdout owner failed");
            let stderr = stderr.await.context("service stderr owner failed");
            let cleanup = if let Some(mut docker) = docker {
                docker.cleanup().await
            } else {
                Ok(())
            };
            if cleanup.is_err() {
                tracing::error!(task = %event_id, code = "service-container-cleanup-failed", "Failed to confirm container removal");
                result = ProcessExit {
                    code: 1,
                    cancelled: false,
                };
            }
            let _ = events.send((event_id, result));
            waited?;
            stdout??;
            stderr??;
            cleanup?;
            Ok(())
        });
        services.joins.lock().unwrap().push(join);
        return Ok(Receipt {
            version: 1,
            task: id.into(),
            execution,
            outcome: Outcome::Ready,
            changed: true,
            key,
            output: String::new(),
            causes,
            duration_ms: started.elapsed().as_millis() as u64,
            exit_code: 0,
            diagnostic: None,
        });
    }
    let status = process
        .wait(
            &cancel,
            task.timeout
                .as_ref()
                .map(|s| config::duration(s))
                .transpose()?,
        )
        .await?;
    stdout.await??;
    stderr.await??;
    if let Some(mut docker) = docker {
        docker.cleanup().await?;
    }
    let stable = (task.install && !task.cache)
        || files::input_state(&graph.workspace, project, task)? == inputs;
    let mut receipt = Receipt {
        version: 1,
        task: id.into(),
        execution: execution.clone(),
        outcome: if status.cancelled || cancel.is_cancelled() {
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
        diagnostic: None,
    };
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
        publish(graph, id, &mut receipt, &inputs, remote.as_ref(), &cancel).await?;
    }
    tracing::info!(task = id, outcome = ?receipt.outcome, changed = receipt.changed, duration_ms = receipt.duration_ms, "Task finished");
    Ok(receipt)
}

async fn publish(
    graph: &Graph,
    id: &str,
    receipt: &mut Receipt,
    inputs: &BTreeMap<String, String>,
    remote: Option<&Remote>,
    cancel: &CancellationToken,
) -> Result<()> {
    let node = &graph.tasks[id];
    let project = &graph.workspace.projects[&node.project];
    let task = &node.task;
    let valid = |receipt: &mut Receipt| -> Result<bool> {
        if cancel.is_cancelled() {
            receipt.outcome = Outcome::Cancelled;
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
    if task.output.as_ref().is_some_and(|v| !v.is_empty()) {
        receipt.output = cache::output_state(project, task)?;
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
        receipt.output = artifact.output_digest.clone();
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
        if let (Some(remote), Some(digest)) = (remote, staged) {
            tokio::select! {
                biased;
                _ = cancel.cancelled() => {},
                result = remote.commit(&artifact.key, &digest) => if result.is_err() { tracing::warn!(task = id, code = "remote-write-failed", "Remote cache publication failed"); }
            }
        }
        if !valid(receipt)? {
            return Ok(());
        }
        cache::store(&graph.workspace.root, &artifact)?;
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
) -> Result<()> {
    let timeout = match readiness {
        Readiness::Tcp { timeout, .. }
        | Readiness::Http { timeout, .. }
        | Readiness::Command { timeout, .. } => config::duration(timeout)?,
    };
    let deadline = tokio::time::Instant::now() + timeout;
    let client = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .timeout(Duration::from_secs(1))
        .build()?;
    loop {
        ensure!(
            process.child.try_wait()?.is_none(),
            "service exited before readiness"
        );
        let check = async {
            match readiness {
                Readiness::Tcp { address, .. } => {
                    tokio::net::TcpStream::connect(address).await.is_ok()
                }
                Readiness::Http { url, .. } => client
                    .get(url)
                    .send()
                    .await
                    .is_ok_and(|r| r.status().is_success()),
                Readiness::Command { command, .. } => crate::process::capture_with_shell(
                    directory,
                    command,
                    shell,
                    environment,
                    cancel,
                )
                .await
                .is_ok(),
            }
        };
        let ready = tokio::select! { _ = cancel.cancelled() => anyhow::bail!("service readiness cancelled"), result = tokio::time::timeout_at(deadline, check) => result.context("service readiness timed out")? };
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

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
    pub joins: Mutex<Vec<tokio::task::JoinHandle<()>>>,
}
impl Services {
    pub async fn shutdown(&self) {
        for token in self.controls.lock().unwrap().values() {
            token.cancel();
        }
        let joins = std::mem::take(&mut *self.joins.lock().unwrap());
        for join in joins {
            let _ = join.await;
        }
        self.controls.lock().unwrap().clear();
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
        }
    }
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
            let own_outputs_missing = !graph.tasks[&id]
                .task
                .output
                .as_ref()
                .is_none_or(Vec::is_empty)
                && cache::output_state(
                    &graph.workspace.projects[&graph.tasks[&id].project],
                    &graph.tasks[&id].task,
                )
                .is_err();
            if !causes.iter().any(Cause::independent)
                && !prerequisites.values().any(|r| r.changed)
                && !own_outputs_missing
            {
                let mut receipt = Receipt::skipped(&id, Outcome::Suppressed, causes.clone());
                if let Some(previous) = previous(&graph.workspace.root, &id) {
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
                        tracing::error!(task = %id, error = %error, "Task failed");
                        let mut receipt = Receipt::skipped(&id, Outcome::Failed, causes);
                        receipt.diagnostic = Some(error.to_string());
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
        let bytes = crate::process::capture_with_env(
            &project.directory,
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
                match remote.get(&key).await {
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
            if artifact.validate(&key, id, project, task).is_ok() {
                ensure!(
                    !cancel.is_cancelled(),
                    "task cancelled before cache restoration"
                );
                let before = cache::output_state(project, task).ok();
                if before.as_deref() != Some(&artifact.output_digest) {
                    artifact.restore(&key, id, project, task)?;
                    source = Outcome::Restored;
                }
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
    values.insert(
        "TFLOW_RESULT_FILE".into(),
        result_file.to_string_lossy().into_owned(),
    );
    values.insert("TFLOW_EXECUTION_ID".into(), execution.clone());
    if let Ok(exe) = std::env::current_exe() {
        values.insert("TFLOW_BIN".into(), exe.to_string_lossy().into_owned());
    }
    let log = Arc::new(Mutex::new(File::create(run_directory.join("output.log"))?));
    let mut docker = None;
    let mut command = task.command.clone();
    if task.platform.executor == Executor::Docker {
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
        let receipt = crate::shard::execute(
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
        if receipt.success() && !cancel.is_cancelled() {
            persist(&graph.workspace.root, &receipt)?;
        }
        return Ok(receipt);
    }
    let mut process = OwnedProcess::spawn(
        &project.directory,
        &command,
        task.shell.as_deref(),
        Some(&values),
    )?;
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
    tracing::info!(task = id, execution = %execution, "Task started");
    if task.service {
        if let Some(readiness) = &task.readiness {
            wait_ready(
                &mut process,
                readiness,
                &project.directory,
                &values,
                &cancel,
            )
            .await?;
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
            let result = process.wait(&stop, None).await.unwrap_or(ProcessExit {
                code: 1,
                cancelled: false,
            });
            let _ = stdout.await;
            let _ = stderr.await;
            if let Some(mut docker) = docker {
                let _ = docker.cleanup().await;
            }
            let _ = events.send((event_id, result));
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
    let stable = task.install || files::input_state(&graph.workspace, project, task)? == inputs;
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
        if task.cache && !cancel.is_cancelled() {
            let artifact = cache::Artifact::capture(receipt.key.clone(), id.into(), project, task)?;
            receipt.output = artifact.output_digest.clone();
            cache::store(&graph.workspace.root, &artifact)?;
            if let Some(remote) = &remote {
                if remote.put(&artifact).await.is_err() {
                    tracing::warn!(
                        task = id,
                        code = "remote-write-failed",
                        "Remote cache write failed"
                    );
                }
            }
        }
        if !cancel.is_cancelled() {
            persist(&graph.workspace.root, &receipt)?;
        }
    }
    tracing::info!(task = id, outcome = ?receipt.outcome, changed = receipt.changed, duration_ms = receipt.duration_ms, "Task finished");
    Ok(receipt)
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
                Readiness::Command { command, .. } => {
                    crate::process::capture_with_env(directory, command, environment, cancel)
                        .await
                        .is_ok()
                }
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

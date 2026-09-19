use std::{
    collections::{BTreeMap, BTreeSet},
    path::Path,
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};

use anyhow::{ensure, Context, Result};
use notify::{RecursiveMode, Watcher};
use tokio_util::sync::CancellationToken;

use crate::{
    config::{self, Overlap},
    discover::Workspace,
    files,
    graph::{reference, Graph},
    plan::{Cause, Plan},
    runner::{self, Receipt, RunOptions, RunResult, Services},
    schedule::{CronClock, IntervalClock},
};

struct Timer {
    interval: Option<IntervalClock>,
    cron: Option<CronClock>,
}
struct Pending {
    causes: BTreeSet<Cause>,
    due: Instant,
}

pub async fn start(
    root: &Path,
    profile: &str,
    mut options: RunOptions,
    cancel: CancellationToken,
) -> Result<RunResult> {
    // Discovery stores canonical paths (including Windows verbatim prefixes).
    // Watch the same root and normalize notifications before path comparisons;
    // notify may return ordinary drive paths or paths through a root alias.
    let root = root.canonicalize()?;
    let root = root.as_path();
    // Subscription precedes discovery/activation; events during initial metadata
    // reads, cache hits, and prerequisite execution remain queued for
    // reconciliation.
    let (events, mut receiver) = tokio::sync::mpsc::unbounded_channel();
    let mut watcher = notify::recommended_watcher(move |event: notify::Result<notify::Event>| {
        // Linux reports opens/reads from our own metadata and input snapshots.
        // Feeding those back into discovery cancels live work and creates a
        // perpetual rescan loop. Only filesystem mutations can invalidate work.
        if event.as_ref().is_ok_and(|event| event.kind.is_access()) {
            return;
        }
        let _ = events.send(event);
    })?;
    watcher.watch(root, RecursiveMode::Recursive)?;
    let (service_events, mut service_receiver) = tokio::sync::mpsc::unbounded_channel();
    let services = Arc::new(Services {
        controls: Mutex::new(BTreeMap::new()),
        events: service_events,
        joins: Mutex::new(vec![]),
    });
    options.services = Some(services.clone());
    let work_cancel = cancel.child_token();
    // Keep the JoinSet outside the fallible session body. A server/configuration
    // failure must cancel and await every wave, rather than aborting its futures
    // before their process/container owners have finished asynchronous cleanup.
    let mut waves = tokio::task::JoinSet::new();
    let result = async {
        let mut graph = Arc::new(Graph::build(Workspace::discover(root).await?.select_platform(options.os, options.arch))?);
        let mut roots = roots(&graph, profile)?;
        let mut active_set = activation(&graph, &roots);
        let mut bootstrap_results = BTreeMap::new();
        if active_set.iter().any(|id| graph.unresolved.contains(id)) {
            let installs: Vec<_> = active_set.iter().filter(|id| graph.tasks[*id].task.install).cloned().collect();
            ensure!(!installs.is_empty(), "unresolved native metadata requires an explicit install: true prerequisite");
            let install_plan = Plan::create(&graph, &installs, &[], false)?;
            let result = runner::run_plan(graph.clone(), install_plan, options.clone(), work_cancel.child_token()).await?;
            ensure!(result.success, "installation prerequisite failed");
            bootstrap_results = result.results;
            graph = Arc::new(Graph::build(Workspace::discover(root).await?.select_platform(options.os, options.arch))?);
            roots = roots_for_profile(&graph, profile)?;
            active_set = activation(&graph, &roots);
        }
        let mut pending = initial(&graph, &active_set);
        pending.retain(|id, _| !bootstrap_results.contains_key(id));
        let mut timers = timers(&graph, &active_set)?;
        let mut observed = snapshots(&graph, &active_set)?;
        let mut results: BTreeMap<String, Receipt> = bootstrap_results;
        let mut active_tasks = BTreeSet::new();
        let mut generation_cancel = work_cancel.child_token();
        let mut reload = false;
        let mut invalid = false;
        let mut initial_done = false;
        loop {
            if work_cancel.is_cancelled() { break; }
            if reload {
                // FSEvents can deliver delayed/coalesced metadata notifications.
                // Confirm a different generation before cancelling healthy work.
                let next = Workspace::discover(root).await.map(|ws| ws.select_platform(options.os, options.arch)).and_then(Graph::build);
                let changed = !next.as_ref().is_ok_and(|next| next.workspace.generation == graph.workspace.generation) || invalid;
                if changed {
                    generation_cancel.cancel();
                    while waves.join_next().await.is_some() {}
                    active_tasks.clear();
                    generation_cancel = work_cancel.child_token();
                }
                match next {
                    Ok(next) => {
                        if next.workspace.generation != graph.workspace.generation || invalid {
                            services.shutdown().await?;
                            // Consume expected service exits from the replaced generation.
                            while service_receiver.try_recv().is_ok() {}
                            graph = Arc::new(next);
                            roots = roots_for_profile(&graph, profile)?;
                            active_set = activation(&graph, &roots);
                            pending = initial(&graph, &active_set);
                            timers = self::timers(&graph, &active_set)?;
                            observed = snapshots(&graph, &active_set)?;
                            results.clear(); initial_done = false;
                        }
                        invalid = false;
                    }
                    Err(error) => { tracing::error!(code = "session-configuration-invalid", error = %error, "New work suspended until configuration is corrected"); invalid = true; }
                }
                reload = false;
            }
            if !invalid && !reload {
                let now = Instant::now();
                for (id, timer) in &mut timers {
                    let tick = if let Some(interval) = &mut timer.interval {
                        interval.tick(now)
                    } else { timer.cron.as_mut().is_some_and(|c| c.tick(chrono::Utc::now())) };
                    if tick { enqueue(&graph, &options, &mut pending, id, Cause::Schedule, now); }
                }
                let due: BTreeSet<_> = pending.iter().filter(|(id, p)| p.due <= now && !active_tasks.contains(*id)).map(|(id, _)| id.clone()).collect();
                if !due.is_empty() {
                    let mut seeds: BTreeMap<String, BTreeSet<Cause>> = BTreeMap::new();
                    for id in &due {
                        let closure = graph.closure(&BTreeSet::from([id.clone()]), false, false);
                        if closure.iter().any(|p| active_tasks.contains(p)) { continue; }
                        seeds.insert(id.clone(), pending.remove(id).unwrap().causes);
                    }
                    if !seeds.is_empty() {
                        let propagation = graph.closure(&seeds.keys().cloned().collect(), true, false);
                        for id in propagation {
                            if active_set.contains(&id) && !graph.tasks[&id].task.service && !active_tasks.contains(&id) { seeds.entry(id).or_default(); }
                        }
                        let plan = Plan::for_tasks(&graph, seeds.clone())?;
                        let selected: BTreeSet<_> = plan.order.iter().cloned().collect();
                        let mut wave_options = options.clone();
                        wave_options.provided = results.iter().filter(|(id, r)| r.success() && !seeds.contains_key(*id)).map(|(id, r)| (id.clone(), r.clone())).collect();
                        active_tasks.extend(selected.clone());
                        let graph = graph.clone();
                        let token = generation_cancel.child_token();
                        waves.spawn(async move { (selected, runner::run_plan(graph, plan, wave_options, token).await) });
                    }
                }
            }
            if waves.is_empty() && pending.is_empty() && !initial_done {
                initial_done = true;
                roots.retain(|id| { let task = &graph.tasks[id].task; task.service || task.watch.is_some() || task.schedule.is_some() });
                active_set = activation(&graph, &roots);
                timers.retain(|id, _| active_set.contains(id));
                for (id, stop) in services.controls.lock().unwrap().iter() { if !active_set.contains(id) { stop.cancel(); } }
            }
            if initial_done && active_set.is_empty() && waves.is_empty() { break; }
            tokio::select! {
                _ = work_cancel.cancelled() => break,
                event = receiver.recv() => {
                    if let Some(event) = event {
                        match event {
                            Ok(event) => {
                                for path in event.paths {
                                    let path = files::canonical_path(&path)?;
                                    if !path.starts_with(root) { continue; }
                                    if path.components().any(|c| c.as_os_str() == ".taskflow") || path.components().any(|c| c.as_os_str().to_string_lossy().starts_with(".taskflow-restore-")) { continue; }
                                    if graph.workspace.metadata_files.contains(&path) || path.file_name().is_some_and(|v| matches!(v.to_str(), Some("taskflow.yml" | "package.json" | "Cargo.toml" | "go.mod" | "go.work" | "pnpm-workspace.yaml"))) {
                                        reload = true;
                                        continue;
                                    }
                                    for id in &active_set {
                                        let node = &graph.tasks[id];
                                        let Some(watch) = &node.task.watch else { continue; };
                                        if files::input_matches(&graph.workspace.projects[&node.project], &node.task, &path)? {
                                            let snapshot = files::input_state(&graph.workspace, &graph.workspace.projects[&node.project], &node.task)?;
                                            if observed.get(id) != Some(&snapshot) {
                                                observed.insert(id.clone(), snapshot);
                                                enqueue(&graph, &options, &mut pending, id, Cause::Input { path: files::relative_to(root, &path) }, Instant::now() + config::duration(&watch.debounce)?);
                                            }
                                        }
                                    }
                                }
                            }
                            Err(_) => {
                                // Overflow or lost events require a full conservative rescan.
                                for id in &active_set { if graph.tasks[id].task.watch.is_some() { enqueue(&graph, &options, &mut pending, id, Cause::IncompleteGraph, Instant::now()); } }
                                reload = true;
                            }
                        }
                    }
                }
                event = service_receiver.recv() => {
                    if let Some((id, status)) = event {
                        services.controls.lock().unwrap().remove(&id);
                        if active_set.contains(&id) && !status.cancelled { anyhow::bail!("service {id} exited ({}); shutting down session", status.code); }
                    }
                }
                Some(result) = waves.join_next(), if !waves.is_empty() => {
                    let (selected, wave) = result?;
                    active_tasks.retain(|id| !selected.contains(id));
                    match wave {
                        Ok(wave) => {
                            for (id, receipt) in wave.results {
                                if !receipt.success() { tracing::warn!(task = %id, outcome = ?receipt.outcome, "Session check failed; subscriptions remain active"); }
                                results.insert(id, receipt);
                            }
                        }
                        Err(error) => {
                            if selected.iter().any(|id| graph.tasks[id].task.service) { return Err(error); }
                            tracing::error!(error = %error, "Session wave failed; subscriptions remain active");
                        }
                    }
                    if selected.iter().any(|id| graph.tasks[id].task.service && results.get(id).is_some_and(|r| !r.success() && r.outcome != runner::Outcome::Cancelled)) { anyhow::bail!("service activation failed"); }
                }
                _ = tokio::time::sleep(Duration::from_millis(25)) => {}
            }
        }
        work_cancel.cancel();
        while let Some(result) = waves.join_next().await { if let Ok((_, Ok(wave))) = result { results.extend(wave.results); } }
        Ok(RunResult { version: 1, success: results.values().all(Receipt::success), results })
    }.await;
    work_cancel.cancel();
    for token in options.task_cancellations.lock().unwrap().values() {
        token.cancel();
    }
    while waves.join_next().await.is_some() {}
    let cleanup = services.shutdown().await;
    drop(watcher);
    cleanup?;
    result
}

fn roots(graph: &Graph, profile: &str) -> Result<BTreeSet<String>> {
    roots_for_profile(graph, profile)
}
fn roots_for_profile(graph: &Graph, profile: &str) -> Result<BTreeSet<String>> {
    let targets = graph
        .workspace
        .config
        .start
        .get(profile)
        .with_context(|| format!("unknown start profile: {profile}"))?;
    ensure!(!targets.is_empty(), "start profile is empty");
    Ok(targets
        .iter()
        .map(|s| reference(&graph.workspace.config.project, s))
        .collect())
}
fn activation(graph: &Graph, roots: &BTreeSet<String>) -> BTreeSet<String> {
    let mut active = graph.closure(roots, false, false);
    loop {
        let previous = active.len();
        for id in active.clone() {
            let node = &graph.tasks[&id];
            for companion in &node.task.with {
                active.extend(graph.closure(
                    &BTreeSet::from([reference(&node.project, companion)]),
                    false,
                    false,
                ));
            }
        }
        if active.len() == previous {
            return active;
        }
    }
}
fn initial(graph: &Graph, active: &BTreeSet<String>) -> BTreeMap<String, Pending> {
    active
        .iter()
        .filter(|id| {
            let task = &graph.tasks[*id].task;
            task.service
                || task
                    .watch
                    .as_ref()
                    .map(|w| w.initial)
                    .or_else(|| task.schedule.as_ref().map(|s| s.initial))
                    .unwrap_or(true)
        })
        .map(|id| {
            (
                id.clone(),
                Pending {
                    causes: BTreeSet::from([Cause::Activation]),
                    due: Instant::now(),
                },
            )
        })
        .collect()
}
fn timers(graph: &Graph, active: &BTreeSet<String>) -> Result<BTreeMap<String, Timer>> {
    let mut timers = BTreeMap::new();
    for id in active {
        if let Some(schedule) = &graph.tasks[id].task.schedule {
            let every = schedule
                .every
                .as_ref()
                .map(|s| config::duration(s))
                .transpose()?;
            let mut cron = schedule
                .cron
                .as_ref()
                .map(|s| CronClock::new(s, &schedule.timezone))
                .transpose()?;
            if let Some(cron) = &mut cron {
                cron.tick(chrono::Utc::now());
            }
            timers.insert(
                id.clone(),
                Timer {
                    interval: every.map(|period| IntervalClock::new(period, Instant::now())),
                    cron,
                },
            );
        }
    }
    Ok(timers)
}
fn snapshots(
    graph: &Graph,
    active: &BTreeSet<String>,
) -> Result<BTreeMap<String, BTreeMap<String, String>>> {
    active
        .iter()
        .filter(|id| graph.tasks[*id].task.watch.is_some())
        .map(|id| {
            Ok((
                id.clone(),
                files::input_state(
                    &graph.workspace,
                    &graph.workspace.projects[&graph.tasks[id].project],
                    &graph.tasks[id].task,
                )?,
            ))
        })
        .collect()
}
fn enqueue(
    graph: &Graph,
    options: &RunOptions,
    pending: &mut BTreeMap<String, Pending>,
    id: &str,
    cause: Cause,
    due: Instant,
) {
    // Wave membership reserves planned prerequisites until the wave completes;
    // overlap applies only while this task itself still owns an execution.
    let running = options.task_cancellations.lock().unwrap().get(id).cloned();
    if let Some(token) = running {
        match graph.tasks[id].task.overlap() {
            Overlap::Skip => return,
            Overlap::Restart => {
                token.cancel();
            }
            Overlap::Queue => {}
        }
    }
    let pending = pending.entry(id.into()).or_insert_with(|| Pending {
        causes: BTreeSet::new(),
        due,
    });
    pending.causes.insert(cause);
    pending.due = due;
}

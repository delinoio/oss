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
        let _ = events.send((Instant::now(), event));
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
        let mut bootstrap_evidence = BTreeMap::new();
        let mut invalid_bootstrap = BTreeSet::new();
        let mut bootstrap_generations = BTreeSet::new();
        while let Some(install) = runner::next_install(&graph, &graph.topological(&active_set)?, &bootstrap_results)? {
            ensure!(bootstrap_generations.insert((graph.workspace.generation.clone(), install.clone())), "installation did not stabilize after graph refresh: {install}");
            let install_plan = Plan::create(&graph, &[install], &[], false)?;
            let mut bootstrap_options = options.clone();
            bootstrap_options.shard = None;
            bootstrap_options.provided = bootstrap_results.clone();
            let result = runner::run_plan(graph.clone(), install_plan, bootstrap_options.clone(), work_cancel.child_token()).await?;
            if !result.success { return Ok(result); }
            // Live owners cannot be transferred to a refreshed task identity.
            services.shutdown().await?;
            while service_receiver.try_recv().is_ok() {}
            graph = Arc::new(Graph::build(Workspace::discover(root).await?.select_platform(options.os, options.arch))?);
            bootstrap_evidence.extend(result.results);
            let (retained, invalidated) = runner::revalidate_bootstrap(&graph, bootstrap_evidence, &bootstrap_options, &work_cancel).await?;
            // Removed receipts cannot appear in a later phase's validation
            // input. Keep their activation until a refreshed receipt proves
            // that a later phase actually repaired them.
            invalid_bootstrap.extend(invalidated);
            invalid_bootstrap.retain(|id| !retained.contains_key(id));
            // Stopped services can validate an install's semantic inputs, but
            // cannot satisfy readiness in another phase or the final session.
            bootstrap_results = retained.iter().filter(|(id, _)| !graph.tasks[*id].task.service)
                .map(|(id, receipt)| (id.clone(), receipt.clone())).collect();
            bootstrap_evidence = retained;
            tracing::debug!(retained = bootstrap_results.len(), evidence = bootstrap_evidence.len(), invalidated = invalid_bootstrap.len(), "Refreshed session bootstrap receipts");
            roots = roots_for_profile(&graph, profile)?;
            active_set = activation(&graph, &roots);
        }
        let mut pending = initial(&graph, &active_set);
        pending.retain(|id, _| !bootstrap_results.contains_key(id));
        for id in invalid_bootstrap.intersection(&active_set) {
            pending.entry(id.clone()).or_insert_with(|| Pending { causes: BTreeSet::from([Cause::Activation]), due: Instant::now() });
        }
        let mut timers = timers(&graph, &active_set)?;
        let mut observed = snapshots(&graph, &active_set)?;
        let mut baseline_at = Instant::now();
        let mut bootstrap_reuse: BTreeSet<_> = bootstrap_results.keys().cloned().collect();
        let mut results: BTreeMap<String, Receipt> = bootstrap_results;
        let mut active_tasks = BTreeMap::new();
        let (completed, mut completions) = tokio::sync::mpsc::unbounded_channel();
        let mut next_wave = 0u64;
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
                            let mut retained = std::mem::take(&mut pending);
                            pending = initial(&graph, &active_set);
                            for (id, previous) in retained.iter_mut().filter(|(id, _)| active_set.contains(*id)) {
                                let entry = pending.entry(id.clone()).or_insert_with(|| Pending { causes: BTreeSet::new(), due: previous.due });
                                entry.causes.append(&mut previous.causes);
                                entry.due = entry.due.min(previous.due);
                            }
                            timers = self::timers(&graph, &active_set)?;
                            baseline_at = Instant::now();
                            results.clear(); bootstrap_reuse.clear(); initial_done = false;
                        }
                        // Discovery refreshes automatic metadata inputs too.
                        // Compare against the last accepted snapshots before
                        // replacing them; initial:false only disables activation,
                        // never an actual manifest/lockfile change. Keep this
                        // baseline intact throughout invalid configuration.
                        let refreshed = snapshots(&graph, &active_set)?;
                        for (id, snapshot) in &refreshed {
                            let previous = observed.get(id);
                            let paths: BTreeSet<_> = snapshot.keys().chain(previous.into_iter().flat_map(|state| state.keys())).collect();
                            for path in paths {
                                if previous.and_then(|state| state.get(path)) != snapshot.get(path) {
                                    let watch = graph.tasks[id].task.watch.as_ref().unwrap();
                                    tracing::debug!(task = %id, path, "Preserving input change across graph refresh");
                                    enqueue(&graph, &options, &mut pending, id, Cause::Input { path: path.clone() }, Instant::now() + config::duration(&watch.debounce)?);
                                }
                            }
                        }
                        observed = refreshed;
                        invalid = false;
                    }
                    Err(error) => { tracing::error!(code = "session-configuration-invalid", error = %error, "New work suspended until configuration is corrected"); invalid = true; }
                }
                reload = false;
            }
            drain_service_events(&services, &mut service_receiver, &active_set)?;
            if !invalid && !reload {
                let now = Instant::now();
                for (id, timer) in &mut timers {
                    let tick = if let Some(interval) = &mut timer.interval {
                        interval.tick(now)
                    } else { timer.cron.as_mut().is_some_and(|c| c.tick(chrono::Utc::now())) };
                    if tick { enqueue(&graph, &options, &mut pending, id, Cause::Schedule, now); }
                }
                let due: BTreeSet<_> = pending.iter().filter(|(id, p)| p.due <= now && !active_tasks.contains_key(*id)).map(|(id, _)| id.clone()).collect();
                if !due.is_empty() {
                    let mut seeds: BTreeMap<String, BTreeSet<Cause>> = BTreeMap::new();
                    for id in &due {
                        let closure = graph.closure(&BTreeSet::from([id.clone()]), false, false);
                        if closure.iter().any(|p| active_tasks.contains_key(p)) { continue; }
                        seeds.insert(id.clone(), pending.remove(id).unwrap().causes);
                    }
                    if !seeds.is_empty() {
                        let propagation = graph.closure(&seeds.keys().cloned().collect(), true, false);
                        for id in &propagation {
                            if !active_set.contains(id) || graph.tasks[id].task.service { continue; }
                            // The first activation renews stopped service owners,
                            // not their already-validated semantic inputs. Keep
                            // bootstrap receipts reusable unless independently
                            // seeded. Later waves use ordinary cause propagation.
                            if bootstrap_reuse.contains(id) && !seeds.contains_key(id) { continue; }
                            let reserved = graph.closure(&BTreeSet::from([id.clone()]), false, false).iter().any(|p| active_tasks.contains_key(p));
                            if reserved {
                                for prerequisite in graph.prerequisites(id).into_iter().filter(|p| propagation.contains(p)) {
                                    enqueue(&graph, &options, &mut pending, id, Cause::Prerequisite { task: prerequisite }, now);
                                }
                            } else { seeds.entry(id.clone()).or_default(); }
                        }
                        let plan = Plan::for_tasks(&graph, seeds.clone())?;
                        let selected: BTreeSet<_> = plan.order.iter().cloned().collect();
                        let mut wave_options = options.clone();
                        wave_options.provided = provided_receipts(&graph, &selected, &seeds, &results, &services, &mut service_receiver, &active_set)?;
                        bootstrap_reuse.clear();
                        let wave_id = next_wave;
                        next_wave += 1;
                        active_tasks.extend(selected.iter().cloned().map(|id| (id, wave_id)));
                        let (finished, mut task_results) = tokio::sync::mpsc::unbounded_channel();
                        wave_options.completions = Some(finished);
                        let completed = completed.clone();
                        let graph = graph.clone();
                        let token = generation_cancel.child_token();
                        waves.spawn(async move {
                            let run = runner::run_plan(graph, plan, wave_options, token);
                            tokio::pin!(run);
                            let result = loop {
                                tokio::select! {
                                    result = &mut run => break result,
                                    Some(receipt) = task_results.recv() => { let _ = completed.send((wave_id, receipt)); }
                                }
                            };
                            while let Ok(receipt) = task_results.try_recv() { let _ = completed.send((wave_id, receipt)); }
                            (wave_id, selected, result)
                        });
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
                    if let Some((received_at, event)) = event {
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
                                        if files::input_event_may_match(&graph.workspace.projects[&node.project], &node.task, &path) {
                                            let snapshot = files::input_state(&graph.workspace, &graph.workspace.projects[&node.project], &node.task)?;
                                            // Queued mutations can already be represented in the first
                                            // snapshot. Preserve their input cause even with initial:false.
                                            let before_baseline = received_at <= baseline_at && queued_input_event_may_match(&graph.workspace.projects[&node.project], &node.task, &path, event.kind)?;
                                            let changed = observed.get(id) != Some(&snapshot);
                                            tracing::debug!(task = %id, path = %path.display(), before_baseline, changed, event_age_ms = received_at.elapsed().as_millis(), "Observed watched input event");
                                            if before_baseline || changed {
                                                observed.insert(id.clone(), snapshot);
                                                enqueue(&graph, &options, &mut pending, id, Cause::Input { path: files::relative_to(root, &path)? }, Instant::now() + config::duration(&watch.debounce)?);
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
                        service_exit(&services, &active_set, id, status)?;
                    }
                }
                Some((wave_id, receipt)) = completions.recv() => {
                    complete_task(wave_id, receipt, &mut active_tasks, &mut results);
                }
                Some(result) = waves.join_next(), if !waves.is_empty() => {
                    let (wave_id, selected, wave) = result?;
                    match wave {
                        Ok(wave) => {
                            for receipt in wave.results.into_values() {
                                complete_task(wave_id, receipt, &mut active_tasks, &mut results);
                            }
                        }
                        Err(error) => {
                            if selected.iter().any(|id| graph.tasks[id].task.service) { return Err(error); }
                            tracing::error!(error = %error, "Session wave failed; subscriptions remain active");
                        }
                    }
                    active_tasks.retain(|_, owner| *owner != wave_id);
                    for id in selected.iter().filter(|id| graph.tasks[*id].task.service) {
                        if let Some(receipt) = results.get(id).filter(|r| !r.success() && r.outcome != runner::Outcome::Cancelled) {
                            if receipt.exit_code == 124 {
                                return Err(anyhow::Error::new(crate::process::TimedOut).context("service activation timed out"));
                            }
                            anyhow::bail!("service activation failed");
                        }
                    }
                }
                _ = tokio::time::sleep(Duration::from_millis(25)) => {}
            }
        }
        work_cancel.cancel();
        while let Some(result) = waves.join_next().await {
            if let Ok((wave_id, _, Ok(wave))) = result {
                for receipt in wave.results.into_values() { complete_task(wave_id, receipt, &mut active_tasks, &mut results); }
            }
        }
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

fn provided_receipts(
    graph: &Graph,
    selected: &BTreeSet<String>,
    seeds: &BTreeMap<String, BTreeSet<Cause>>,
    results: &BTreeMap<String, Receipt>,
    services: &Services,
    receiver: &mut tokio::sync::mpsc::UnboundedReceiver<(String, crate::process::ProcessExit)>,
    active: &BTreeSet<String>,
) -> Result<BTreeMap<String, Receipt>> {
    let provided = results
        .iter()
        .filter(|(id, receipt)| {
            if !selected.contains(*id) || seeds.contains_key(*id) || !receipt.success() {
                return false;
            }
            let node = &graph.tasks[*id];
            // A live service is shared by its owners; finite artifacts
            // must still match before bypassing the normal executor.
            let valid = node.task.service
                || crate::cache::output_state(&graph.workspace.projects[&node.project], &node.task)
                    .is_ok_and(|output| output == receipt.output);
            if !valid {
                tracing::info!(
                    task = *id,
                    code = "session-output-invalid",
                    "Revalidating prerequisite before the next session wave"
                );
            }
            valid
        })
        .map(|(id, r)| {
            // This receipt proves availability, not a new change in this wave.
            // Keep the semantic identity for keys without replaying its old cause.
            let mut reused = r.clone();
            reused.changed = false;
            (id.clone(), reused)
        })
        .collect();
    // Output hashing can take time even without an await. Consume every known
    // service exit before supplying ready receipts to a newly spawned wave.
    drain_service_events(services, receiver, active)?;
    Ok(provided)
}

fn service_exit(
    services: &Services,
    active: &BTreeSet<String>,
    id: String,
    status: crate::process::ProcessExit,
) -> Result<()> {
    services.controls.lock().unwrap().remove(&id);
    if active.contains(&id) && status.reason == crate::process::ExitReason::TimedOut {
        return Err(anyhow::Error::new(crate::process::TimedOut)
            .context(format!("service {id} timed out; shutting down session")));
    }
    ensure!(
        !active.contains(&id) || status.cancelled(),
        "service {id} exited ({}); shutting down session",
        status.code
    );
    Ok(())
}

fn drain_service_events(
    services: &Services,
    receiver: &mut tokio::sync::mpsc::UnboundedReceiver<(String, crate::process::ProcessExit)>,
    active: &BTreeSet<String>,
) -> Result<()> {
    while let Ok((id, status)) = receiver.try_recv() {
        service_exit(services, active, id, status)?;
    }
    Ok(())
}

fn complete_task(
    wave_id: u64,
    receipt: Receipt,
    active: &mut BTreeMap<String, u64>,
    results: &mut BTreeMap<String, Receipt>,
) {
    // A replaced execution may complete before an older unrelated wave. Never
    // let that old wave release the new owner or overwrite its latest receipt.
    if active.get(&receipt.task) != Some(&wave_id) {
        return;
    }
    active.remove(&receipt.task);
    if !receipt.success() {
        tracing::warn!(task = %receipt.task, outcome = ?receipt.outcome, "Session check failed; subscriptions remain active");
    }
    results.insert(receipt.task.clone(), receipt);
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
                || (task.watch.is_none() && task.schedule.is_none())
                || task.watch.as_ref().is_some_and(|watch| watch.initial)
                || task
                    .schedule
                    .as_ref()
                    .is_some_and(|schedule| schedule.initial)
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
    // Pending prerequisites remain reserved until their individual receipt;
    // overlap applies only while this task itself still owns an execution.
    let running = options.task_cancellations.lock().unwrap().get(id).cloned();
    if let Some(token) = running {
        // A mixed subscription keeps each trigger's default: a timer tick
        // cannot discard an input change just because the task has a schedule.
        let overlap =
            graph.tasks[id]
                .task
                .overlap
                .unwrap_or(if matches!(&cause, Cause::Schedule) {
                    Overlap::Skip
                } else {
                    Overlap::Queue
                });
        tracing::debug!(
            task = id,
            ?cause,
            ?overlap,
            "Applying overlap to active execution"
        );
        match overlap {
            Overlap::Skip => return,
            Overlap::Restart => {
                token.cancel();
            }
            Overlap::Queue => {}
        }
    }
    tracing::debug!(task = id, ?cause, "Queueing session trigger");
    let pending = pending.entry(id.into()).or_insert_with(|| Pending {
        causes: BTreeSet::new(),
        due,
    });
    pending.causes.insert(cause);
    pending.due = due;
}

fn queued_input_event_may_match(
    project: &crate::discover::Project,
    task: &config::Task,
    path: &Path,
    kind: notify::EventKind,
) -> Result<bool> {
    if files::input_matches(project, task, path)? {
        return Ok(true);
    }
    // A directory-only create/rename/remove can already be reflected in the
    // first snapshot. Exact file glob matching loses that queued cause. Known
    // file events retain exact filtering; ambiguous events use root overlap
    // because a removed directory cannot be inspected afterward.
    if matches!(
        kind,
        notify::EventKind::Create(notify::event::CreateKind::File)
            | notify::EventKind::Remove(notify::event::RemoveKind::File)
            | notify::EventKind::Modify(notify::event::ModifyKind::Data(_))
    ) || files::output_matches(project, task, path)?
    {
        return Ok(false);
    }
    Ok(files::input_event_may_match(project, task, path))
}

#[cfg(test)]
mod overlap_tests {
    use super::*;

    #[tokio::test]
    async fn queued_directory_mutations_keep_causes_already_in_the_baseline() {
        use notify::{
            event::{CreateKind, ModifyKind, RemoveKind, RenameMode},
            EventKind,
        };
        let root = tempfile::tempdir().unwrap();
        std::fs::write(
            root.path().join("taskflow.yml"),
            serde_yaml::to_string(&serde_json::json!({
                "version":1,"project":"app","tasks":{"check":{
                    "command":["unused"],"input":["src/*.rs","!src/ignored.rs"],
                    "output":["src/generated/**"],"watch":{"initial":false}
                }}
            }))
            .unwrap(),
        )
        .unwrap();
        files::atomic_write(
            &root.path().join("src/main.rs"),
            b"created during discovery",
        )
        .unwrap();
        let graph = Graph::build(Workspace::discover(root.path()).await.unwrap()).unwrap();
        let project = &graph.workspace.projects["app"];
        let task = &graph.tasks["app#check"].task;
        let source = project.directory.join("src");
        assert!(!files::input_matches(project, task, &source).unwrap());
        assert!(initial(&graph, &BTreeSet::from(["app#check".into()])).is_empty());
        for kind in [
            EventKind::Create(CreateKind::Folder),
            EventKind::Modify(ModifyKind::Name(RenameMode::Both)),
            EventKind::Remove(RemoveKind::Folder),
            EventKind::Any,
        ] {
            if kind.is_remove() {
                std::fs::remove_dir_all(&source).unwrap();
            }
            // Discovery has already consumed the mutation: an ordinary
            // snapshot comparison cannot recover its initial-disabled cause.
            let baseline = files::input_state(&graph.workspace, project, task).unwrap();
            assert_eq!(
                baseline,
                files::input_state(&graph.workspace, project, task).unwrap()
            );
            assert!(queued_input_event_may_match(project, task, &source, kind).unwrap());
        }
        for (path, kind) in [
            ("src/generated", EventKind::Create(CreateKind::Folder)),
            ("src/generated/result", EventKind::Any),
            ("src/ignored.rs", EventKind::Create(CreateKind::File)),
            ("unrelated", EventKind::Create(CreateKind::Folder)),
        ] {
            assert!(
                !queued_input_event_may_match(project, task, &project.directory.join(path), kind)
                    .unwrap(),
                "{path}"
            );
        }
    }

    #[tokio::test]
    async fn queued_service_exit_prevents_reusing_ready_receipts() {
        let root = tempfile::tempdir().unwrap();
        let binary = root.path().join(if cfg!(windows) {
            "server.exe"
        } else {
            "server"
        });
        let source = root.path().join("server.rs");
        std::fs::write(
            &source,
            r#"
            fn main() {
                if std::env::args().any(|a| a == "--ready") { return; }
                while !std::path::Path::new("exit").exists() {
                    std::thread::sleep(std::time::Duration::from_millis(10));
                }
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
        std::fs::write(root.path().join("taskflow.yml"), serde_yaml::to_string(&serde_json::json!({
            "version":1,"project":"app","tasks":{
                "server":{"command":[binary],"service":true,"input":[],"readiness":{"type":"command","command":[binary,"--ready"],"timeout":"10s"}},
                "check":{"command":[binary,"--ready"],"input":[],"dependsOn":[{"task":"server","waitFor":"ready"}]}
            }
        })).unwrap()).unwrap();
        let graph =
            Arc::new(Graph::build(Workspace::discover(root.path()).await.unwrap()).unwrap());
        let (events, mut receiver) = tokio::sync::mpsc::unbounded_channel();
        let services = Arc::new(Services {
            controls: Mutex::new(BTreeMap::new()),
            events,
            joins: Mutex::new(vec![]),
        });
        let plan = Plan::create(&graph, &["server".into()], &[], false).unwrap();
        let ready = runner::run_plan(
            graph.clone(),
            plan,
            RunOptions {
                services: Some(services.clone()),
                ..Default::default()
            },
            CancellationToken::new(),
        )
        .await
        .unwrap();
        assert_eq!(ready.results["app#server"].outcome, runner::Outcome::Ready);
        let selected = BTreeSet::from(["app#check".into(), "app#server".into()]);
        for cause in [
            Cause::Schedule,
            Cause::Input {
                path: "source".into(),
            },
        ] {
            let seeds = BTreeMap::from([("app#check".into(), BTreeSet::from([cause]))]);
            assert!(provided_receipts(
                &graph,
                &selected,
                &seeds,
                &ready.results,
                &services,
                &mut receiver,
                &selected
            )
            .unwrap()
            .contains_key("app#server"));
        }
        std::fs::write(root.path().join("exit"), "exit normally").unwrap();
        tokio::time::timeout(Duration::from_secs(10), async {
            while !services
                .joins
                .lock()
                .unwrap()
                .iter()
                .all(|join| join.is_finished())
            {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        let seeds = BTreeMap::from([("app#check".into(), BTreeSet::from([Cause::Schedule]))]);
        // The process owner has queued a real exit while this next trigger is
        // due. A cached Ready receipt cannot authorize that dependent wave.
        let error = provided_receipts(
            &graph,
            &selected,
            &seeds,
            &ready.results,
            &services,
            &mut receiver,
            &selected,
        )
        .unwrap_err();
        assert!(
            error.to_string().contains("service app#server exited (0)"),
            "{error:#}"
        );
        assert!(services.controls.lock().unwrap().is_empty());
        services.shutdown().await.unwrap();
    }

    #[tokio::test]
    async fn mixed_subscriptions_preserve_trigger_defaults_and_explicit_overrides() {
        for (policy, watch_queued, tick_queued, restarted) in [
            (None, true, false, false),
            (Some("queue"), true, true, false),
            (Some("skip"), false, false, false),
            (Some("restart"), true, true, true),
        ] {
            let root = tempfile::tempdir().unwrap();
            let mut task = serde_json::json!({"command":["unused"],"input":["source"],"watch":{},"schedule":{"every":"1s"}});
            if let Some(policy) = policy {
                task["overlap"] = policy.into();
            }
            std::fs::write(
                root.path().join("taskflow.yml"),
                serde_yaml::to_string(
                    &serde_json::json!({"version":1,"project":"app","tasks":{"check":task}}),
                )
                .unwrap(),
            )
            .unwrap();
            let graph = Graph::build(Workspace::discover(root.path()).await.unwrap()).unwrap();
            let options = RunOptions::default();
            let running = CancellationToken::new();
            options
                .task_cancellations
                .lock()
                .unwrap()
                .insert("app#check".into(), running.clone());
            let mut pending = BTreeMap::new();
            let input = Cause::Input {
                path: "source".into(),
            };
            enqueue(
                &graph,
                &options,
                &mut pending,
                "app#check",
                input.clone(),
                Instant::now(),
            );
            assert_eq!(pending.contains_key("app#check"), watch_queued);
            enqueue(
                &graph,
                &options,
                &mut pending,
                "app#check",
                Cause::Schedule,
                Instant::now(),
            );
            let causes = pending
                .get("app#check")
                .map(|pending| pending.causes.clone())
                .unwrap_or_default();
            assert_eq!(causes.contains(&input), watch_queued);
            assert_eq!(causes.contains(&Cause::Schedule), tick_queued);
            assert_eq!(running.is_cancelled(), restarted);
        }
    }
}

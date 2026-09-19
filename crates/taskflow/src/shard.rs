use std::{
    collections::{BTreeMap, BTreeSet},
    fs::File,
    path::Path,
    sync::{Arc, Mutex},
    time::Instant,
};

use anyhow::{bail, ensure, Context, Result};
use serde::{Deserialize, Serialize};
use serde_json::Value;
use tokio_util::sync::CancellationToken;

use crate::{
    config::{Command, ShardAdapter},
    files,
    graph::Graph,
    plan::Cause,
    process::{self, OwnedProcess},
    runner::{Outcome, Receipt, RunOptions},
};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Inventory {
    pub version: u32,
    pub tests: Vec<TestUnit>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct TestUnit {
    pub id: String,
    #[serde(default)]
    pub duration_ms: u64,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ShardResults {
    pub version: u32,
    pub inventory: String,
    pub index: usize,
    pub count: usize,
    pub results: Vec<UnitResult>,
}
#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct UnitResult {
    pub id: String,
    pub status: UnitStatus,
    #[serde(default)]
    pub duration_ms: u64,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum UnitStatus {
    Passed,
    Skipped,
    Failed,
    Cancelled,
}

pub fn assign(inventory: &Inventory, count: usize) -> Result<Vec<Vec<String>>> {
    ensure!(
        inventory.version == 1 && (1..=256).contains(&count),
        "invalid shard inventory version or count"
    );
    let mut seen = BTreeSet::new();
    for unit in &inventory.tests {
        ensure!(
            !unit.id.is_empty() && seen.insert(&unit.id),
            "duplicate or empty test ID"
        );
    }
    let mut ordered = inventory.tests.clone();
    ordered.sort_by_key(|t| (std::cmp::Reverse(t.duration_ms), t.id.clone()));
    let mut shards = vec![vec![]; count];
    let mut durations = vec![0u64; count];
    for unit in ordered {
        let index = (0..count)
            .min_by_key(|i| (durations[*i], shards[*i].len(), *i))
            .unwrap();
        durations[index] = durations[index].saturating_add(unit.duration_ms.max(1));
        shards[index].push(unit.id);
    }
    for shard in &mut shards {
        shard.sort();
    }
    Ok(shards)
}
pub fn account(expected: &[String], actual: &[UnitResult]) -> Result<bool> {
    let expected: BTreeSet<_> = expected.iter().collect();
    let actual_ids: BTreeSet<_> = actual.iter().map(|r| &r.id).collect();
    ensure!(
        actual_ids.len() == actual.len() && expected == actual_ids,
        "shard results contain missing, duplicate, or unexpected test IDs"
    );
    Ok(actual
        .iter()
        .all(|r| matches!(r.status, UnitStatus::Passed | UnitStatus::Skipped)))
}
pub fn aggregate(inventory: &Inventory, count: usize, results: &[ShardResults]) -> Result<bool> {
    let assignments = assign(inventory, count)?;
    let digest = files::digest(&serde_json::to_vec(inventory)?);
    ensure!(results.len() == count, "missing shard result");
    let mut seen = BTreeSet::new();
    let mut passed = true;
    for result in results {
        ensure!(
            result.version == 1
                && result.count == count
                && result.inventory == digest
                && result.index < count
                && seen.insert(result.index),
            "incompatible or duplicate shard result"
        );
        passed &= account(&assignments[result.index], &result.results)?;
    }
    Ok(passed)
}

pub async fn inventory(
    graph: &Graph,
    id: &str,
    env: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<(Inventory, BTreeMap<String, Command>)> {
    let task = &graph.tasks[id].task;
    let shard = task
        .shard
        .as_ref()
        .context("task has no shard configuration")?;
    let directory = &graph.workspace.projects[&graph.tasks[id].project].directory;
    if shard.adapter == ShardAdapter::Generic {
        let bytes = process::capture_with_env(
            directory,
            shard
                .list
                .as_ref()
                .context("generic inventory command missing")?,
            env,
            cancel,
        )
        .await?;
        let inventory: Inventory = serde_json::from_slice(&bytes)?;
        assign(&inventory, shard.count)?;
        return Ok((inventory, BTreeMap::new()));
    }
    let Command::Argv(base) = &task.command else {
        bail!("native sharding requires an argv command; use generic for shell commands");
    };
    let mut commands = BTreeMap::new();
    match shard.adapter {
        ShardAdapter::Go => {
            ensure!(
                base.len() >= 2 && base[0] == "go" && base[1] == "test",
                "Go adapter requires go test argv"
            );
            let mut list = base.clone();
            list.extend(["-list".into(), ".".into(), "-json".into()]);
            let bytes =
                process::capture_with_env(directory, &Command::Argv(list), env, cancel).await?;
            let flags = go_flags(&base[2..])?;
            for line in bytes.split(|b| *b == b'\n').filter(|l| !l.is_empty()) {
                let event: Value = serde_json::from_slice(line)?;
                let (Some(package), Some(output)) =
                    (event["Package"].as_str(), event["Output"].as_str())
                else {
                    continue;
                };
                let name = output.trim();
                if !["Test", "Example", "Fuzz"]
                    .iter()
                    .any(|prefix| name.starts_with(prefix))
                    || !name.chars().all(|c| c.is_alphanumeric() || c == '_')
                {
                    continue;
                }
                let mut run = vec!["go".into(), "test".into()];
                run.extend(flags.clone());
                run.extend([
                    package.into(),
                    "-run".into(),
                    format!("^{name}$"),
                    "-count=1".into(),
                ]);
                commands.insert(format!("{package}::{name}"), Command::Argv(run));
            }
        }
        ShardAdapter::Libtest => {
            ensure!(
                base.len() >= 2
                    && base[0] == "cargo"
                    && base[1] == "test"
                    && !base.iter().any(|v| v == "--"),
                "libtest adapter requires cargo test without harness arguments"
            );
            let manifest = std::fs::read_to_string(directory.join("Cargo.toml"))?;
            ensure!(
                !manifest.lines().any(|line| line
                    .split('#')
                    .next()
                    .unwrap_or("")
                    .replace(' ', "")
                    .trim()
                    == "harness=false"),
                "custom Rust harness requires the generic adapter"
            );
            let mut build = base.clone();
            build.extend(["--no-run".into(), "--message-format=json".into()]);
            let bytes =
                process::capture_with_env(directory, &Command::Argv(build), env, cancel).await?;
            let mut has_library = false;
            for line in bytes.split(|b| *b == b'\n').filter(|l| !l.is_empty()) {
                let artifact: Value = serde_json::from_slice(line)?;
                if artifact["reason"] != "compiler-artifact"
                    || artifact.pointer("/profile/test") != Some(&Value::Bool(true))
                {
                    continue;
                }
                let Some(binary) = artifact["executable"].as_str() else {
                    continue;
                };
                let target = artifact
                    .pointer("/target/name")
                    .and_then(Value::as_str)
                    .context("test target name missing")?;
                let kinds = artifact
                    .pointer("/target/kind")
                    .and_then(Value::as_array)
                    .context("test target kind missing")?;
                has_library |= kinds
                    .iter()
                    .any(|v| matches!(v.as_str(), Some("lib" | "rlib")));
                let names = process::capture_with_env(
                    directory,
                    &Command::Argv(vec![
                        binary.into(),
                        "--list".into(),
                        "--format=terse".into(),
                    ]),
                    env,
                    cancel,
                )
                .await?;
                for line in std::str::from_utf8(&names)?.lines() {
                    if let Some(name) = line.strip_suffix(": test") {
                        let unit = format!(
                            "{}:{target}::{name}",
                            kinds
                                .iter()
                                .filter_map(Value::as_str)
                                .collect::<Vec<_>>()
                                .join("+")
                        );
                        ensure!(
                            commands
                                .insert(
                                    unit,
                                    Command::Argv(vec![
                                        binary.into(),
                                        name.into(),
                                        "--exact".into()
                                    ])
                                )
                                .is_none(),
                            "ambiguous Rust test ID"
                        );
                    }
                }
            }
            if has_library
                && !base.iter().any(|v| {
                    matches!(
                        v.as_str(),
                        "--lib" | "--tests" | "--bins" | "--examples" | "--all-targets"
                    )
                })
            {
                let mut docs = base.clone();
                docs.push("--doc".into());
                commands.insert("rust:doctests".into(), Command::Argv(docs));
            }
        }
        ShardAdapter::Vitest | ShardAdapter::Jest => {
            let name = if shard.adapter == ShardAdapter::Vitest {
                "vitest"
            } else {
                "jest"
            };
            let position = base
                .iter()
                .position(|v| v == name)
                .context("test runner missing from command argv")?;
            let mut list = base.clone();
            if shard.adapter == ShardAdapter::Vitest {
                if list.get(position + 1).is_some_and(|v| v == "run") {
                    list[position + 1] = "list".into();
                } else {
                    list.insert(position + 1, "list".into());
                }
                list.push("--filesOnly".into());
            } else {
                list.extend(["--listTests".into(), "--json".into()]);
            }
            let bytes =
                process::capture_with_env(directory, &Command::Argv(list), env, cancel).await?;
            let files: Vec<String> = if shard.adapter == ShardAdapter::Jest {
                serde_json::from_slice(&bytes)?
            } else {
                std::str::from_utf8(&bytes)?
                    .lines()
                    .filter(|s| !s.trim().is_empty())
                    .map(|s| s.trim().to_owned())
                    .collect()
            };
            for file in files {
                let absolute = files::within(&graph.workspace.root, &directory.join(&file))?;
                ensure!(
                    absolute.is_file(),
                    "test runner inventory returned a non-file"
                );
                let id = files::relative_to(directory, &absolute);
                let mut run = base.clone();
                if shard.adapter == ShardAdapter::Jest {
                    run.push("--runTestsByPath".into());
                } else if !run.iter().any(|v| v == "run" || v == "--run") {
                    run.insert(position + 1, "run".into());
                }
                run.push(absolute.to_string_lossy().into_owned());
                commands.insert(id, Command::Argv(run));
            }
        }
        ShardAdapter::Generic => unreachable!(),
    }
    let inventory = Inventory {
        version: 1,
        tests: commands
            .keys()
            .map(|id| TestUnit {
                id: id.clone(),
                duration_ms: 0,
            })
            .collect(),
    };
    assign(&inventory, shard.count)?;
    Ok((inventory, commands))
}

fn go_flags(arguments: &[String]) -> Result<Vec<String>> {
    let mut flags = vec![];
    let mut args = arguments.iter();
    while let Some(arg) = args.next() {
        if !arg.starts_with('-') {
            continue;
        }
        ensure!(
            !matches!(
                arg.split('=').next(),
                Some(
                    "-run"
                        | "-skip"
                        | "-list"
                        | "-json"
                        | "-args"
                        | "-fuzz"
                        | "-bench"
                        | "-coverprofile"
                )
            ),
            "Go selection/output overrides require generic sharding"
        );
        flags.push(arg.clone());
        if !arg.contains('=')
            && matches!(
                arg.as_str(),
                "-tags" | "-timeout" | "-count" | "-parallel" | "-p" | "-cpu" | "-mod" | "-modfile"
            )
        {
            flags.push(args.next().context("Go flag requires value")?.clone());
        }
    }
    Ok(flags)
}

#[allow(clippy::too_many_arguments)]
pub async fn execute(
    graph: &Graph,
    id: &str,
    environment: &BTreeMap<String, String>,
    secrets: &[Vec<u8>],
    options: &RunOptions,
    cancel: &CancellationToken,
    execution: &str,
    key: &str,
    causes: BTreeSet<Cause>,
    log: Arc<Mutex<File>>,
) -> Result<Receipt> {
    let task = &graph.tasks[id].task;
    let config = task.shard.as_ref().unwrap();
    ensure!(
        task.platform.executor == crate::config::Executor::Host,
        "native shard adapter runs on its selected host; use a generic Docker command adapter for \
         container suites"
    );
    let directory = &graph.workspace.projects[&graph.tasks[id].project].directory;
    let (inventory, commands) = inventory(graph, id, environment, cancel).await?;
    let count = options.shard.map_or(config.count, |s| s.1);
    let assignments = assign(&inventory, count)?;
    let inventory_digest = files::digest(&serde_json::to_vec(&inventory)?);
    let root = graph.workspace.root.join(".taskflow/runs").join(execution);
    files::atomic_write(
        &root.join("inventory.json"),
        &serde_json::to_vec(&inventory)?,
    )?;
    let indices = if let Some((index, total)) = options.shard {
        ensure!(
            index < total && total == config.count,
            "invalid shard selection"
        );
        vec![index]
    } else {
        (0..count).collect()
    };
    let started = Instant::now();
    let mut all_passed = true;
    for index in indices {
        let selected = &assignments[index];
        let mut results = vec![];
        if config.adapter == ShardAdapter::Generic && !selected.is_empty() {
            let selected_path = root.join(format!("selected-{index}.json"));
            let results_path = root.join(format!("generic-{index}.json"));
            files::atomic_write(
                &selected_path,
                &serde_json::to_vec(&serde_json::json!({"version":1,"tests":selected}))?,
            )?;
            let mut env = environment.clone();
            env.insert(
                "TFLOW_SHARD_INPUT".into(),
                selected_path.to_string_lossy().into_owned(),
            );
            env.insert(
                "TFLOW_SHARD_RESULT".into(),
                results_path.to_string_lossy().into_owned(),
            );
            let status = run_unit(
                directory,
                config.run.as_ref().unwrap(),
                &env,
                secrets,
                options,
                cancel,
                log.clone(),
            )
            .await?;
            if status == UnitStatus::Cancelled {
                results.extend(selected.iter().map(|id| UnitResult {
                    id: id.clone(),
                    status,
                    duration_ms: 0,
                }));
            } else {
                #[derive(Deserialize)]
                #[serde(deny_unknown_fields)]
                struct GenericResult {
                    version: u32,
                    results: Vec<UnitResult>,
                }
                let report: GenericResult = serde_json::from_slice(
                    &std::fs::read(&results_path)
                        .context("generic adapter did not write results")?,
                )?;
                ensure!(report.version == 1, "unknown generic result version");
                results = report.results;
                all_passed &= status == UnitStatus::Passed;
            }
        } else {
            for test in selected {
                let began = Instant::now();
                let status = if cancel.is_cancelled() {
                    UnitStatus::Cancelled
                } else {
                    run_unit(
                        directory,
                        &commands[test],
                        environment,
                        secrets,
                        options,
                        cancel,
                        log.clone(),
                    )
                    .await?
                };
                results.push(UnitResult {
                    id: test.clone(),
                    status,
                    duration_ms: began.elapsed().as_millis() as u64,
                });
            }
        }
        all_passed &= account(selected, &results)?;
        let report = ShardResults {
            version: 1,
            inventory: inventory_digest.clone(),
            index,
            count,
            results,
        };
        files::atomic_write(
            &root.join(format!("shard-{index}.json")),
            &serde_json::to_vec(&report)?,
        )?;
    }
    Ok(Receipt {
        version: 1,
        task: id.into(),
        execution: execution.into(),
        outcome: if cancel.is_cancelled() {
            Outcome::Cancelled
        } else if all_passed {
            Outcome::Executed
        } else {
            Outcome::Failed
        },
        changed: true,
        key: key.into(),
        output: inventory_digest,
        causes,
        duration_ms: started.elapsed().as_millis() as u64,
        exit_code: if all_passed { 0 } else { 1 },
        diagnostic: None,
    })
}
async fn run_unit(
    directory: &Path,
    command: &Command,
    env: &BTreeMap<String, String>,
    secrets: &[Vec<u8>],
    options: &RunOptions,
    cancel: &CancellationToken,
    log: Arc<Mutex<File>>,
) -> Result<UnitStatus> {
    let mut child = OwnedProcess::spawn(directory, command, None, Some(env))?;
    let stdout = tokio::spawn(crate::runner::stream_log(
        child.child.stdout.take().unwrap(),
        log.clone(),
        secrets.to_vec(),
        options.show_secrets,
        options.quiet,
    ));
    let stderr = tokio::spawn(crate::runner::stream_log(
        child.child.stderr.take().unwrap(),
        log,
        secrets.to_vec(),
        options.show_secrets,
        options.quiet,
    ));
    let status = child.wait(cancel, None).await?;
    stdout.await??;
    stderr.await??;
    Ok(if status.cancelled {
        UnitStatus::Cancelled
    } else if status.code == 0 {
        UnitStatus::Passed
    } else {
        UnitStatus::Failed
    })
}

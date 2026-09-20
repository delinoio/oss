use std::{
    collections::{BTreeMap, BTreeSet},
    io::Read,
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
    process::{self, ExitReason, OwnedProcess, ProcessExit},
    runner::{Outcome, OutputLog, Receipt, RunOptions},
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

pub fn validate_task(task: &crate::config::Task) -> Result<()> {
    let Some(shard) = &task.shard else {
        return Ok(());
    };
    if shard.adapter == ShardAdapter::Generic {
        for command in [shard.list.as_ref(), shard.run.as_ref()] {
            crate::config::validate_command(
                command.context("generic adapter requires list and run")?,
            )?;
        }
        return Ok(());
    }
    let Command::Argv(args) = &task.command else {
        bail!("native sharding requires argv; use generic for shell commands");
    };
    match shard.adapter {
        ShardAdapter::Go => {
            ensure!(
                args.len() >= 2 && args[0] == "go" && args[1] == "test",
                "Go sharding requires go test"
            );
            go_flags(&args[2..])?;
        }
        ShardAdapter::Libtest => {
            ensure!(
                args.len() >= 2 && args[0] == "cargo" && args[1] == "test",
                "libtest sharding requires cargo test"
            );
            let mut rest = args[2..].iter();
            while let Some(arg) = rest.next() {
                let flag = arg.split('=').next().unwrap();
                ensure!(
                    flag != "--target",
                    "Cargo --target requires generic sharding to preserve target runners"
                );
                if matches!(
                    flag,
                    "-p" | "--package"
                        | "--exclude"
                        | "--manifest-path"
                        | "--target-dir"
                        | "--features"
                        | "--profile"
                        | "-j"
                        | "--jobs"
                        | "--test"
                        | "--bin"
                        | "--example"
                        | "--config"
                ) {
                    if !arg.contains('=') {
                        rest.next().context("Cargo flag requires a value")?;
                    }
                } else {
                    ensure!(
                        matches!(
                            flag,
                            "--workspace"
                                | "--all"
                                | "--all-features"
                                | "--no-default-features"
                                | "--release"
                                | "--locked"
                                | "--offline"
                                | "--frozen"
                                | "--lib"
                                | "--tests"
                                | "--bins"
                                | "--examples"
                                | "--all-targets"
                                | "-q"
                                | "--quiet"
                                | "-v"
                                | "--verbose"
                        ),
                        "Cargo test filters and unsupported options require generic sharding"
                    );
                }
            }
        }
        ShardAdapter::Vitest | ShardAdapter::Jest => {
            let runner = if shard.adapter == ShardAdapter::Vitest {
                "vitest"
            } else {
                "jest"
            };
            let position = args
                .iter()
                .position(|v| v == runner)
                .context("native test runner missing from argv")?;
            for arg in &args[position + 1..] {
                ensure!(
                    arg == "run"
                        || matches!(
                            arg.split('=').next(),
                            Some(
                                "--run"
                                    | "--runInBand"
                                    | "--maxWorkers"
                                    | "--minWorkers"
                                    | "--pool"
                                    | "--config"
                                    | "--coverage"
                                    | "--no-file-parallelism"
                                    | "--fileParallelism"
                                    | "--isolate"
                                    | "--no-isolate"
                                    | "--silent"
                                    | "--bail"
                            )
                        ),
                    "JS selection/reporting overrides require generic sharding; option values \
                     must use ="
                );
            }
        }
        ShardAdapter::Generic => unreachable!(),
    }
    Ok(())
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
    ensure!(results.len() == count, "missing shard result");
    validate_reports(inventory, count, results)
}

pub fn validate_reports(
    inventory: &Inventory,
    count: usize,
    results: &[ShardResults],
) -> Result<bool> {
    let assignments = assign(inventory, count)?;
    let digest = files::digest(&serde_json::to_vec(inventory)?);
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

pub fn read_reports(directory: &Path) -> Result<(Inventory, Vec<ShardResults>)> {
    let inventory = serde_json::from_slice(&read_metadata(
        &directory.join("inventory.json"),
        "shard inventory",
    )?)?;
    let mut reports = vec![];
    for entry in std::fs::read_dir(directory)? {
        let entry = entry?;
        if entry.file_name().to_string_lossy().starts_with("shard-") {
            reports.push(serde_json::from_slice(&read_metadata(
                &entry.path(),
                "shard report",
            )?)?);
        }
    }
    reports.sort_by_key(|r: &ShardResults| r.index);
    Ok((inventory, reports))
}

pub fn write_reports(
    directory: &Path,
    inventory: &Inventory,
    reports: &[ShardResults],
) -> Result<()> {
    files::atomic_write(
        &directory.join("inventory.json"),
        &serde_json::to_vec(inventory)?,
    )?;
    for report in reports {
        files::atomic_write(
            &directory.join(format!("shard-{}.json", report.index)),
            &serde_json::to_vec(report)?,
        )?;
    }
    Ok(())
}

pub async fn inventory(
    graph: &Graph,
    id: &str,
    env: &BTreeMap<String, String>,
    overrides: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<(Inventory, BTreeMap<String, Command>)> {
    let task = &graph.tasks[id].task;
    let shard = task
        .shard
        .as_ref()
        .context("task has no shard configuration")?;
    let directory = &graph.workspace.projects[&graph.tasks[id].project].directory;
    if shard.adapter == ShardAdapter::Generic {
        let bytes = process::capture_task(
            &graph.workspace.root,
            directory,
            task,
            shard
                .list
                .as_ref()
                .context("generic inventory command missing")?,
            env,
            overrides,
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
            let bytes = process::capture_task(
                &graph.workspace.root,
                directory,
                task,
                &Command::Argv(list),
                env,
                overrides,
                cancel,
            )
            .await?;
            let (flags, _) = go_flags(&base[2..])?;
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
            let mut build = base.clone();
            let mut metadata_command = vec![
                "cargo".into(),
                "metadata".into(),
                "--no-deps".into(),
                "--format-version=1".into(),
            ];
            // Inventory must identify the package selected by the build, which
            // may be excluded from the task directory's native workspace.
            let mut args = base[2..].iter();
            while let Some(arg) = args.next() {
                if arg == "--manifest-path" {
                    metadata_command.extend([
                        arg.clone(),
                        args.next().context("Cargo manifest path missing")?.clone(),
                    ]);
                } else if arg.starts_with("--manifest-path=") {
                    metadata_command.push(arg.clone());
                }
            }
            let metadata = process::capture_task(
                &graph.workspace.root,
                directory,
                task,
                &Command::Argv(metadata_command),
                env,
                overrides,
                cancel,
            )
            .await?;
            let metadata: Value = serde_json::from_slice(&metadata)?;
            build.extend(["--no-run".into(), "--message-format=json".into()]);
            let bytes = process::capture_task(
                &graph.workspace.root,
                directory,
                task,
                &Command::Argv(build),
                env,
                overrides,
                cancel,
            )
            .await?;
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
                if task.platform.executor == crate::config::Executor::Docker {
                    let relative = binary
                        .strip_prefix("/workspace/")
                        .filter(|path| {
                            !path.is_empty()
                                && path.split('/').all(|part| !matches!(part, "" | "." | ".."))
                        })
                        .context(
                            "Docker libtest executable must persist under /workspace; use a \
                             mounted Cargo target directory or generic sharding",
                        )?;
                    let persisted =
                        files::within(&graph.workspace.root, &graph.workspace.root.join(relative))
                            .context("Docker libtest executable escapes the mounted workspace")?;
                    ensure!(
                        persisted.is_file(),
                        "Docker libtest executable is missing from the mounted workspace; use a \
                         mounted Cargo target directory or generic sharding"
                    );
                }
                let package = metadata["packages"]
                    .as_array()
                    .context("Cargo packages missing")?
                    .iter()
                    .find(|package| package["id"] == artifact["package_id"])
                    .context("test artifact package missing from metadata")?;
                let package_name = package["name"].as_str().context("package name missing")?;
                let version = package["version"]
                    .as_str()
                    .context("package version missing")?;
                let target = artifact
                    .pointer("/target/name")
                    .and_then(Value::as_str)
                    .context("test target name missing")?;
                let kinds = artifact
                    .pointer("/target/kind")
                    .and_then(Value::as_array)
                    .context("test target kind missing")?;
                let manifest_path = package["manifest_path"]
                    .as_str()
                    .context("Cargo manifest path missing")?;
                let manifest_path = if task.platform.executor == crate::config::Executor::Docker {
                    graph.workspace.root.join(
                        manifest_path
                            .strip_prefix("/workspace/")
                            .context("Cargo test manifest outside container workspace")?,
                    )
                } else {
                    manifest_path.into()
                };
                let manifest: toml::Value =
                    toml::from_str(&std::fs::read_to_string(manifest_path)?)?;
                // Cargo has already selected packages, features and targets.
                // Check only this executable's target before invoking --list;
                // unrelated harness declarations must not reject the suite.
                let section = kinds
                    .iter()
                    .filter_map(Value::as_str)
                    .find(|kind| matches!(*kind, "bin" | "test" | "example" | "bench"))
                    .unwrap_or("lib");
                let declaration = if section == "lib" {
                    manifest.get("lib")
                } else {
                    manifest
                        .get(section)
                        .and_then(toml::Value::as_array)
                        .and_then(|targets| {
                            targets.iter().find(|entry| {
                                entry.get("name").and_then(toml::Value::as_str) == Some(target)
                            })
                        })
                };
                ensure!(
                    declaration
                        .and_then(|entry| entry.get("harness"))
                        .and_then(toml::Value::as_bool)
                        != Some(false),
                    "custom Rust harness requires generic sharding: {package_name}:{target}"
                );
                has_library |= kinds
                    .iter()
                    .any(|v| matches!(v.as_str(), Some("lib" | "rlib")));
                let names = process::capture_task(
                    &graph.workspace.root,
                    directory,
                    task,
                    &Command::Argv(vec![
                        binary.into(),
                        "--list".into(),
                        "--format=terse".into(),
                    ]),
                    env,
                    overrides,
                    cancel,
                )
                .await?;
                // --list includes #[ignore] cases even though the exact normal
                // invocation would run zero tests and exit successfully.
                let ignored = process::capture_task(
                    &graph.workspace.root,
                    directory,
                    task,
                    &Command::Argv(vec![
                        binary.into(),
                        "--list".into(),
                        "--ignored".into(),
                        "--format=terse".into(),
                    ]),
                    env,
                    overrides,
                    cancel,
                )
                .await?;
                let ignored: BTreeSet<_> = std::str::from_utf8(&ignored)?
                    .lines()
                    .filter_map(|line| line.strip_suffix(": test"))
                    .collect();
                tracing::debug!(
                    target,
                    ignored = ignored.len(),
                    "Excluded ignored libtest cases from runnable inventory"
                );
                for line in std::str::from_utf8(&names)?.lines() {
                    if let Some(name) = line.strip_suffix(": test") {
                        if ignored.contains(name) {
                            continue;
                        }
                        let unit = format!(
                            "{package_name}@{version}:{}:{target}::{name}",
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
            let bytes = process::capture_task(
                &graph.workspace.root,
                directory,
                task,
                &Command::Argv(list),
                env,
                overrides,
                cancel,
            )
            .await?;
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
                let file_path = if task.platform.executor == crate::config::Executor::Docker
                    && file.starts_with("/workspace/")
                {
                    graph
                        .workspace
                        .root
                        .join(file.trim_start_matches("/workspace/"))
                } else {
                    directory.join(&file)
                };
                let absolute = files::within(&graph.workspace.root, &file_path)?;
                ensure!(
                    absolute.is_file(),
                    "test runner inventory returned a non-file"
                );
                let id = files::relative_to(directory, &absolute)?;
                let mut run = base.clone();
                if shard.adapter == ShardAdapter::Jest {
                    run.push("--runTestsByPath".into());
                } else if !run.iter().any(|v| v == "run" || v == "--run") {
                    run.insert(position + 1, "run".into());
                }
                // JS runners normalize their inventories to ordinary paths.
                // Passing Rust's Windows verbatim canonical prefix as a filter
                // matches no files. Execute the validated project-relative ID,
                // which also stays valid inside the project's Docker workdir.
                run.push(format!("./{id}"));
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

fn go_flags(arguments: &[String]) -> Result<(Vec<String>, bool)> {
    let mut flags = vec![];
    let mut fail_fast = false;
    let mut args = arguments.iter();
    while let Some(arg) = args.next() {
        if !arg.starts_with('-') {
            continue;
        }
        let flag = arg.split('=').next().unwrap();
        let takes_value = match flag {
            "-tags" | "-timeout" | "-count" | "-parallel" | "-p" | "-cpu" | "-mod" | "-modfile"
            | "-coverpkg" | "-covermode" | "-shuffle" | "-vet" | "-asmflags" | "-gcflags"
            | "-ldflags" | "-gccgoflags" | "-buildmode" | "-compiler" | "-pkgdir" | "-overlay"
            | "-toolexec" | "-installsuffix" | "-buildvcs" => true,
            "-a" | "-n" | "-race" | "-msan" | "-asan" | "-v" | "-work" | "-x" | "-trimpath"
            | "-linkshared" | "-cover" | "-short" | "-failfast" | "-fullpath" | "-benchmem" => {
                false
            }
            _ => bail!("unsupported Go shard flag {flag}; use generic sharding"),
        };
        if flag == "-failfast" {
            fail_fast = match arg.split_once('=').map(|(_, value)| value) {
                None | Some("1" | "t" | "T" | "TRUE" | "true" | "True") => true,
                Some("0" | "f" | "F" | "FALSE" | "false" | "False") => false,
                _ => bail!("invalid Go -failfast boolean"),
            };
        }
        flags.push(arg.clone());
        if takes_value && !arg.contains('=') {
            flags.push(args.next().context("Go flag requires value")?.clone());
        }
    }
    Ok((flags, fail_fast))
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
    log: Arc<Mutex<OutputLog>>,
) -> Result<Receipt> {
    let task = &graph.tasks[id].task;
    let config = task.shard.as_ref().unwrap();
    let started = Instant::now();
    let deadline = task
        .timeout
        .as_deref()
        .map(crate::config::duration)
        .transpose()?
        .map(|limit| tokio::time::Instant::now() + limit);
    let listed = process::DEADLINE
        .scope(
            deadline,
            inventory(graph, id, environment, &options.env, cancel),
        )
        .await;
    let (mut inventory, commands) = match listed {
        Err(error) if error.is::<process::TimedOut>() => {
            return Ok(Receipt {
                version: 1,
                task: id.into(),
                execution: execution.into(),
                outcome: Outcome::Failed,
                changed: true,
                key: key.into(),
                output: String::new(),
                causes,
                duration_ms: started.elapsed().as_millis() as u64,
                exit_code: 124,
                diagnostic: Some("task timeout elapsed during inventory".into()),
            })
        }
        listed => listed?,
    };
    let history_path = graph
        .workspace
        .root
        .join(".taskflow/test-durations")
        .join(format!("{}.json", files::digest(id.as_bytes())));
    // Distributed shards start from identical declared inventory bytes. Local
    // history is used only when this invocation owns the complete suite.
    if options.shard.is_none() {
        let history: BTreeMap<String, u64> = std::fs::read(&history_path)
            .ok()
            .and_then(|bytes| serde_json::from_slice(&bytes).ok())
            .unwrap_or_default();
        for test in &mut inventory.tests {
            if let Some(duration) = history.get(&test.id) {
                test.duration_ms = *duration;
            }
        }
    }
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
    let fail_fast = if config.adapter == ShardAdapter::Go {
        let Command::Argv(args) = &task.command else {
            unreachable!()
        };
        go_flags(&args[2..])?.1
    } else {
        false
    };
    let mut stopped_after_failure = false;
    let mut all_passed = true;
    let mut termination: Option<ProcessExit> = None;
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
            crate::environment::insert(
                &mut env,
                "TFLOW_SHARD_INPUT".into(),
                selected_path.to_string_lossy().into_owned(),
            );
            crate::environment::insert(
                &mut env,
                "TFLOW_SHARD_RESULT".into(),
                results_path.to_string_lossy().into_owned(),
            );
            let exit = if let Some(exit) = termination {
                exit
            } else if cancel.is_cancelled() {
                ProcessExit {
                    code: 130,
                    reason: ExitReason::Cancelled,
                }
            } else {
                run_unit(
                    graph,
                    id,
                    config.run.as_ref().unwrap(),
                    &env,
                    secrets,
                    options,
                    cancel,
                    log.clone(),
                    deadline,
                )
                .await?
            };
            let status = unit_status(exit);
            if exit.reason != ExitReason::Completed {
                termination.get_or_insert(exit);
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
                let report: GenericResult =
                    serde_json::from_slice(&read_generic_result(&results_path)?)?;
                ensure!(report.version == 1, "unknown generic result version");
                results = report.results;
                all_passed &= status == UnitStatus::Passed;
            }
        } else {
            for test in selected {
                if stopped_after_failure {
                    results.push(UnitResult {
                        id: test.clone(),
                        status: UnitStatus::Skipped,
                        duration_ms: 0,
                    });
                    continue;
                }
                let began = Instant::now();
                let exit = if let Some(exit) = termination {
                    exit
                } else if cancel.is_cancelled() {
                    ProcessExit {
                        code: 130,
                        reason: ExitReason::Cancelled,
                    }
                } else {
                    run_unit(
                        graph,
                        id,
                        &commands[test],
                        environment,
                        secrets,
                        options,
                        cancel,
                        log.clone(),
                        deadline,
                    )
                    .await?
                };
                if exit.reason != ExitReason::Completed {
                    termination.get_or_insert(exit);
                } else if fail_fast && exit.code != 0 {
                    stopped_after_failure = true;
                    tracing::info!(
                        task = id,
                        code = "go-failfast",
                        "Skipping remaining units after first Go test failure"
                    );
                }
                results.push(UnitResult {
                    id: test.clone(),
                    status: unit_status(exit),
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
    if termination.is_none()
        && deadline.is_some_and(|deadline| tokio::time::Instant::now() >= deadline)
    {
        termination = Some(ProcessExit {
            code: 124,
            reason: ExitReason::TimedOut,
        });
    }
    if options.shard.is_none() && all_passed && termination.is_none() && !cancel.is_cancelled() {
        let (_, reports) = read_reports(&root)?;
        let durations: BTreeMap<_, _> = reports
            .into_iter()
            .flat_map(|r| r.results)
            .map(|r| (r.id, r.duration_ms))
            .collect();
        files::atomic_write(&history_path, &serde_json::to_vec(&durations)?)?;
    }
    if termination.is_none() && cancel.is_cancelled() {
        termination = Some(ProcessExit {
            code: 130,
            reason: ExitReason::Cancelled,
        });
    }
    let timed_out = termination.is_some_and(|exit| exit.reason == ExitReason::TimedOut);
    if let Some(exit) = termination {
        tracing::info!(task = id, reason = ?exit.reason, code = exit.code, "Shard execution terminated");
    }
    Ok(Receipt {
        version: 1,
        task: id.into(),
        execution: execution.into(),
        outcome: if timed_out {
            Outcome::Failed
        } else if termination.is_some() {
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
        exit_code: termination.map_or(if all_passed { 0 } else { 1 }, |exit| exit.code),
        diagnostic: timed_out.then(|| "task timeout elapsed".into()),
    })
}
#[allow(clippy::too_many_arguments)]
async fn run_unit(
    graph: &Graph,
    id: &str,
    command: &Command,
    env: &BTreeMap<String, String>,
    secrets: &[Vec<u8>],
    options: &RunOptions,
    cancel: &CancellationToken,
    log: Arc<Mutex<OutputLog>>,
    deadline: Option<tokio::time::Instant>,
) -> Result<ProcessExit> {
    let result = process::DEADLINE
        .scope(deadline, async {
            let task = &graph.tasks[id].task;
            let directory = &graph.workspace.projects[&graph.tasks[id].project].directory;
            let mut container = None;
            let mut command = command.clone();
            let mut env = env.clone();
            if task.platform.executor == crate::config::Executor::Docker {
                let mut unit = task.clone();
                unit.command = command;
                // Execution units retain publish mappings. Only inventory and
                // tool probes omit them; each sequential unit owns its ports.
                env = crate::docker::host_environment(&env);
                let prepared = crate::docker::prepare(
                    &graph.workspace.root,
                    directory,
                    &unit,
                    &env,
                    &options.env,
                    &uuid::Uuid::now_v7().to_string(),
                    cancel,
                )
                .await?;
                command = prepared.0;
                container = Some(prepared.1);
            }
            if process::remaining_timeout(None).is_some_and(|remaining| remaining.is_zero()) {
                crate::runner::cleanup_container(container).await?;
                return Ok(ProcessExit {
                    code: 124,
                    reason: ExitReason::TimedOut,
                });
            }
            let mut child =
                OwnedProcess::spawn(directory, &command, task.shell.as_deref(), Some(&env))?;
            let stdout = tokio::spawn(crate::runner::stream_log(
                child.child.stdout.take().unwrap(),
                log.clone(),
                secrets.to_vec(),
            ));
            let stderr = tokio::spawn(crate::runner::stream_log(
                child.child.stderr.take().unwrap(),
                log,
                secrets.to_vec(),
            ));
            let waited = child.wait(cancel, process::remaining_timeout(None)).await;
            let reaped = if waited.is_err() {
                child.terminate().await
            } else {
                Ok(())
            };
            crate::runner::finish_logs_and_container(
                stdout,
                stderr,
                crate::runner::cleanup_container(container),
            )
            .await?;
            reaped?;
            waited
        })
        .await;
    match result {
        Err(error) if error.is::<process::TimedOut>() => Ok(ProcessExit {
            code: 124,
            reason: ExitReason::TimedOut,
        }),
        result => result,
    }
}

fn unit_status(exit: ProcessExit) -> UnitStatus {
    match exit.reason {
        ExitReason::Cancelled => UnitStatus::Cancelled,
        ExitReason::TimedOut => UnitStatus::Failed,
        ExitReason::Completed if exit.code == 0 => UnitStatus::Passed,
        ExitReason::Completed => UnitStatus::Failed,
    }
}

fn read_generic_result(path: &Path) -> Result<Vec<u8>> {
    read_metadata(path, "generic adapter results")
}

fn read_metadata(path: &Path, kind: &str) -> Result<Vec<u8>> {
    ensure!(
        std::fs::symlink_metadata(path)?.is_file(),
        "{kind} must be a regular file"
    );
    let mut options = std::fs::OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        // Reject link replacement and never block if a regular file is replaced
        // by a FIFO between the pathname check and open.
        options.custom_flags(nix::libc::O_NOFOLLOW | nix::libc::O_NONBLOCK);
    }
    let file = options
        .open(path)
        .with_context(|| format!("cannot open {kind}"))?;
    let metadata = file.metadata()?;
    ensure!(metadata.is_file(), "{kind} must be a regular file");
    ensure!(
        metadata.len() <= process::METADATA_LIMIT,
        "{kind} exceeded 64 MiB"
    );
    // The file may grow after the metadata query. Bound the actual reader too,
    // and retain one extra byte only to distinguish the exact limit from overflow.
    let mut bytes = Vec::new();
    file.take(process::METADATA_LIMIT + 1)
        .read_to_end(&mut bytes)?;
    ensure!(
        bytes.len() as u64 <= process::METADATA_LIMIT,
        "{kind} exceeded 64 MiB"
    );
    Ok(bytes)
}

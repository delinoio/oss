//! Clean verification freezes the selected source once, then copies it without
//! hard links into fresh environments. It never checks out or edits the source.
use std::{
    ffi::{OsStr, OsString},
    fs,
    path::{Path, PathBuf},
    process::{Command as Process, Stdio},
};

use tokio_util::sync::CancellationToken;

use crate::{
    analysis,
    config::{Command, Config},
    error::{Error, ErrorCode, Result},
    execute::{self, Request},
    model::*,
};

const ISOLATED_VARIABLES: &[&str] = &[
    "HOME",
    "USERPROFILE",
    "XDG_CACHE_HOME",
    "XDG_CONFIG_HOME",
    "XDG_DATA_HOME",
    "CARGO_HOME",
    "GOCACHE",
    "GOMODCACHE",
    "NPM_CONFIG_CACHE",
    "PNPM_HOME",
    "PIP_CACHE_DIR",
    "GRADLE_USER_HOME",
    "UV_CACHE_DIR",
    "CCACHE_DIR",
    "SCCACHE_DIR",
    "TMPDIR",
    "TMP",
    "TEMP",
];
pub fn execution_context() -> Vec<(OsString, OsString)> {
    [
        "PATH",
        "SystemRoot",
        "WINDIR",
        "COMSPEC",
        "PATHEXT",
        "LANG",
        "LC_ALL",
        "LC_CTYPE",
    ]
    .iter()
    .filter_map(|name| std::env::var_os(name).map(|value| (OsString::from(name), value)))
    .collect()
}
pub fn isolated_environment(root: &Path, names: &[String]) -> Result<Vec<(OsString, OsString)>> {
    let mut env = execution_context();
    for name in names {
        if ISOLATED_VARIABLES
            .iter()
            .any(|key| key.eq_ignore_ascii_case(name))
            || name.starts_with("GIT_CONFIG")
            || name == "GIT_DIR"
            || name == "GIT_WORK_TREE"
        {
            return Err(Error::input(
                "selected environment name conflicts with clean isolation",
            ));
        }
        if let Some(value) = std::env::var_os(name) {
            env.retain(|(key, _)| key != OsStr::new(name));
            env.push((name.into(), value));
        }
    }
    for name in ISOLATED_VARIABLES {
        let directory = root.join(if *name == "USERPROFILE" { "HOME" } else { name });
        fs::create_dir_all(&directory).map_err(|_| Error::storage())?;
        env.push((name.into(), directory.into_os_string()));
    }
    Ok(env)
}
fn git(root: &Path, environment: &[(OsString, OsString)]) -> Process {
    let mut command = Process::new("git");
    command
        .current_dir(root)
        .env_clear()
        .envs(environment.iter().map(|(k, v)| (k, v)))
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env(
            "GIT_CONFIG_GLOBAL",
            if cfg!(windows) { "NUL" } else { "/dev/null" },
        )
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_CONFIG_COUNT", "0")
        .arg("-c")
        .arg("core.fsmonitor=false")
        .arg("-c")
        .arg("core.hooksPath=/dev/null")
        .stdin(Stdio::null());
    command
}
async fn managed_git(
    command: Process,
    capture: bool,
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    use tokio::io::AsyncReadExt;
    let mut command = tokio::process::Command::from(command);
    command.stderr(Stdio::null());
    if capture {
        command.stdout(Stdio::piped());
    }
    let mut child = fspy::lifecycle::OwnedChild::spawn(command)
        .map_err(|_| Error::input("Git preparation could not be started"))?;
    let stdout = child.child.stdout.take();
    let local_cancel = cancel.child_token();
    let read_cancel = local_cancel.clone();
    let operation = async {
        let read = async {
            let mut bytes = Vec::new();
            if let Some(stdout) = stdout {
                stdout
                    .take(16 * 1024 * 1024 + 1)
                    .read_to_end(&mut bytes)
                    .await
                    .map_err(|_| Error::input("Git metadata could not be read"))?;
                if bytes.len() > 16 * 1024 * 1024 {
                    read_cancel.cancel();
                    return Err(Error::input("Git metadata exceeds the size limit"));
                }
            }
            Ok(bytes)
        };
        tokio::join!(child.wait(local_cancel.clone()), read)
    };
    tokio::pin!(operation);
    let mut timed_out = false;
    let (status, output) = tokio::select! {
        result = &mut operation => result,
        () = tokio::time::sleep(std::time::Duration::from_secs(120)) => {
            timed_out = true;
            local_cancel.cancel();
            operation.await
        }
    };
    let (status, incomplete) =
        status.map_err(|_| Error::new(ErrorCode::CleanupFailed, "Git process cleanup failed"))?;
    if cancel.is_cancelled() {
        return Err(Error::new(
            ErrorCode::Cancelled,
            "Git preparation cancelled",
        ));
    }
    if timed_out {
        return Err(Error::new(ErrorCode::Timeout, "Git preparation timed out"));
    }
    let output = output?;
    if incomplete {
        return Err(Error::new(
            ErrorCode::CleanupFailed,
            "Git left an incomplete process lifetime",
        ));
    }
    if !status.success() {
        return Err(Error::input(
            "Git source selection failed; a readable repository and HEAD are required",
        ));
    }
    Ok(output)
}
async fn git_output(
    root: &Path,
    args: &[&OsStr],
    env: &[(OsString, OsString)],
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    let mut command = git(root, env);
    command.args(args);
    managed_git(command, true, cancel).await
}
pub fn workspace_root(cwd: &Path) -> PathBuf {
    cwd.ancestors()
        .find(|directory| directory.join(".git").exists())
        .unwrap_or(cwd)
        .to_owned()
}
pub async fn revision(root: &Path, cancel: &CancellationToken) -> Option<String> {
    let bytes = git_output(
        root,
        &[
            OsStr::new("rev-parse"),
            OsStr::new("--verify"),
            OsStr::new("HEAD^{commit}"),
        ],
        &execution_context(),
        cancel,
    )
    .await
    .ok()?;
    let value = String::from_utf8(bytes).ok()?.trim().to_owned();
    if (value.len() == 40 || value.len() == 64) && value.bytes().all(|b| b.is_ascii_hexdigit()) {
        Some(value)
    } else {
        None
    }
}
async fn run_git(
    root: &Path,
    args: &[&OsStr],
    environment: &[(OsString, OsString)],
    cancel: &CancellationToken,
) -> Result<()> {
    let mut command = git(root, environment);
    command.args(args).stdout(Stdio::null());
    managed_git(command, false, cancel).await.map(|_| ())
}
fn copy_entry(source: &Path, destination: &Path, cancel: &CancellationToken) -> Result<()> {
    if cancel.is_cancelled() {
        return Err(Error::new(
            ErrorCode::Cancelled,
            "source preparation cancelled",
        ));
    }
    let metadata = fs::symlink_metadata(source).map_err(|_| Error::storage())?;
    if metadata.is_symlink() {
        let target = fs::read_link(source).map_err(|_| Error::storage())?;
        #[cfg(unix)]
        {
            std::os::unix::fs::symlink(target, destination).map_err(|_| Error::storage())?;
        }
        #[cfg(windows)]
        {
            if source.is_dir() {
                std::os::windows::fs::symlink_dir(target, destination)
            } else {
                std::os::windows::fs::symlink_file(target, destination)
            }
            .map_err(|_| {
                Error::new(
                    ErrorCode::Unsupported,
                    "copying checkout symlinks requires Windows symlink capability",
                )
            })?;
        }
    } else if metadata.is_dir() {
        fs::create_dir(destination).map_err(|_| Error::storage())?;
        for entry in fs::read_dir(source).map_err(|_| Error::storage())? {
            let entry = entry.map_err(|_| Error::storage())?;
            copy_entry(&entry.path(), &destination.join(entry.file_name()), cancel)?;
        }
    } else if metadata.is_file() {
        fs::copy(source, destination).map_err(|_| Error::storage())?;
        fs::set_permissions(destination, metadata.permissions()).map_err(|_| Error::storage())?;
        let after = fs::symlink_metadata(source).map_err(|_| Error::storage())?;
        if metadata.len() != after.len() || metadata.modified().ok() != after.modified().ok() {
            return Err(Error::new(
                ErrorCode::Incomplete,
                "source changed during clean checkout preparation",
            ));
        }
    } else {
        return Err(Error::new(
            ErrorCode::Unsupported,
            "source contains an unsupported filesystem object",
        ));
    }
    Ok(())
}
async fn include_worktree(
    root: &Path,
    selected: &Path,
    temporary: &Path,
    env: &[(OsString, OsString)],
    cancel: &CancellationToken,
    revision: &str,
) -> Result<()> {
    let patch = temporary.join("tracked.patch");
    let file = fs::File::create(&patch).map_err(|_| Error::storage())?;
    let mut command = git(root, env);
    command
        .args([
            "diff",
            "--binary",
            "--no-ext-diff",
            "--no-textconv",
            revision,
            "--",
        ])
        .stdout(file);
    managed_git(command, false, cancel).await?;
    if fs::metadata(&patch).map_err(|_| Error::storage())?.len() > 0 {
        run_git(
            selected,
            &[
                OsStr::new("apply"),
                OsStr::new("--binary"),
                OsStr::new("--whitespace=nowarn"),
                patch.as_os_str(),
            ],
            env,
            cancel,
        )
        .await?;
    }
    let names = git_output(
        root,
        &[
            OsStr::new("ls-files"),
            OsStr::new("--others"),
            OsStr::new("--exclude-standard"),
            OsStr::new("-z"),
        ],
        env,
        cancel,
    )
    .await?;
    for name in names
        .split(|byte| *byte == 0)
        .filter(|name| !name.is_empty())
    {
        let name = std::str::from_utf8(name).map_err(|_| {
            Error::new(
                ErrorCode::Unsupported,
                "non-UTF-8 source paths are unsupported",
            )
        })?;
        let relative = Path::new(name);
        if relative.is_absolute()
            || relative
                .components()
                .any(|part| !matches!(part, std::path::Component::Normal(_)))
        {
            return Err(Error::input("invalid Git source path"));
        }
        let destination = selected.join(relative);
        let parent = destination.parent().ok_or_else(Error::storage)?;
        let mut ancestor = selected.to_owned();
        if let Some(parent) = relative.parent() {
            for component in parent.components() {
                ancestor.push(component);
                if fs::symlink_metadata(&ancestor).is_ok_and(|m| m.is_symlink()) {
                    return Err(Error::input("untracked source path traverses a symlink"));
                }
            }
        }
        fs::create_dir_all(parent).map_err(|_| Error::storage())?;
        copy_entry(&root.join(relative), &destination, cancel)?;
    }
    fs::remove_file(patch).map_err(|_| Error::storage())?;
    Ok(())
}
pub async fn verify(
    root: &Path,
    name: &str,
    config: &Config,
    runs: u32,
    include: bool,
    baseline: Option<&Report>,
    cancel: CancellationToken,
) -> Result<Report> {
    if runs == 0 || runs > 32 {
        return Err(Error::input(
            "verification run count must be between 1 and 32",
        ));
    }
    let command = config
        .commands
        .get(name)
        .ok_or_else(|| Error::input("configured command was not found"))?;
    if runs > 1 && command.outputs.is_empty() {
        return Err(Error::input(
            "repeat verification requires declared outputs",
        ));
    }
    let revision = revision(root, &cancel)
        .await
        .ok_or_else(|| Error::input("clean verification requires a repository HEAD"))?;
    let owned = crate::temporary::Directory::new("runlens-clean-").map_err(|_| Error::storage())?;
    let result = verify_in(
        root,
        name,
        config,
        command,
        runs,
        include,
        baseline,
        &revision,
        owned.path(),
        cancel,
    )
    .await;
    let cleanup = owned.close();
    match (result, cleanup) {
        (Ok(mut report), Err(_)) => {
            report.verification = Some(Verdict::Inconclusive);
            if let Some(last) = report.executions.last_mut() {
                last.outcome.errors.push(ErrorCode::CleanupFailed);
                last.outcome.collection_complete = false;
            }
            Ok(report)
        }
        (Err(_), Err(_)) => Err(Error::new(
            ErrorCode::CleanupFailed,
            "clean checkout cleanup failed; inspect the runlens-clean-prefixed OS temporary \
             directory",
        )),
        (result, Ok(())) => result,
    }
}
#[allow(clippy::too_many_arguments)]
async fn verify_in(
    root: &Path,
    name: &str,
    config: &Config,
    command: &Command,
    runs: u32,
    include: bool,
    baseline: Option<&Report>,
    revision: &str,
    owned: &Path,
    cancel: CancellationToken,
) -> Result<Report> {
    tracing::info!(
        stage = "source-preparation",
        runs,
        "freezing selected source"
    );
    let env = isolated_environment(&owned.join("source-environment"), &[])?;
    let selected = owned.join("selected");
    // Git parses its source argument as a repository locator. Windows verbatim
    // filesystem prefixes are OS paths, not Git's UNC/URL locator grammar.
    #[cfg(windows)]
    let source_locator = OsString::from(crate::privacy::normalized(root));
    #[cfg(not(windows))]
    let source_locator = root.as_os_str().to_owned();
    run_git(
        owned,
        &[
            OsStr::new("clone"),
            OsStr::new("--no-checkout"),
            OsStr::new("--no-local"),
            OsStr::new("--"),
            &source_locator,
            selected.as_os_str(),
        ],
        &env,
        &cancel,
    )
    .await?;
    run_git(
        &selected,
        &[
            OsStr::new("checkout"),
            OsStr::new("--detach"),
            OsStr::new(revision),
        ],
        &env,
        &cancel,
    )
    .await?;
    if include {
        include_worktree(root, &selected, owned, &env, &cancel, revision).await?;
    }
    let mut report = Report::new(if runs == 1 {
        ReportKind::Clean
    } else {
        ReportKind::Repeat
    });
    report.limitations.push(
        if include {
            "Source includes selected tracked changes and nonignored untracked files frozen before \
             repetitions."
        } else {
            "Source is repository HEAD; uncommitted changes and untracked files are excluded."
        }
        .into(),
    );
    report.limitations.push(
        "Temporary checkout isolation is not an OS security boundary. Commands retain host \
         permissions and network access. Submodules and dependency installation require explicit \
         preparation."
            .into(),
    );
    for repetition in 1..=runs {
        if cancel.is_cancelled() {
            if report.executions.is_empty() {
                return Err(Error::new(
                    ErrorCode::Cancelled,
                    "verification cancelled before execution",
                ));
            }
            if let Some(last) = report.executions.last_mut() {
                last.outcome.errors.push(ErrorCode::Cancelled);
                last.outcome.collection_complete = false;
            }
            report.verification = Some(Verdict::Inconclusive);
            break;
        }
        let round = owned.join(format!("round-{repetition}"));
        fs::create_dir(&round).map_err(|_| Error::storage())?;
        let workspace = round.join("workspace");
        copy_entry(&selected, &workspace, &cancel)?;
        let workspace = workspace.canonicalize().map_err(|_| Error::storage())?;
        let environment = isolated_environment(&round.join("environment"), &command.env)?;
        let mut prepared = true;
        for argv in &command.prepare {
            let mut preparation = Command::direct(argv.clone());
            preparation.cwd = command.cwd.clone();
            preparation.env = command.env.clone();
            preparation.timeout_ms = command.timeout_ms;
            let execution = execute::observe(Request {
                root: &workspace,
                command: &preparation,
                name: Some(name),
                config,
                environment: environment.clone(),
                temporary: vec![round.join("environment")],
                revision: Some(revision.into()),
                working_tree_included: include,
                role: Role::Preparation,
                repetition,
                cancellation: cancel.clone(),
            })
            .await?;
            prepared &= execution.outcome.success();
            report.executions.push(execution);
            if !prepared {
                break;
            }
        }
        if !prepared {
            report.verification = Some(Verdict::Failed);
            break;
        }
        let execution = execute::observe(Request {
            root: &workspace,
            command,
            name: Some(name),
            config,
            environment,
            temporary: vec![round.join("environment")],
            revision: Some(revision.into()),
            working_tree_included: include,
            role: Role::Target,
            repetition,
            cancellation: cancel.clone(),
        })
        .await?;
        let succeeded = execution.outcome.success();
        report.executions.push(execution);
        if !succeeded {
            report.verification = Some(Verdict::Failed);
            break;
        }
        fs::remove_dir_all(round)
            .map_err(|_| Error::new(ErrorCode::CleanupFailed, "repetition cleanup failed"))?;
    }
    if report.targets().count() == runs as usize
        && report.executions.iter().all(|e| e.outcome.success())
    {
        let checks = analysis::policy(&report, &config.policy, &config.commands, baseline)?;
        report.findings = checks.findings;
        report.verification = checks.verdict;
        if runs > 1 {
            let comparison = analysis::repeated_outputs(&report, &command.outputs)?;
            merge_findings(&mut report, &comparison)?;
            if comparison.verdict != Some(Verdict::Passed) {
                report.verification = comparison.verdict;
            }
        } else if let Some(baseline) = baseline {
            let comparison = analysis::compare(baseline, &report, &[])?;
            // Baseline findings can refer to the explicitly supplied baseline;
            // include that metadata so every saved evidence reference is closed.
            for execution in &baseline.executions {
                if !report.executions.iter().any(|e| e.id == execution.id) {
                    let mut execution = execution.clone();
                    execution.role = Role::Preparation;
                    report.executions.push(execution);
                }
            }
            merge_findings(&mut report, &comparison)?;
            if comparison.verdict != Some(Verdict::Passed) {
                report.verification = Some(Verdict::Inconclusive);
            } else if !comparison.differences.is_empty() || !comparison.findings.is_empty() {
                report.verification = Some(Verdict::Failed);
            }
        } else {
            report.limitations.push(
                "No baseline was supplied; successful clean execution does not establish \
                 equivalence to the current worktree."
                    .into(),
            );
        }
    } else if report.verification.is_none() {
        report.verification = Some(Verdict::Inconclusive);
    }
    Ok(report)
}
fn merge_findings(report: &mut Report, analysis: &analysis::Analysis) -> Result<()> {
    for item in analysis.findings.iter() {
        let (_, finding) = item?;
        report
            .findings
            .insert(format!("f{:08}", report.findings.len()), finding)?;
    }
    Ok(())
}

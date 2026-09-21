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
        // Windows launch context is not ambient configuration on Unix. Unix
        // commands must explicitly select these names when they need them.
        #[cfg(windows)]
        "SystemRoot",
        #[cfg(windows)]
        "WINDIR",
        #[cfg(windows)]
        "COMSPEC",
        #[cfg(windows)]
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
        // Windows environment keys are case-insensitive, including Git's
        // repository and configuration controls passed to preparation commands.
        let git_name = if cfg!(windows) {
            name.to_ascii_uppercase()
        } else {
            name.clone()
        };
        if ISOLATED_VARIABLES
            .iter()
            .any(|key| key.eq_ignore_ascii_case(name))
            || git_name.starts_with("GIT_CONFIG")
            || git_name == "GIT_DIR"
            || git_name == "GIT_WORK_TREE"
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
        // Resolve aliases such as macOS /var -> /private/var. Git for Windows
        // cannot consume verbatim \\?\ paths in HOME/GIT_CONFIG_GLOBAL, so pass
        // the normalized drive/UNC spelling; the redactor uses that spelling too.
        let directory = directory.canonicalize().map_err(|_| Error::storage())?;
        #[cfg(windows)]
        let directory = PathBuf::from(crate::privacy::normalized(&directory));
        env.push((name.into(), directory.into_os_string()));
    }
    Ok(env)
}
fn git(root: &Path, environment: &[(OsString, OsString)]) -> Result<Process> {
    let home = environment
        .iter()
        .find(|(name, _)| name == "HOME")
        .map(|(_, value)| PathBuf::from(value))
        .ok_or_else(|| Error::input("Git preparation requires an isolated home"))?;
    // Native Windows arm64 Git rejects NUL as a global config file. An empty
    // regular file inside the private home is portable and never loads user
    // configuration. Keep it private even when Git changes null-device support.
    let empty_config = home.join(".runlens-empty-gitconfig");
    match fs::File::create_new(&empty_config) {
        Ok(_) => {}
        Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {
            if !fs::symlink_metadata(&empty_config)
                .is_ok_and(|metadata| metadata.is_file() && metadata.len() == 0)
            {
                return Err(Error::input(
                    "isolated Git configuration is not an empty regular file",
                ));
            }
        }
        Err(_) => return Err(Error::storage()),
    }
    let mut command = Process::new("git");
    #[cfg(windows)]
    let directory = PathBuf::from(crate::privacy::normalized(root));
    #[cfg(not(windows))]
    let directory = root;
    command
        .current_dir(directory)
        .env_clear()
        .envs(environment.iter().map(|(k, v)| (k, v)))
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", empty_config)
        .env("GIT_TERMINAL_PROMPT", "0")
        .env("GIT_CONFIG_COUNT", "0")
        .arg("-c")
        .arg("core.fsmonitor=false")
        .arg("-c")
        .arg("core.hooksPath=/dev/null")
        .stdin(Stdio::null());
    Ok(command)
}
async fn managed_git(
    command: Process,
    capture: bool,
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    use tokio::io::AsyncReadExt;
    let mut command = tokio::process::Command::from(command);
    command.stderr(Stdio::piped());
    if capture {
        command.stdout(Stdio::piped());
    }
    let mut child = fspy::lifecycle::OwnedChild::spawn(command)
        .map_err(|_| Error::input("Git preparation could not be started"))?;
    let stdout = child.child.stdout.take();
    let stderr = child.child.stderr.take();
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
        let diagnostics = async {
            let mut bytes = Vec::new();
            if let Some(mut stderr) = stderr {
                let _ = (&mut stderr).take(8192).read_to_end(&mut bytes).await;
                let _ = tokio::io::copy(&mut stderr, &mut tokio::io::sink()).await;
            }
            // Only a bounded classification escapes this scope. Git text can
            // contain local paths and never enters logs or saved reports.
            let text = String::from_utf8_lossy(&bytes);
            if text.contains("dubious ownership") || text.contains("unsafe repository") {
                GitFailure::Ownership
            } else if text.contains("not a git repository") {
                GitFailure::Repository
            } else if text.contains("config") {
                GitFailure::Configuration
            } else if text.contains("revision") {
                GitFailure::Revision
            } else if text.contains("HOME") || text.contains("profile") {
                GitFailure::Home
            } else if text.contains("chdir") || text.contains("directory") {
                GitFailure::Directory
            } else {
                GitFailure::Other
            }
        };
        tokio::join!(child.wait(local_cancel.clone()), read, diagnostics)
    };
    tokio::pin!(operation);
    let mut timed_out = false;
    let (status, output, reason) = tokio::select! {
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
        tracing::warn!(
            stage = "source-preparation",
            child_exit_code = status.code(),
            reason = ?reason,
            "Git source operation failed"
        );
        return Err(Error::input(
            "Git source selection failed; a readable repository and HEAD are required",
        ));
    }
    Ok(output)
}
#[derive(Debug)]
enum GitFailure {
    Ownership,
    Repository,
    Configuration,
    Revision,
    Home,
    Directory,
    Other,
}
async fn git_output(
    root: &Path,
    args: &[&OsStr],
    env: &[(OsString, OsString)],
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    let mut command = git(root, env)?;
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
    // Git platform shims may need a home even for rev-parse. Supply a fresh
    // location rather than exposing ambient user configuration or credentials.
    let owned = crate::temporary::Directory::new("runlens-revision-").inspect_err(|error| {
        tracing::debug!(stage="source-revision", operation="temporary-directory", io_kind=?error.kind(), "source revision unavailable");
    }).ok()?;
    let environment = isolated_environment(owned.path(), &[]).inspect_err(|error| {
        tracing::debug!(stage="source-revision", operation="environment", code=?error.code, reason=error.message, "source revision unavailable");
    }).ok()?;
    let bytes = git_output(
        root,
        &[
            OsStr::new("rev-parse"),
            OsStr::new("--verify"),
            OsStr::new("HEAD^{commit}"),
        ],
        &environment,
        cancel,
    )
    .await;
    let bytes = match bytes {
        Ok(bytes) => bytes,
        Err(error) => {
            // Error messages are repository-owned static classifications, never
            // Git stderr. Keep lifecycle, launch and read failures distinguishable.
            tracing::debug!(stage="source-revision", operation="git", code=?error.code, reason=error.message, "source revision unavailable");
            return None;
        }
    };
    let value = String::from_utf8(bytes)
        .inspect_err(|_| {
            tracing::debug!(
                stage = "source-revision",
                operation = "decode",
                "source revision was not UTF-8"
            );
        })
        .ok()?
        .trim()
        .to_owned();
    if (value.len() == 40 || value.len() == 64) && value.bytes().all(|b| b.is_ascii_hexdigit()) {
        Some(value)
    } else {
        tracing::debug!(
            stage = "source-revision",
            operation = "validate",
            "source revision has invalid shape"
        );
        None
    }
}
async fn run_git(
    root: &Path,
    args: &[&OsStr],
    environment: &[(OsString, OsString)],
    cancel: &CancellationToken,
) -> Result<()> {
    let mut command = git(root, environment)?;
    command.args(args).stdout(Stdio::null());
    managed_git(command, false, cancel).await.map(|_| ())
}
fn copy_entry(source: &Path, destination: &Path, cancel: &CancellationToken) -> Result<()> {
    copy_source_entry(source, destination, cancel, false)
}
fn copy_source_entry(
    source: &Path,
    destination: &Path,
    cancel: &CancellationToken,
    frozen: bool,
) -> Result<()> {
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
            use std::os::windows::fs::FileTypeExt;
            // Read the link's own reparse type: its target can be absent until
            // preparation runs, so following it would turn a directory link
            // into a file link and change the frozen checkout's semantics.
            if metadata.file_type().is_symlink_dir() {
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
            copy_source_entry(
                &entry.path(),
                &destination.join(entry.file_name()),
                cancel,
                frozen,
            )?;
        }
    } else if metadata.is_file() {
        use std::io::{Read, Write};
        let mut options = fs::OpenOptions::new();
        options.read(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;
            options.custom_flags(0x00200000);
        }
        let mut input = options.open(source).map_err(|_| Error::storage())?;
        if !crate::snapshot::same(&metadata, &input.metadata().map_err(|_| Error::storage())?) {
            return Err(Error::new(
                ErrorCode::Incomplete,
                "source changed during preparation",
            ));
        }
        let mut output = fs::File::create_new(destination).map_err(|_| Error::storage())?;
        let mut buffer = [0u8; 65536];
        loop {
            if cancel.is_cancelled() {
                return Err(Error::new(
                    ErrorCode::Cancelled,
                    "source preparation cancelled",
                ));
            }
            let count = input.read(&mut buffer).map_err(|_| Error::storage())?;
            if count == 0 {
                break;
            }
            output
                .write_all(&buffer[..count])
                .map_err(|_| Error::storage())?;
        }
        if !crate::snapshot::same(&metadata, &input.metadata().map_err(|_| Error::storage())?) {
            return Err(Error::new(
                ErrorCode::Incomplete,
                "source changed during preparation",
            ));
        }
    } else {
        return Err(Error::new(
            ErrorCode::Unsupported,
            "source contains an unsupported filesystem object",
        ));
    }
    let accessed = filetime::FileTime::from_last_access_time(&metadata);
    let modified = filetime::FileTime::from_last_modification_time(&metadata);
    // Set directory times only after descendants exist, and never follow links.
    filetime::set_symlink_file_times(destination, accessed, modified)
        .map_err(|_| Error::storage())?;
    if !metadata.is_symlink() {
        fs::set_permissions(destination, metadata.permissions()).map_err(|_| Error::storage())?;
    }
    let after = fs::symlink_metadata(source).map_err(|_| Error::storage())?;
    if !crate::snapshot::same(&metadata, &after) {
        return Err(Error::new(
            ErrorCode::Incomplete,
            "source changed during clean checkout preparation",
        ));
    }
    if frozen {
        // Reading a frozen owned checkout can itself advance atime. Restore its
        // selected timestamps so later rounds receive the same source metadata.
        // This is never enabled for reads from the user's worktree.
        filetime::set_symlink_file_times(source, accessed, modified)
            .map_err(|_| Error::storage())?;
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
    let mut command = git(root, env)?;
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
    command.validate_argv()?;
    if runs > 1 && command.outputs.is_empty() {
        return Err(Error::input(
            "repeat verification requires declared outputs",
        ));
    }
    let current_executions = (command.prepare.len() + 1)
        .checked_mul(runs as usize)
        .and_then(|count| count.checked_add(baseline.map_or(0, |report| report.executions.len())));
    if current_executions.is_none_or(|count| count > MAX_EXECUTIONS) {
        return Err(Error::input(
            "baseline and requested executions exceed the report execution limit",
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
            if let Some(last) = report
                .executions
                .iter_mut()
                .rev()
                .find(|execution| execution.role != Role::Baseline)
            {
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
    // Check all planned targets/preparations and imported metadata before the
    // first preparation can perform side effects. Future round paths are known
    // without creating them; canonicalize only their already-owned parent.
    let canonical_owned = owned.canonicalize().map_err(|_| Error::storage())?;
    let mut budget = crate::report::MetadataBudget::planned();
    if let Some(baseline) = baseline {
        for execution in &baseline.executions {
            budget.execution(&execution.command, &execution.environment, &execution.scope)?;
        }
    }
    for repetition in 1..=runs {
        let round = canonical_owned.join(format!("round-{repetition}"));
        let workspace = round.join("workspace");
        let redactor = crate::privacy::Redactor::new(
            &workspace,
            &[&round.join("environment")],
            &config.redaction,
        )?;
        for argv in &command.prepare {
            let mut preparation = Command::direct(argv.clone());
            preparation.env = command.env.clone();
            let (identity, environment, scope) = execute::metadata(
                &preparation,
                Some(name),
                &workspace.join(&command.cwd),
                config,
                &redactor,
                Some(revision),
                include,
            );
            budget.execution(&identity, &environment, &scope)?;
        }
        let (identity, environment, scope) = execute::metadata(
            command,
            Some(name),
            &workspace.join(&command.cwd),
            config,
            &redactor,
            Some(revision),
            include,
        );
        budget.execution(&identity, &environment, &scope)?;
    }
    let mut expected = std::collections::BTreeMap::new();
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
        copy_source_entry(&selected, &workspace, &cancel, true)?;
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
            report.verification = Some(stopped_verdict(&report.executions.last().unwrap().outcome));
            break;
        }
        expected =
            crate::privacy::command_identities(config, &workspace, &[&round.join("environment")])?;
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
        let stopped = stopped_verdict(&execution.outcome);
        report.executions.push(execution);
        if !succeeded {
            report.verification = Some(stopped);
            break;
        }
        fs::remove_dir_all(round)
            .map_err(|_| Error::new(ErrorCode::CleanupFailed, "repetition cleanup failed"))?;
    }
    // Partial evidence can prove a policy failure even when collection could
    // not prove success. Preserve that failure ahead of an inconclusive result.
    let checks = analysis::policy(
        &report,
        &config.policy,
        &config.commands,
        &expected,
        baseline,
    )?;
    report.findings = checks.findings;
    report.verification = merge_verdict(report.verification, checks.verdict);
    if report.targets().count() == runs as usize
        && report.executions.iter().all(|e| e.outcome.success())
    {
        if runs > 1 {
            let comparison = analysis::repeated_outputs(&report, &command.outputs)?;
            merge_findings(&mut report, &comparison)?;
            report.verification = merge_verdict(report.verification, comparison.verdict);
        } else if let Some(baseline) = baseline {
            let comparison = analysis::compare(baseline, &report, &[])?;
            merge_findings(&mut report, &comparison)?;
            let verdict = if comparison.verdict == Some(Verdict::Failed) {
                Verdict::Failed
            } else if comparison.verdict != Some(Verdict::Passed) {
                Verdict::Inconclusive
            } else if !comparison.differences.is_empty() || !comparison.findings.is_empty() {
                Verdict::Failed
            } else {
                Verdict::Passed
            };
            report.verification = merge_verdict(report.verification, Some(verdict));
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
    // Policy findings can reference historical evidence even when preparation
    // or target execution stopped early. Close those references on every path.
    if let Some(baseline) = baseline {
        for execution in &baseline.executions {
            if !report.executions.iter().any(|e| e.id == execution.id) {
                let mut execution = execution.clone();
                execution.role = Role::Baseline;
                report.executions.push(execution);
            }
        }
    }
    Ok(report)
}
fn stopped_verdict(outcome: &Outcome) -> Verdict {
    if outcome.child_exit_code.is_some_and(|code| code != 0)
        || outcome.child_signal.is_some()
        || outcome.errors.contains(&ErrorCode::CommandFailed)
    {
        Verdict::Failed
    } else {
        Verdict::Inconclusive
    }
}
fn merge_verdict(left: Option<Verdict>, right: Option<Verdict>) -> Option<Verdict> {
    match (left, right) {
        (Some(Verdict::Failed), _) | (_, Some(Verdict::Failed)) => Some(Verdict::Failed),
        (Some(Verdict::Inconclusive), _) | (_, Some(Verdict::Inconclusive)) => {
            Some(Verdict::Inconclusive)
        }
        (Some(Verdict::Passed), _) | (_, Some(Verdict::Passed)) => Some(Verdict::Passed),
        (None, None) => None,
    }
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

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn frozen_copies_preserve_file_directory_and_symlink_timestamps() {
        use filetime::{FileTime, set_symlink_file_times};
        let root = tempfile::tempdir().unwrap();
        let source = root.path().join("source");
        fs::create_dir(&source).unwrap();
        fs::write(source.join("file"), "source").unwrap();
        #[cfg(unix)]
        std::os::unix::fs::symlink("file", source.join("link")).unwrap();
        #[cfg(windows)]
        std::os::windows::fs::symlink_file("file", source.join("link")).unwrap();
        let accessed = FileTime::from_unix_time(1_600_000_000, 123_456_700);
        let modified = FileTime::from_unix_time(1_650_000_000, 987_654_300);
        for name in ["file", "link", ""] {
            set_symlink_file_times(source.join(name), accessed, modified).unwrap();
        }
        let expected: Vec<_> = ["file", "link", ""]
            .into_iter()
            .map(|name| {
                let metadata = fs::symlink_metadata(source.join(name)).unwrap();
                (
                    name,
                    FileTime::from_last_access_time(&metadata),
                    FileTime::from_last_modification_time(&metadata),
                )
            })
            .collect();
        for round in 0..3 {
            let destination = root.path().join(format!("round-{round}"));
            copy_source_entry(&source, &destination, &CancellationToken::new(), true).unwrap();
            for (name, accessed, modified) in &expected {
                for base in [&source, &destination] {
                    let metadata = fs::symlink_metadata(base.join(name)).unwrap();
                    assert_eq!(
                        FileTime::from_last_access_time(&metadata),
                        *accessed,
                        "{name}"
                    );
                    assert_eq!(
                        FileTime::from_last_modification_time(&metadata),
                        *modified,
                        "{name}"
                    );
                }
            }
        }
    }

    #[test]
    fn source_copy_preserves_dangling_link_types_until_targets_exist() {
        let root = tempfile::tempdir().unwrap();
        let source = root.path().join("source");
        let copied = root.path().join("copied");
        fs::create_dir(&source).unwrap();
        fs::create_dir(&copied).unwrap();
        for directory in [false, true] {
            let name = if directory { "dir-link" } else { "file-link" };
            let target = if directory { "directory" } else { "file" };
            #[cfg(unix)]
            std::os::unix::fs::symlink(target, source.join(name)).unwrap();
            #[cfg(windows)]
            if directory {
                std::os::windows::fs::symlink_dir(target, source.join(name)).unwrap();
            } else {
                std::os::windows::fs::symlink_file(target, source.join(name)).unwrap();
            }
            copy_entry(
                &source.join(name),
                &copied.join(name),
                &CancellationToken::new(),
            )
            .unwrap();
            assert_eq!(fs::read_link(copied.join(name)).unwrap(), Path::new(target));
            assert!(!copied.join(name).exists());
            #[cfg(windows)]
            {
                use std::os::windows::fs::FileTypeExt;
                let kind = fs::symlink_metadata(copied.join(name)).unwrap().file_type();
                assert_eq!(kind.is_symlink_dir(), directory);
                assert_eq!(kind.is_symlink_file(), !directory);
            }
            if directory {
                fs::create_dir(copied.join(target)).unwrap();
                fs::write(copied.join(name).join("result"), "directory").unwrap();
                assert_eq!(
                    fs::read_to_string(copied.join(target).join("result")).unwrap(),
                    "directory"
                );
            } else {
                fs::write(copied.join(target), "file").unwrap();
                assert_eq!(fs::read_to_string(copied.join(name)).unwrap(), "file");
            }
            assert!(!source.join(target).exists());
        }
    }
}

use std::{
    ffi::OsString,
    path::{Path, PathBuf},
    process::Stdio,
    sync::atomic::Ordering,
    time::{Duration, Instant},
};

use tokio_util::sync::CancellationToken;

use crate::{
    config::{Command, Config, patterns},
    entries::Entries,
    error::{Error, ErrorCode, Result},
    model::*,
    platform,
    privacy::Redactor,
    snapshot,
};

pub struct Request<'a> {
    pub root: &'a Path,
    pub command: &'a Command,
    pub name: Option<&'a str>,
    pub config: &'a Config,
    pub environment: Vec<(OsString, OsString)>,
    pub temporary: Vec<PathBuf>,
    pub revision: Option<String>,
    pub working_tree_included: bool,
    pub role: Role,
    pub repetition: u32,
    pub cancellation: CancellationToken,
}
pub async fn observe(request: Request<'_>) -> Result<Execution> {
    let id = uuid::Uuid::now_v7();
    let started = Instant::now();
    request.config.limits.validate()?;
    crate::entries::set_memory_limit(request.config.limits.memory_bytes);
    if request.command.argv.is_empty() {
        return Err(Error::input("command argv is required"));
    }
    let cwd = request
        .root
        .join(&request.command.cwd)
        .canonicalize()
        .map_err(|_| Error::input("command working directory does not exist"))?;
    if !cwd.starts_with(request.root) {
        return Err(Error::input(
            "command working directory escapes the workspace",
        ));
    }
    let executable = platform::resolve(&request.command.argv[0], &request.environment, &cwd)?;
    platform::preflight(&executable)?;
    let executable_identity = platform::executable_identity(&executable, &request.cancellation);
    if request.cancellation.is_cancelled() {
        return Err(Error::new(
            ErrorCode::Cancelled,
            "execution cancelled before launch",
        ));
    }
    let owned = crate::temporary::Directory::new("runlens-").map_err(|_| Error::storage())?;
    let mut temporary = request
        .temporary
        .iter()
        .map(|p| p.canonicalize().unwrap_or_else(|_| p.clone()))
        .collect::<Vec<_>>();
    temporary.push(owned.path().canonicalize().map_err(|_| Error::storage())?);
    let refs = temporary.iter().map(PathBuf::as_path).collect::<Vec<_>>();
    let redactor = Redactor::new(request.root, &refs, &request.config.redaction)?;
    let exclusions = request
        .config
        .exclusions
        .iter()
        .chain(request.command.exclusions.iter())
        .cloned()
        .collect::<Vec<_>>();
    tracing::info!(execution_id=%id,stage="before-snapshot","execution starting");
    let before = snapshot::take(
        request.root,
        &exclusions,
        &temporary,
        &redactor,
        &request.config.limits,
        &request.cancellation,
    )?;
    if request.cancellation.is_cancelled() {
        return Err(Error::new(
            ErrorCode::Cancelled,
            "execution cancelled before launch",
        ));
    }
    let mut native = fspy::Command::new(&executable);
    native
        .args(&request.command.argv[1..])
        .current_dir(&cwd)
        .envs(request.environment.iter().map(|(k, v)| (k, v)))
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit());
    // Finite pipe input is preserved. A terminal does not authorize interactive
    // prompts; give the child EOF instead of allocating a PTY.
    use std::io::IsTerminal;
    native.stdin(if std::io::stdin().is_terminal() {
        Stdio::null()
    } else {
        Stdio::inherit()
    });
    let cancel = request.cancellation.child_token();
    let mut timed_out = false;
    // Snapshots can take arbitrarily longer than executable inspection. Check
    // the path again at the launch boundary, including newly protected images.
    platform::preflight(&executable)?;
    let executable_sha256 = executable_identity
        .map(|identity| identity.revalidate(&executable))
        .transpose()?;
    tracing::info!(execution_id=%id,stage="spawn","starting requested command");
    let child = native
        .spawn_in(
            owned.path(),
            (request.config.limits.total_bytes / 3) as usize,
            request.config.limits.max_paths,
            cancel.clone(),
        )
        .await
        .map_err(|_| {
            Error::new(
                ErrorCode::Unsupported,
                "tracing initialization or injection failed before command launch; run runlens \
                 doctor",
            )
        })?;
    let mut wait = child.wait_handle;
    let timeout = async {
        match request.command.timeout_ms {
            Some(ms) => tokio::time::sleep(Duration::from_millis(ms)).await,
            None => std::future::pending().await,
        }
    };
    let termination = tokio::select! {
        result=&mut wait=>result,
        ()=timeout=>{timed_out=true;cancel.cancel();wait.await}
    };
    let mut accesses: Entries<Access> = Entries::new(request.config.limits.memory_bytes / 8);
    let mut errors = Vec::new();
    let mut complete = true;
    let mut exit_code = None;
    #[cfg(unix)]
    let mut signal = None;
    #[cfg(not(unix))]
    let signal = None;
    let matcher = patterns(&exclusions)?;
    match termination {
        Ok(termination) => {
            exit_code = termination.status.code();
            #[cfg(unix)]
            {
                use std::os::unix::process::ExitStatusExt;
                signal = termination.status.signal();
            }
            if termination.lifecycle_incomplete {
                complete = false;
            }
            match termination.path_accesses {
                Ok(observations) => {
                    tracing::debug!(execution_id=%id, stage="collection", attached=observations.attached(), lost=observations.incomplete(), lifecycle_incomplete=termination.lifecycle_incomplete, "native collection status");
                    complete &= !observations.incomplete() && observations.attached();
                    for observation in observations.iter() {
                        if observation.mode.contains(fspy::AccessMode::ATTACHED) {
                            continue;
                        }
                        let raw_path = observation.path.to_path_buf();
                        let path = raw_path.as_path();
                        if temporary
                            .iter()
                            .any(|temporary| crate::privacy::within_root(path, temporary))
                        {
                            continue;
                        }
                        let key = redactor.path(path);
                        let mut value = accesses.get(&key)?.unwrap_or(Access {
                            read: false,
                            write: false,
                            read_directory: false,
                            unsupported: false,
                            in_scope: observed_in_scope(
                                path,
                                request.root,
                                &before.entries,
                                &redactor,
                            ) && !snapshot::excluded(
                                path,
                                request.root,
                                &matcher,
                                &temporary,
                            ),
                        });
                        value.read |= observation.mode.contains(fspy::AccessMode::READ);
                        value.write |= observation.mode.contains(fspy::AccessMode::WRITE);
                        value.read_directory |=
                            observation.mode.contains(fspy::AccessMode::READ_DIR);
                        value.unsupported |=
                            observation.mode.contains(fspy::AccessMode::UNSUPPORTED);
                        if value.unsupported {
                            complete = false;
                        }
                        if accesses.len() >= request.config.limits.max_paths
                            && accesses.get(&key)?.is_none()
                        {
                            complete = false;
                            break;
                        }
                        accesses.insert(key, value)?;
                    }
                }
                Err(_) => complete = false,
            }
            if !termination.status.success() {
                errors.push(ErrorCode::CommandFailed);
            }
        }
        Err(_) => {
            complete = false;
            errors.push(ErrorCode::CleanupFailed);
        }
    }
    if timed_out {
        errors.push(ErrorCode::Timeout);
    } else if request.cancellation.is_cancelled() {
        errors.push(ErrorCode::Cancelled);
    }
    if !complete {
        errors.push(ErrorCode::Incomplete);
    }
    tracing::info!(execution_id=%id,stage="after-snapshot",paths=accesses.len(),collection_complete=complete,"command completed");
    // A cancelled scan is incomplete; it must not invent the missing after-state.
    let after = snapshot::take(
        request.root,
        &exclusions,
        &temporary,
        &redactor,
        &request.config.limits,
        &request.cancellation,
    )?;
    complete &= before.complete && after.complete;
    tracing::debug!(execution_id=%id, stage="snapshots", before_complete=before.complete, after_complete=after.complete, "snapshot coverage status");
    if !complete && !errors.contains(&ErrorCode::Incomplete) {
        errors.push(ErrorCode::Incomplete);
    }
    let changes = snapshot::changes(
        &before.entries,
        &after.entries,
        before.complete,
        after.complete,
    )?;
    // Cancellation can arrive after the child has been reaped, while hashing
    // the after-state or comparing it. Preserve that invocation status too.
    if !timed_out && request.cancellation.is_cancelled() {
        complete = false;
        if !errors.contains(&ErrorCode::Cancelled) {
            errors.push(ErrorCode::Cancelled);
        }
        if !errors.contains(&ErrorCode::Incomplete) {
            errors.push(ErrorCode::Incomplete);
        }
    }
    let redacted_paths = redactor.path_redacted.load(Ordering::Relaxed);
    if redacted_paths {
        complete = false;
        if !errors.contains(&ErrorCode::Incomplete) {
            errors.push(ErrorCode::Incomplete);
        }
    }
    let mut execution = Execution {
        id,
        role: request.role,
        repetition: request.repetition,
        command: Identity {
            name: request.name.map(|s| redactor.text(s)),
            argv: redactor.argv(&request.command.argv),
            cwd: redactor.path(&cwd),
        },
        environment: Environment {
            os: std::env::consts::OS.into(),
            architecture: std::env::consts::ARCH.into(),
            os_version: platform::os_version(),
            runlens_version: env!("CARGO_PKG_VERSION").into(),
            engine_version: ENGINE_REVISION.into(),
            executable_sha256,
            source_revision: request.revision,
            working_tree_included: request.working_tree_included,
            environment_names: request
                .command
                .env
                .iter()
                .map(|name| redactor.text(name))
                .collect(),
        },
        scope: Scope {
            root: "${workspace}".into(),
            exclusions: exclusions.iter().map(|s| redactor.text(s)).collect(),
            input_patterns: request
                .command
                .inputs
                .iter()
                .map(|s| redactor.text(s))
                .collect(),
            output_patterns: request
                .command
                .outputs
                .iter()
                .map(|s| redactor.text(s))
                .collect(),
            before_complete: before.complete,
            after_complete: after.complete,
            redacted_paths,
        },
        before: before.entries,
        after: after.entries,
        accesses,
        changes,
        outcome: Outcome {
            child_exit_code: exit_code,
            child_signal: signal,
            collection_complete: complete,
            errors,
            elapsed_ms: started.elapsed().as_millis().min(u64::MAX as u128) as u64,
        },
    };
    if owned.close().is_err() {
        execution.outcome.collection_complete = false;
        execution.outcome.errors.push(ErrorCode::CleanupFailed);
        tracing::error!(execution_id=%id,stage="cleanup",code="cleanup-failed","private execution directory cleanup failed");
    }
    tracing::info!(execution_id=%id,stage="complete",elapsed_ms=execution.outcome.elapsed_ms,"execution evidence ready");
    Ok(execution)
}

fn observed_in_scope(
    path: &Path,
    root: &Path,
    before: &Entries<FileState>,
    redactor: &Redactor,
) -> bool {
    let normalized = crate::privacy::normalized(path);
    let root_text = crate::privacy::normalized(root);
    if crate::privacy::strip_path_root(&normalized, &root_text).is_none()
        || path
            .components()
            .any(|part| matches!(part, std::path::Component::ParentDir))
    {
        return false;
    }
    for ancestor in path.ancestors() {
        if crate::privacy::strip_path_root(&crate::privacy::normalized(ancestor), &root_text)
            == Some("")
        {
            break;
        }
        if std::fs::symlink_metadata(ancestor).is_ok_and(|metadata| metadata.is_symlink()) {
            return false;
        }
        if before
            .get(&redactor.path(ancestor))
            .ok()
            .flatten()
            .is_some_and(|state| {
                state.kind == Some(FileKind::Symlink) || state.knowledge == Knowledge::Unknown
            })
        {
            return false;
        }
    }
    true
}

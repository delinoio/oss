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
    observe_inner(request, || {}).await
}
#[cfg(feature = "test-support")]
pub async fn observe_with_launch_hook(
    request: Request<'_>,
    hook: impl FnOnce(),
) -> Result<Execution> {
    observe_inner(request, hook).await
}
async fn observe_inner(request: Request<'_>, hook: impl FnOnce()) -> Result<Execution> {
    let id = uuid::Uuid::now_v7();
    let started = Instant::now();
    request.config.limits.validate()?;
    crate::entries::set_memory_limit(request.config.limits.memory_bytes);
    request.command.validate_argv()?;
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
    let storage = crate::temporary::storage_root().map_err(|_| Error::storage())?;
    let owned = crate::temporary::Directory::new_in(storage.path(), "execution-")
        .map_err(|_| Error::storage())?;
    let mut temporary = request
        .temporary
        .iter()
        .map(|p| p.canonicalize().unwrap_or_else(|_| p.clone()))
        .collect::<Vec<_>>();
    let storage_root = storage
        .path()
        .canonicalize()
        .map_err(|_| Error::storage())?;
    // Callers may enumerate the original temporary alias (macOS /var, or a
    // selected symlink) while descriptor observations use its canonical path.
    let storage_paths = [storage_root.clone(), storage.path().to_owned()];
    temporary.push(storage_root.clone());
    temporary.push(storage.path().to_owned());
    let refs = temporary.iter().map(PathBuf::as_path).collect::<Vec<_>>();
    let redactor = Redactor::new(request.root, &refs, &request.config.redaction)?;
    let exclusions = request
        .config
        .exclusions
        .iter()
        .chain(request.command.exclusions.iter())
        .cloned()
        .collect::<Vec<_>>();
    let (identity, mut environment, mut scope) = metadata(
        request.command,
        request.name,
        &cwd,
        request.config,
        &redactor,
        request.revision.as_deref(),
        request.working_tree_included,
    );
    crate::report::MetadataBudget::planned().execution(&identity, &environment, &scope)?;
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
    // Linux resolves the retained descriptor at exec, including across renames.
    #[cfg(target_os = "linux")]
    let launch_path = executable_identity
        .as_ref()
        .map(|identity| identity.launch_path())
        .unwrap_or_else(|| executable.clone());
    #[cfg(windows)]
    let launch_guard = executable_identity
        .as_ref()
        .map(|identity| platform::lock_launch(&executable, identity))
        .transpose()?;
    #[cfg(windows)]
    let launch_path = launch_guard
        .as_ref()
        .map(|guard| guard.path.clone())
        .unwrap_or_else(|| executable.clone());
    #[cfg(target_os = "macos")]
    let launch_path = executable.clone();
    let mut native = fspy::Command::new(&launch_path);
    #[cfg(unix)]
    native.arg0(&request.command.argv[0]);
    native
        .args(&request.command.argv[1..])
        .current_dir(&cwd)
        .envs(request.environment.iter().map(|(k, v)| (k, v)))
        .stdout(Stdio::inherit())
        .stderr(Stdio::inherit());
    // Finite pipe input is preserved. A terminal does not authorize interactive
    // prompts; give the child EOF instead of allocating a PTY.
    use std::io::IsTerminal;
    let stdin_inherited = !std::io::stdin().is_terminal();
    let stdio_complete = platform::inherited_stdio_complete(stdin_inherited);
    native.stdin(if !stdin_inherited {
        Stdio::null()
    } else {
        Stdio::inherit()
    });
    let cancel = request.cancellation.child_token();
    let mut timed_out = false;
    // Snapshots can take arbitrarily longer than executable inspection. Check
    // the path again at the launch boundary, including newly protected images.
    platform::preflight(&executable)?;
    let mut executable_sha256 = executable_identity
        .as_ref()
        .map(|identity| identity.revalidate(&executable))
        .transpose()?;
    hook();
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
    #[cfg(windows)]
    drop(launch_guard);
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
    let mut path_mutations = Entries::new(request.config.limits.memory_bytes / 8);
    let mut errors = Vec::new();
    let mut complete = stdio_complete;
    tracing::debug!(execution_id=%id, stage="inherited-stdio", collection_complete=stdio_complete, "standard stream coverage");
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
                    #[cfg(target_os = "macos")]
                    if !executable_identity.as_ref().is_some_and(|identity| {
                        identity.matches_image(observations.executable_id())
                    }) {
                        executable_sha256 = None;
                    }
                    for observation in observations.iter() {
                        if observation
                            .mode
                            .intersects(fspy::AccessMode::ATTACHED | fspy::AccessMode::IMAGE)
                        {
                            continue;
                        }
                        let raw_path = observation.path.to_path_buf();
                        #[cfg(target_os = "linux")]
                        let raw_path = if raw_path == launch_path {
                            executable.clone()
                        } else {
                            raw_path
                        };
                        let path = raw_path.as_path();
                        // Include retained baseline and snapshot spill indexes in
                        // tracer-private storage. Fresh HOME/cache remains visible.
                        if storage_paths
                            .iter()
                            .any(|root| crate::privacy::within_root(path, root))
                        {
                            continue;
                        }
                        let key = redactor.path(path);
                        if observation.mode.contains(fspy::AccessMode::PATH_MUTATION) {
                            // Dot/separator aliases name the same directory entry.
                            // Parent traversal may cross links and cannot be
                            // safely collapsed using only the final tree.
                            if path
                                .components()
                                .any(|part| matches!(part, std::path::Component::ParentDir))
                            {
                                complete = false;
                            }
                            let canonical_spelling: PathBuf = path.components().collect();
                            path_mutations.insert(redactor.path(&canonical_spelling), true)?;
                        }
                        let mut value = accesses.get(&key)?.unwrap_or(Access {
                            read: false,
                            write: false,
                            read_directory: false,
                            unsupported: false,
                            in_scope: true,
                        });
                        // Merge conservatively even if redaction unifies separate raw
                        // observations: one following access invalidates a link leaf.
                        let nofollow_only =
                            observation.mode.contains(fspy::AccessMode::READ_NOFOLLOW)
                                && !observation.mode.intersects(
                                    fspy::AccessMode::READ
                                        | fspy::AccessMode::WRITE
                                        | fspy::AccessMode::READ_DIR,
                                );
                        value.in_scope &=
                            observed_in_scope(
                                path,
                                request.root,
                                &before.entries,
                                &redactor,
                                nofollow_only,
                            ) && !snapshot::excluded(path, request.root, &matcher, &temporary);
                        value.read |= observation
                            .mode
                            .intersects(fspy::AccessMode::READ | fspy::AccessMode::READ_NOFOLLOW);
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
    let uncertain_ancestors = invalidate_mutated_ancestors(&mut accesses, &path_mutations)?;
    if uncertain_ancestors != 0 {
        complete = false;
        tracing::debug!(execution_id=%id, stage="ancestor-identity", affected_paths=uncertain_ancestors,
            "access identity is uncertain beneath a replaced ancestor");
    }
    if executable_identity
        .as_ref()
        .and_then(|identity| identity.stable_digest())
        != executable_sha256
        || executable_sha256.is_none()
    {
        executable_sha256 = None;
        complete = false;
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
    environment.executable_sha256 = executable_sha256;
    scope.before_complete = before.complete;
    scope.after_complete = after.complete;
    scope.redacted_paths = redacted_paths;
    let mut execution = Execution {
        id,
        role: request.role,
        repetition: request.repetition,
        command: identity,
        environment,
        scope,
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

#[allow(clippy::too_many_arguments)]
pub(crate) fn metadata(
    command: &Command,
    name: Option<&str>,
    cwd: &Path,
    config: &Config,
    redactor: &Redactor,
    revision: Option<&str>,
    included: bool,
) -> (Identity, Environment, Scope) {
    (
        Identity {
            name: name.map(|value| redactor.text(value)),
            argv: redactor.argv(&command.argv),
            cwd: redactor.path(cwd),
        },
        Environment {
            os: std::env::consts::OS.into(),
            architecture: std::env::consts::ARCH.into(),
            os_version: platform::os_version(),
            runlens_version: env!("CARGO_PKG_VERSION").into(),
            engine_version: ENGINE_REVISION.into(),
            // Reserve a complete digest before launch even if hashing later fails.
            executable_sha256: Some("0".repeat(64)),
            source_revision: revision.map(str::to_owned),
            working_tree_included: included,
            environment_names: command.env.iter().map(|name| redactor.text(name)).collect(),
        },
        Scope {
            root: "${workspace}".into(),
            exclusions: config
                .exclusions
                .iter()
                .chain(&command.exclusions)
                .map(|value| redactor.text(value))
                .collect(),
            input_patterns: command
                .inputs
                .iter()
                .map(|value| redactor.text(value))
                .collect(),
            output_patterns: command
                .outputs
                .iter()
                .map(|value| redactor.text(value))
                .collect(),
            before_complete: false,
            after_complete: false,
            redacted_paths: false,
        },
    )
}

// The aggregate event stream has no ordered directory identities. Even matching
// before/after directories cannot disprove a temporary rename/link/restore.
// Replacement/removal attempts on an ancestor invalidate descendant evidence.
// Ordinary mkdir or metadata writes cannot replace an established directory.
// Keep this index redacted and within the shared retained-memory/spill budget.
fn invalidate_mutated_ancestors(
    accesses: &mut Entries<Access>,
    writes: &Entries<bool>,
) -> Result<usize> {
    if writes.is_empty() {
        return Ok(0);
    }
    let mut budget = 4_000_000usize;
    let mut affected = 0;
    for record in accesses.iter() {
        let (path, mut access) = record?;
        if !access.in_scope {
            continue;
        }
        let mut ancestor = path.as_str();
        while let Some((parent, _)) = ancestor.rsplit_once('/') {
            ancestor = parent;
            if ancestor_written(ancestor, writes, &mut budget)? {
                access.in_scope = false;
                access.unsupported = true;
                accesses.insert(path, access)?;
                affected += 1;
                break;
            }
        }
    }
    Ok(affected)
}

fn ancestor_written(path: &str, writes: &Entries<bool>, budget: &mut usize) -> Result<bool> {
    // Exhaustion cannot establish stable ancestry. Windows aliases require its
    // native ordinal comparison; avoid Unicode lowercasing or quadratic scans.
    if *budget == 0 {
        return Ok(true);
    }
    *budget -= 1;
    if writes.get(path)?.is_some() {
        return Ok(true);
    }
    #[cfg(windows)]
    for record in writes.iter() {
        if *budget == 0 {
            return Ok(true);
        }
        *budget -= 1;
        let (written, _) = record?;
        if crate::privacy::windows_query_eq(path, &written).0 {
            return Ok(true);
        }
    }
    Ok(false)
}

fn observed_in_scope(
    path: &Path,
    root: &Path,
    before: &Entries<FileState>,
    redactor: &Redactor,
    nofollow_only: bool,
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
        let current = std::fs::symlink_metadata(ancestor).ok();
        let Ok(previous) = before.get(&redactor.path(ancestor)) else {
            return false;
        };
        if previous
            .as_ref()
            .is_some_and(|state| state.knowledge == Knowledge::Unknown)
        {
            return false;
        }
        let link = current
            .as_ref()
            .is_some_and(|metadata| metadata.is_symlink())
            || previous
                .as_ref()
                .is_some_and(|state| state.kind == Some(FileKind::Symlink));
        if link {
            // Only an unchanged, known leaf can be a no-follow input. Link
            // ancestors and any aggregate following access remain uncertain.
            if ancestor != path
                || !nofollow_only
                || !current
                    .as_ref()
                    .is_some_and(|metadata| metadata.is_symlink())
                || !previous.as_ref().is_some_and(|state| {
                    state.knowledge == Knowledge::Known
                        && state.kind == Some(FileKind::Symlink)
                        && std::fs::read_link(path)
                            .ok()
                            .and_then(|target| target.to_str().map(|text| redactor.text(text)))
                            == state.link_target
                })
            {
                return false;
            }
        }
        // A missing leaf is a valid failed attempt. Every ancestor must be a
        // known directory both before and after execution: a removed or newly
        // created directory cannot rule out a transient external symlink.
        if ancestor != path
            && (!current.as_ref().is_some_and(|metadata| metadata.is_dir())
                || !previous.as_ref().is_some_and(|state| {
                    state.knowledge == Knowledge::Known && state.kind == Some(FileKind::Directory)
                }))
        {
            return false;
        }
    }
    true
}

#[cfg(test)]
mod ancestry_tests {
    use super::*;

    #[test]
    fn ancestor_writes_invalidate_descendants_without_changing_leaf_or_sibling_scope() {
        let mut accesses = Entries::new(0);
        for (path, write) in [
            ("${workspace}/dir", true),
            ("${workspace}/dir/input", false),
            ("${workspace}/output", true),
            ("${workspace}/other/input", false),
        ] {
            accesses
                .insert(
                    path.into(),
                    Access {
                        read: !write,
                        write,
                        read_directory: false,
                        unsupported: false,
                        in_scope: true,
                    },
                )
                .unwrap();
        }
        let mut replacements = Entries::new(0);
        replacements
            .insert("${workspace}/dir".into(), true)
            .unwrap();
        assert_eq!(
            invalidate_mutated_ancestors(&mut accesses, &replacements).unwrap(),
            1
        );
        for path in [
            "${workspace}/dir",
            "${workspace}/output",
            "${workspace}/other/input",
        ] {
            assert!(accesses.get(path).unwrap().unwrap().in_scope);
        }
        let unknown = accesses.get("${workspace}/dir/input").unwrap().unwrap();
        assert!(!unknown.in_scope && unknown.unsupported);
        let writes = Entries::new(0);
        assert!(ancestor_written("${workspace}/unknown", &writes, &mut 0).unwrap());
    }

    #[cfg(windows)]
    #[test]
    fn ancestor_writes_match_native_windows_case_aliases() {
        let mut writes = Entries::new(0);
        writes
            .insert("${workspace}/DIRECTORY".into(), true)
            .unwrap();
        assert!(ancestor_written("${workspace}/directory", &writes, &mut 100).unwrap());
        assert!(!ancestor_written("${workspace}/directory-other", &writes, &mut 100).unwrap());
    }
}

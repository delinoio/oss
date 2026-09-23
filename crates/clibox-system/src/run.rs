//! Local execution controls for `clibox run`.
//!
//! This module deliberately owns only the wrapper lifecycle. Environment
//! planning remains in `environment` so every workload, including a managed
//! service, keeps the exact `run env` assignment and executable lookup rules.

use std::{
    env,
    ffi::OsString,
    fs::{self, File, OpenOptions},
    io::{self, Read, Write},
    path::{Path, PathBuf},
    process::{Child, Command as ProcessCommand, ExitStatus, Stdio},
    sync::{
        atomic::{AtomicBool, Ordering},
        mpsc, Arc, Mutex,
    },
    thread,
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use clap::{Args, Subcommand, ValueEnum};
use fs4::{FileExt, TryLockError};
use rand::Rng;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

use crate::{
    environment,
    error::{Code, Failure, Result},
    runtime,
};

const POLL: Duration = Duration::from_millis(20);
const CLEANUP_CONFIRMATION: Duration = Duration::from_secs(5);
const STATE_VERSION: u8 = 1;
const MAX_EXACT_TOKEN_COUNT: u64 = 1 << 53;
const MAX_BUCKET_STATE_BYTES: u64 = 4 * 1024;

#[derive(Subcommand)]
pub enum Command {
    /// Admit one workload through a shared local token bucket.
    #[command(
        name = "with-rate-limit",
        after_help = "Example: clibox run with-rate-limit --name publish --limit 2 --period 1m -- \
                      npm publish\n\nThe limit admits executions, not concurrent descendants. \
                      Names and project paths are hashed before local state is stored. Exit \
                      codes: 124 admission timeout, 2 invalid arguments, 1 wrapper failure; \
                      natural child status is preserved."
    )]
    RateLimit(RateLimit),
    /// Run one workload while holding a shared local exclusive lock.
    #[command(
        name = "with-lock",
        after_help = "Example: clibox run with-lock --name database-migrate -- pnpm \
                      migrate\n\nSame-key nested locks contend normally and can wait \
                      indefinitely. Exit codes: 75 locked/fail, 0 locked/skip, 124 wait timeout, \
                      2 invalid arguments, 1 wrapper failure; natural child status is preserved."
    )]
    Lock(Lock),
    /// Wait for HTTP readiness before running a workload, optionally owning a
    /// service.
    #[command(
        name = "with-service",
        after_help = "Examples:\n  clibox run with-service http://127.0.0.1:3000/health -- npm test\n  clibox run with-service http://127.0.0.1:3000/health --service node server.js -- npm test\n\nA managed service is started only after a retryable not-ready preflight. The first standalone -- after --service separates service arguments from the workload; -- inside service arguments is unsupported. External services are never terminated. Exit codes: 124 readiness timeout, 2 invalid arguments, 1 wrapper failure; natural child status is preserved."
    )]
    Service(Service),
    /// Retry a workload after eligible nonzero numeric exit statuses.
    #[command(
        name = "with-retry",
        after_help = "Example: clibox run with-retry --max-attempts 5 --jitter none -- cargo \
                      fetch\n\nAttempts share literal argv, prepared environment, cwd, and stdin; \
                      consumed stdin is not replayed. Spawn failures and Unix signal termination \
                      are not retried. Exit codes: 124 overall timeout, 2 invalid arguments, 1 \
                      wrapper failure; the final child status is preserved."
    )]
    Retry(Retry),
    /// Bound a workload's total runtime and optional output-idle interval.
    #[command(
        name = "with-timeout",
        after_help = "Example: clibox run with-timeout --timeout 10m --idle-timeout 30s -- npm \
                      test\n\nAny stdout or stderr bytes reset the idle timer. Output is \
                      forwarded immediately, but workload TTY identity is not guaranteed. A \
                      timeout exits 124 after bounded cleanup."
    )]
    Timeout(Timeout),
}

#[derive(Args)]
pub struct RateLimit {
    #[arg(long, value_parser = parse_name)]
    name: String,
    #[arg(long, value_parser = parse_positive_u64)]
    limit: u64,
    #[arg(long, value_parser = parse_positive_duration)]
    period: Duration,
    #[arg(long, default_value = "1", value_parser = parse_token_count)]
    burst: u64,
    #[command(flatten)]
    scope: ScopeOptions,
    #[arg(long, value_parser = parse_optional_duration)]
    wait_timeout: Option<Duration>,
    #[command(flatten)]
    workload: Workload,
}

#[derive(Args)]
pub struct Lock {
    #[arg(long, value_parser = parse_name)]
    name: String,
    #[command(flatten)]
    scope: ScopeOptions,
    #[arg(long, value_enum, default_value = "wait")]
    on_locked: OnLocked,
    #[arg(long, value_parser = parse_optional_duration)]
    wait_timeout: Option<Duration>,
    #[command(flatten)]
    workload: Workload,
}

#[derive(Args)]
pub struct Service {
    #[arg(value_name = "URL", value_parser = parse_http_url)]
    url: reqwest::Url,
    #[arg(long, value_enum, default_value = "get")]
    method: HttpMethod,
    #[arg(long, value_parser = parse_status)]
    status: Option<u16>,
    #[arg(long, default_value = "250ms", value_parser = parse_positive_duration)]
    interval: Duration,
    #[arg(long, default_value = "3s", value_parser = parse_positive_duration)]
    attempt_timeout: Duration,
    #[arg(long, default_value = "30s", value_parser = parse_optional_duration)]
    ready_timeout: Duration,
    /// Service-only environment assignments followed by its literal command.
    #[arg(long, num_args = 1.., allow_hyphen_values = true, value_terminator = "--", value_name = "KEY=VALUE ... SERVER ARG")]
    service: Option<Vec<OsString>>,
    #[command(flatten)]
    workload: Workload,
}

#[derive(Args)]
pub struct Retry {
    #[arg(long, default_value = "3", value_parser = parse_positive_u32)]
    max_attempts: u32,
    #[arg(long, default_value = "1s", value_parser = parse_positive_duration)]
    delay: Duration,
    #[arg(long, default_value = "2", value_parser = parse_positive_u32)]
    backoff_factor: u32,
    #[arg(long, default_value = "30s", value_parser = parse_positive_duration)]
    max_delay: Duration,
    #[arg(long, value_enum, default_value = "full")]
    jitter: Jitter,
    #[arg(long, value_name = "CODE", value_parser = parse_exit_code)]
    retry_exit_code: Vec<i32>,
    #[arg(long, value_parser = parse_optional_duration)]
    timeout: Option<Duration>,
    #[command(flatten)]
    workload: Workload,
}

#[derive(Args)]
pub struct Timeout {
    #[arg(long, value_parser = parse_optional_duration)]
    timeout: Option<Duration>,
    #[arg(long, value_parser = parse_optional_duration)]
    idle_timeout: Option<Duration>,
    #[command(flatten)]
    workload: Workload,
}

#[derive(Args)]
struct Workload {
    #[arg(long, default_value = "5s", value_parser = parse_optional_duration)]
    kill_after: Duration,
    /// Assignments followed by the command and its literal arguments.
    #[arg(value_name = "KEY=VALUE ... COMMAND ARG", trailing_var_arg = true, allow_hyphen_values = true, num_args = 1..)]
    args: Vec<OsString>,
}

#[derive(Args)]
struct ScopeOptions {
    #[arg(long, value_enum, default_value = "project")]
    scope: Scope,
    #[arg(long)]
    project_dir: Option<PathBuf>,
}

#[derive(Clone, Copy, ValueEnum, PartialEq, Eq)]
enum Scope {
    Project,
    User,
}

#[derive(Clone, Copy, PartialEq, Eq, ValueEnum)]
enum OnLocked {
    Wait,
    Skip,
    Fail,
}

#[derive(Clone, Copy, ValueEnum)]
enum Jitter {
    Full,
    None,
}

#[derive(Clone, Copy, ValueEnum)]
enum HttpMethod {
    Get,
    Head,
}

#[derive(Clone, Copy)]
enum HttpProbeError {
    NotReady,
    AttemptTimeout,
    OverallTimeout,
    Cancelled,
    Terminal,
}

impl HttpProbeError {
    fn retryable(self) -> bool {
        matches!(self, Self::NotReady | Self::AttemptTimeout)
    }
}

pub(crate) enum Outcome {
    Child(ExitStatus),
    Code(i32),
}

pub(crate) fn execute(command: Command, raw: &[OsString]) -> Result<Outcome> {
    match command {
        Command::RateLimit(mut options) => {
            options.workload.restore_leading_separator(raw, false);
            rate_limit(options)
        }
        Command::Lock(mut options) => {
            options.workload.restore_leading_separator(raw, false);
            with_lock(options)
        }
        Command::Service(mut options) => {
            options
                .workload
                .restore_leading_separator(raw, options.service.is_some());
            with_service(options)
        }
        Command::Retry(mut options) => {
            options.workload.restore_leading_separator(raw, false);
            with_retry(options)
        }
        Command::Timeout(mut options) => {
            options.workload.restore_leading_separator(raw, false);
            with_timeout(options)
        }
    }
}

impl Workload {
    fn restore_leading_separator(&mut self, raw: &[OsString], service_delimiter: bool) {
        let Some(start) = raw.len().checked_sub(self.args.len()) else {
            return;
        };
        let Some(separator) = start.checked_sub(1) else {
            return;
        };
        if raw.get(separator).is_none_or(|value| value != "--") {
            return;
        }
        // `--service` consumes its own delimiter before the workload. Only a
        // second adjacent delimiter starts the literal workload itself.
        if service_delimiter
            && separator
                .checked_sub(1)
                .and_then(|index| raw.get(index))
                .is_none_or(|value| value != "--")
        {
            return;
        }
        self.args.insert(0, "--".into());
    }
}

#[cfg(test)]
mod workload_tests {
    use super::*;

    #[test]
    fn restores_only_the_workload_separator() {
        for wrapper in ["with-rate-limit", "with-lock", "with-retry", "with-timeout"] {
            let raw = vec![
                "clibox".into(),
                "run".into(),
                wrapper.into(),
                "--".into(),
                "tool=value".into(),
            ];
            let mut workload = Workload {
                kill_after: Duration::ZERO,
                args: vec!["tool=value".into()],
            };
            workload.restore_leading_separator(&raw, false);
            assert_eq!(
                workload.args,
                [OsString::from("--"), OsString::from("tool=value")]
            );
        }

        let mut service_workload = Workload {
            kill_after: Duration::ZERO,
            args: vec!["tool=value".into()],
        };
        let raw = vec![
            "clibox".into(),
            "run".into(),
            "with-service".into(),
            "http://127.0.0.1:3000/health".into(),
            "--service".into(),
            "server".into(),
            "--".into(),
            "--".into(),
            "tool=value".into(),
        ];
        service_workload.restore_leading_separator(&raw, true);
        assert_eq!(
            service_workload.args,
            [OsString::from("--"), OsString::from("tool=value")]
        );

        let mut service_workload = Workload {
            kill_after: Duration::ZERO,
            args: vec!["tool=value".into()],
        };
        let raw = vec![
            "clibox".into(),
            "run".into(),
            "with-service".into(),
            "http://127.0.0.1:3000/health".into(),
            "--service".into(),
            "server".into(),
            "--".into(),
            "tool=value".into(),
        ];
        service_workload.restore_leading_separator(&raw, true);
        assert_eq!(service_workload.args, [OsString::from("tool=value")]);
    }
}

#[cfg(all(test, windows))]
mod windows_state_tests {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::{LocalFree, ERROR_SUCCESS},
        Security::{
            Authorization::{GetSecurityInfo, SetSecurityInfo, SE_FILE_OBJECT},
            DACL_SECURITY_INFORMATION, UNPROTECTED_DACL_SECURITY_INFORMATION,
        },
    };

    use super::*;

    fn unprotect_dacl(file: &File) {
        let mut dacl = std::ptr::null_mut();
        let mut descriptor = std::ptr::null_mut();
        assert_eq!(
            unsafe {
                GetSecurityInfo(
                    file.as_raw_handle(),
                    SE_FILE_OBJECT,
                    DACL_SECURITY_INFORMATION,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    &mut dacl,
                    std::ptr::null_mut(),
                    &mut descriptor,
                )
            },
            ERROR_SUCCESS,
            "read test DACL"
        );
        assert_eq!(
            unsafe {
                SetSecurityInfo(
                    file.as_raw_handle(),
                    SE_FILE_OBJECT,
                    DACL_SECURITY_INFORMATION | UNPROTECTED_DACL_SECURITY_INFORMATION,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    dacl,
                    std::ptr::null(),
                )
            },
            ERROR_SUCCESS,
            "unprotect test DACL"
        );
        unsafe {
            LocalFree(descriptor);
        }
    }

    #[test]
    fn state_directory_and_files_retain_the_current_users_private_dacl() {
        let temporary = tempfile::tempdir().expect("temporary directory");
        let state = temporary.path().join("state");
        ensure_private_dir(&state).expect("private state directory");

        let directory = open_state_directory(&state).expect("state directory handle");
        ensure_windows_state_object(&directory, true).expect("state directory is not a reparse");
        ensure_windows_private_dacl(&directory).expect("state directory DACL");

        let lock = open_lock(&state.join("admission.lock")).expect("state lock");
        ensure_windows_private_dacl(&lock).expect("state lock DACL");

        let bucket_path = state.join("admission.json");
        write_bucket(
            &bucket_path,
            &Bucket {
                version: STATE_VERSION,
                limit: 1,
                period_ms: 1,
                burst: 1,
                tokens: 1.0,
                refill_utc_ms: 0,
            },
        )
        .expect("state bucket");
        let bucket = open_state_for_read(&bucket_path).expect("state bucket handle");
        ensure_windows_state_object(&bucket, false).expect("state bucket links");
        ensure_windows_private_dacl(&bucket).expect("state bucket DACL");
    }

    #[test]
    fn state_objects_reject_an_unprotected_dacl() {
        let temporary = tempfile::tempdir().expect("temporary directory");
        let state = temporary.path().join("state");
        ensure_private_dir(&state).expect("private state directory");
        let lock = open_lock(&state.join("admission.lock")).expect("state lock");

        unprotect_dacl(&lock);
        drop(lock);

        assert!(open_lock(&state.join("admission.lock")).is_err());
    }
}

#[cfg(all(test, target_os = "macos"))]
mod macos_state_tests {
    use super::*;

    #[test]
    fn newly_created_state_objects_have_empty_extended_acls() {
        let temporary = tempfile::tempdir().expect("temporary directory");
        let state = temporary.path().join("state");
        ensure_private_dir(&state).expect("private state directory");
        ensure_private_macos_acl(&state).expect("state directory ACL");

        let lock_path = state.join("admission.lock");
        let _lock = open_lock(&lock_path).expect("state lock");
        ensure_private_macos_acl(&lock_path).expect("state lock ACL");

        let bucket_path = state.join("admission.json");
        write_bucket(
            &bucket_path,
            &Bucket {
                version: STATE_VERSION,
                limit: 1,
                period_ms: 1,
                burst: 1,
                tokens: 1.0,
                refill_utc_ms: 0,
            },
        )
        .expect("state bucket");
        ensure_private_macos_acl(&bucket_path).expect("state bucket ACL");
    }
}

fn rate_limit(options: RateLimit) -> Result<Outcome> {
    let plan = environment::prepare(options.workload.args)?;
    let key = StateKey::new(Namespace::Rate, &options.name, &options.scope)?;
    let start = Instant::now();
    let deadline = admission_deadline(start, options.wait_timeout)?;
    let mut attempted = false;
    loop {
        check_cancelled()?;
        if admission_deadline_expired(deadline, attempted) {
            return Ok(Outcome::Code(124));
        }
        attempted = true;
        let lock = match acquire(&key.lock_path, deadline) {
            Ok(lock) => lock,
            Err(error) if error.code == Code::TerminationTimeout => return Ok(Outcome::Code(124)),
            Err(error) => return Err(error),
        };
        let now = unix_millis();
        let mut bucket = read_bucket(
            &key.data_path,
            options.limit,
            options.period,
            options.burst,
            now,
        )?;
        if bucket.tokens >= 1.0 {
            bucket.tokens -= 1.0;
            write_bucket(&key.data_path, &bucket)?;
            drop(lock);
            tracing::debug!(
                operation = "run-with-rate-limit",
                stage = "admitted",
                "run_admission"
            );
            return run_once(&plan, options.workload.kill_after, Limits::default(), None);
        }
        let wait = bucket.wait_duration();
        drop(lock);
        if deadline.is_some_and(|limit| Instant::now() >= limit) {
            return Ok(Outcome::Code(124));
        }
        tracing::debug!(
            operation = "run-with-rate-limit",
            stage = "waiting",
            "run_admission"
        );
        sleep_cancellable(clip_to_deadline(wait, deadline))?;
    }
}

fn with_lock(options: Lock) -> Result<Outcome> {
    if options.on_locked != OnLocked::Wait && options.wait_timeout.is_some() {
        return invalid("--wait-timeout is valid only with --on-locked wait.");
    }
    let plan = environment::prepare(options.workload.args)?;
    let key = StateKey::new(Namespace::Lock, &options.name, &options.scope)?;
    let deadline = admission_deadline(Instant::now(), options.wait_timeout)?;
    let lock = match options.on_locked {
        OnLocked::Wait => match acquire(&key.lock_path, deadline) {
            Ok(lock) => Some(lock),
            Err(error) if error.code == Code::TerminationTimeout => return Ok(Outcome::Code(124)),
            Err(error) => return Err(error),
        },
        OnLocked::Skip => match try_acquire(&key.lock_path)? {
            Some(lock) => Some(lock),
            None => {
                tracing::debug!(operation = "run-with-lock", stage = "skipped", "run_lock");
                return Ok(Outcome::Code(0));
            }
        },
        OnLocked::Fail => match try_acquire(&key.lock_path)? {
            Some(lock) => Some(lock),
            None => return Ok(Outcome::Code(75)),
        },
    };
    let _lock = lock.expect("a lock is present for every execution path");
    tracing::debug!(operation = "run-with-lock", stage = "acquired", "run_lock");
    run_once(&plan, options.workload.kill_after, Limits::default(), None)
}

fn with_service(options: Service) -> Result<Outcome> {
    let start = Instant::now();
    let workload = environment::prepare(options.workload.args.clone())?;
    let service = options
        .service
        .clone()
        .map(environment::prepare)
        .transpose()?;
    let ready_deadline = deadline(start, Some(options.ready_timeout))?;
    let client = match service_http_client(options.url.scheme() == "https", ready_deadline) {
        Ok(client) => client,
        Err(error) if error.code == Code::TerminationTimeout => return Ok(Outcome::Code(124)),
        Err(error) => return Err(error),
    };
    let mut pending_probe = None;

    // The bounded preflight deliberately happens before spawning either child.
    match check_http(&options, &client, ready_deadline, &mut pending_probe) {
        Ok(()) if service.is_some() => {
            return runtime_failure(
                "The endpoint is already ready; omit --service to use the existing server.",
            )
        }
        Ok(()) => {
            return run_once(
                &workload,
                options.workload.kill_after,
                Limits::default(),
                None,
            );
        }
        Err(code) if code.retryable() && service.is_some() => {
            if ready_deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                return Ok(Outcome::Code(124));
            }
        }
        Err(code) if code.retryable() => {
            return wait_for_external_service(
                &options,
                &client,
                &workload,
                ready_deadline,
                &mut pending_probe,
            );
        }
        Err(HttpProbeError::OverallTimeout) => return Ok(Outcome::Code(124)),
        Err(HttpProbeError::Cancelled) => return check_cancelled().and_then(|()| unreachable!()),
        Err(_) => return runtime_failure("HTTP readiness preflight failed; check the endpoint."),
    }

    // A retryable preflight is the first unsuccessful probe. Do not create
    // side effects before its configured cadence and readiness deadline allow
    // the managed service to start.
    sleep_cancellable(clip_to_deadline(options.interval, ready_deadline))?;
    if ready_deadline.is_some_and(|deadline| Instant::now() >= deadline) {
        return Ok(Outcome::Code(124));
    }
    // The final sleep poll can finish just before a cancellation arrives.
    // Recheck at the side-effect boundary before starting the managed service.
    check_cancelled()?;
    let service_plan = service.expect("service is checked above");
    let mut service_child = match spawn(&service_plan, OutputMode::Service, ready_deadline) {
        Ok(child) => child,
        Err(error) if error.code == Code::TerminationTimeout => return Ok(Outcome::Code(124)),
        Err(error) => return Err(error),
    };
    loop {
        if service_child.output_failed() {
            return cleanup_managed_service(
                &mut service_child,
                options.workload.kill_after,
                runtime_failure("Could not forward managed service output."),
            );
        }
        if runtime::cancelled() {
            return cleanup_managed_service(
                &mut service_child,
                options.workload.kill_after,
                check_cancelled().and_then(|()| unreachable!()),
            );
        }
        if completion_or_cleanup(&mut service_child, options.workload.kill_after)?.is_some() {
            return cleanup_managed_service(
                &mut service_child,
                options.workload.kill_after,
                runtime_failure("The managed service exited before it became ready."),
            );
        }
        match check_http(&options, &client, ready_deadline, &mut pending_probe) {
            Ok(()) => break,
            Err(code) if code.retryable() => {
                // The service can exit while one bounded HTTP attempt is in
                // progress. Prefer that owned-process failure to reporting a
                // coincident readiness deadline.
                if completion_or_cleanup(&mut service_child, options.workload.kill_after)?.is_some()
                {
                    return cleanup_managed_service(
                        &mut service_child,
                        options.workload.kill_after,
                        runtime_failure("The managed service exited before it became ready."),
                    );
                }
                if ready_deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                    return cleanup_managed_service(
                        &mut service_child,
                        options.workload.kill_after,
                        Ok(Outcome::Code(124)),
                    );
                }
                match wait_for_managed_service(
                    &mut service_child,
                    clip_to_deadline(options.interval, ready_deadline),
                ) {
                    Ok(ManagedServiceWait::Elapsed) => (),
                    Ok(ManagedServiceWait::Exited) => {
                        return cleanup_managed_service(
                            &mut service_child,
                            options.workload.kill_after,
                            runtime_failure("The managed service exited before it became ready."),
                        );
                    }
                    Err(error) => {
                        return cleanup_managed_service(
                            &mut service_child,
                            options.workload.kill_after,
                            Err(error),
                        );
                    }
                }
            }
            Err(HttpProbeError::OverallTimeout) => {
                if completion_or_cleanup(&mut service_child, options.workload.kill_after)?.is_some()
                {
                    return cleanup_managed_service(
                        &mut service_child,
                        options.workload.kill_after,
                        runtime_failure("The managed service exited before it became ready."),
                    );
                }
                return cleanup_managed_service(
                    &mut service_child,
                    options.workload.kill_after,
                    Ok(Outcome::Code(124)),
                );
            }
            Err(HttpProbeError::Cancelled) => {
                return cleanup_managed_service(
                    &mut service_child,
                    options.workload.kill_after,
                    check_cancelled().and_then(|()| unreachable!()),
                );
            }
            Err(_) => {
                return cleanup_managed_service(
                    &mut service_child,
                    options.workload.kill_after,
                    runtime_failure("HTTP readiness check failed; check the endpoint."),
                );
            }
        }
    }
    // The managed service can exit or lose its output consumer while the
    // successful readiness request is in flight. Recheck ownership before the
    // workload is allowed to create any side effects.
    if service_child.output_failed() {
        return cleanup_managed_service(
            &mut service_child,
            options.workload.kill_after,
            runtime_failure("Could not forward managed service output."),
        );
    }
    // A successful probe can be observed while the service completion worker
    // is still handing off an exit that happened during the request. Give that
    // worker one bounded poll before the workload can create side effects.
    if let Err(error) = sleep_cancellable(POLL) {
        return cleanup_managed_service(
            &mut service_child,
            options.workload.kill_after,
            Err(error),
        );
    }
    if completion_or_cleanup(&mut service_child, options.workload.kill_after)?.is_some() {
        return cleanup_managed_service(
            &mut service_child,
            options.workload.kill_after,
            runtime_failure("The managed service exited before the workload started."),
        );
    }
    let outcome = match run_once(
        &workload,
        options.workload.kill_after,
        Limits::default(),
        Some(&mut service_child),
    ) {
        Ok(outcome) => outcome,
        Err(error) => {
            return cleanup_managed_service(
                &mut service_child,
                options.workload.kill_after,
                Err(error),
            );
        }
    };
    if !cleanup_or_log(&mut service_child, options.workload.kill_after) {
        return runtime_failure("Managed service cleanup could not be confirmed.");
    }
    finish_managed_service_output(&mut service_child, None)?;
    Ok(outcome)
}

fn cleanup_managed_service(
    service: &mut OwnedChild,
    kill_after: Duration,
    outcome: Result<Outcome>,
) -> Result<Outcome> {
    if cleanup_or_log(service, kill_after) {
        let ignored_cancellation_generation =
            runtime::cancelled().then(runtime::cancellation_generation);
        finish_managed_service_output(service, ignored_cancellation_generation)?;
        outcome
    } else {
        runtime_failure("Managed service cleanup could not be confirmed.")
    }
}

fn finish_managed_service_output(
    service: &mut OwnedChild,
    ignored_cancellation_generation: Option<usize>,
) -> Result<()> {
    match service.join_output_within(&Limits::default(), ignored_cancellation_generation) {
        OutputJoin::Complete if service.output_failed() => {
            runtime_failure("Could not forward managed service output.")
        }
        OutputJoin::Complete => Ok(()),
        OutputJoin::Deadline => {
            runtime_failure("Could not finish forwarding managed service output.")
        }
        OutputJoin::Cancelled => Err(Failure::new(
            Code::Cancelled,
            "Execution was cancelled while forwarding managed service output.",
        )),
        OutputJoin::Failed => {
            runtime_failure("Could not finish forwarding managed service output.")
        }
    }
}

fn wait_for_external_service(
    options: &Service,
    client: &reqwest::blocking::Client,
    workload: &environment::Plan,
    ready_deadline: Option<Instant>,
    pending_probe: &mut Option<PendingHttpProbe>,
) -> Result<Outcome> {
    // The caller already performed a retryable preflight. Treat it as the
    // first unsuccessful probe so external endpoints never receive an
    // immediate duplicate request before their configured polling interval.
    sleep_cancellable(clip_to_deadline(options.interval, ready_deadline))?;
    loop {
        check_cancelled()?;
        match check_http(options, client, ready_deadline, pending_probe) {
            Ok(()) => {
                return run_once(
                    workload,
                    options.workload.kill_after,
                    Limits::default(),
                    None,
                );
            }
            Err(code) if code.retryable() => {
                if ready_deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                    return Ok(Outcome::Code(124));
                }
                sleep_cancellable(clip_to_deadline(options.interval, ready_deadline))?;
            }
            Err(HttpProbeError::OverallTimeout) => return Ok(Outcome::Code(124)),
            Err(HttpProbeError::Cancelled) => {
                return check_cancelled().and_then(|()| unreachable!());
            }
            Err(_) => {
                return runtime_failure("HTTP readiness check failed; check the endpoint.");
            }
        }
    }
}

fn with_retry(options: Retry) -> Result<Outcome> {
    if options.max_delay < options.delay {
        return invalid("--max-delay must be at least --delay.");
    }
    let started = Instant::now();
    let plan = environment::prepare(options.workload.args)?;
    let overall = deadline(started, options.timeout)?;
    let selected: std::collections::BTreeSet<_> = options.retry_exit_code.into_iter().collect();
    for attempt in 1..=options.max_attempts {
        if overall.is_some_and(|deadline| Instant::now() >= deadline) {
            return Ok(Outcome::Code(124));
        }
        tracing::debug!(
            operation = "run-with-retry",
            attempt,
            stage = "started",
            "run_attempt"
        );
        let outcome = run_once(
            &plan,
            options.workload.kill_after,
            Limits {
                overall,
                idle: None,
            },
            None,
        )?;
        let Outcome::Child(status) = outcome else {
            return Ok(outcome);
        };
        if status.success()
            || !retryable_status(&status, &selected)
            || attempt == options.max_attempts
        {
            return Ok(Outcome::Child(status));
        }
        let delay = retry_delay(
            options.delay,
            options.max_delay,
            options.backoff_factor,
            attempt,
            options.jitter,
        );
        tracing::debug!(
            operation = "run-with-retry",
            attempt,
            stage = "backoff",
            "run_attempt"
        );
        match sleep_cancellable(clip_to_deadline(delay, overall)) {
            Ok(()) if overall.is_some_and(|deadline| Instant::now() >= deadline) => {
                return Ok(Outcome::Code(124));
            }
            Ok(()) => (),
            Err(error) if error.code == Code::Cancelled => return Err(error),
            Err(error) => return Err(error),
        }
    }
    unreachable!("a positive retry count always returns from the loop")
}

fn with_timeout(options: Timeout) -> Result<Outcome> {
    let Some(overall_duration) = options.timeout.filter(|duration| !duration.is_zero()) else {
        if options
            .idle_timeout
            .is_none_or(|duration| duration.is_zero())
        {
            return invalid("Provide a positive --timeout or --idle-timeout.");
        }
        return timeout_with_idle(options);
    };
    let started = Instant::now();
    let plan = environment::prepare(options.workload.args)?;
    run_once(
        &plan,
        options.workload.kill_after,
        Limits {
            overall: Some(
                started
                    .checked_add(overall_duration)
                    .ok_or_else(|| Failure::new(Code::InvalidInput, "Duration is too large."))?,
            ),
            idle: options.idle_timeout.filter(|duration| !duration.is_zero()),
        },
        None,
    )
}

fn timeout_with_idle(options: Timeout) -> Result<Outcome> {
    let plan = environment::prepare(options.workload.args)?;
    run_once(
        &plan,
        options.workload.kill_after,
        Limits {
            overall: None,
            idle: options.idle_timeout.filter(|duration| !duration.is_zero()),
        },
        None,
    )
}

fn check_http(
    options: &Service,
    client: &reqwest::blocking::Client,
    deadline: Option<Instant>,
    pending: &mut Option<PendingHttpProbe>,
) -> std::result::Result<(), HttpProbeError> {
    if let Some(result) = queued_probe_result(pending, deadline) {
        return result;
    }
    if pending.is_none() {
        let started = Instant::now();
        let remaining = deadline.map(|limit| limit.saturating_duration_since(started));
        if remaining == Some(Duration::ZERO) {
            return Err(HttpProbeError::OverallTimeout);
        }
        let budget = remaining
            .map(|remaining| remaining.min(options.attempt_timeout))
            .unwrap_or(options.attempt_timeout);
        let attempt_deadline = started
            .checked_add(budget)
            .ok_or(HttpProbeError::Terminal)?;
        let method = match options.method {
            HttpMethod::Get => reqwest::Method::GET,
            HttpMethod::Head => reqwest::Method::HEAD,
        };
        *pending = Some(start_http_probe(
            client.clone(),
            options.url.clone(),
            method,
            options.status,
            budget,
            attempt_deadline,
        )?);
    }
    loop {
        if runtime::cancelled() {
            return Err(HttpProbeError::Cancelled);
        }
        if let Some(result) = queued_probe_result(pending, deadline) {
            return result;
        }
        let attempt_deadline = pending
            .as_ref()
            .expect("a pending readiness probe is retained until it finishes")
            .attempt_deadline;
        let until_attempt = attempt_deadline.saturating_duration_since(Instant::now());
        if until_attempt.is_zero() {
            // Retain the worker: some native trust/connect paths cannot be
            // interrupted by the request timeout. Starting another probe
            // before it reports would violate the non-overlap contract.
            return Err(HttpProbeError::AttemptTimeout);
        }
        let until_overall = deadline
            .map(|limit| limit.saturating_duration_since(Instant::now()))
            .unwrap_or(until_attempt);
        if until_overall.is_zero() {
            return Err(HttpProbeError::OverallTimeout);
        }
        thread::sleep(POLL.min(until_attempt).min(until_overall));
    }
}

struct PendingHttpProbe {
    receiver: mpsc::Receiver<HttpProbe>,
    attempt_deadline: Instant,
}

fn start_http_probe(
    client: reqwest::blocking::Client,
    url: reqwest::Url,
    method: reqwest::Method,
    expected_status: Option<u16>,
    budget: Duration,
    attempt_deadline: Instant,
) -> std::result::Result<PendingHttpProbe, HttpProbeError> {
    let (sender, receiver) = mpsc::sync_channel(1);
    thread::Builder::new()
        .name("clibox-readiness-probe".into())
        .spawn(move || {
            let result = http_attempt(client, url, method, expected_status, budget);
            let _ = sender.send(HttpProbe {
                result,
                observed_at: Instant::now(),
            });
        })
        .map_err(|error| {
            tracing::debug!(
                operation = "run-with-service",
                error_kind = ?error.kind(),
                stage = "readiness_worker_spawn_failed",
                "run_readiness"
            );
            HttpProbeError::Terminal
        })?;
    Ok(PendingHttpProbe {
        receiver,
        attempt_deadline,
    })
}

fn queued_probe_result(
    pending: &mut Option<PendingHttpProbe>,
    overall_deadline: Option<Instant>,
) -> Option<std::result::Result<(), HttpProbeError>> {
    let probe = pending.as_ref()?;
    let attempt_deadline = probe.attempt_deadline;
    match probe.receiver.try_recv() {
        Ok(probe) => {
            *pending = None;
            Some(bounded_probe_result(
                probe,
                attempt_deadline,
                overall_deadline,
            ))
        }
        Err(mpsc::TryRecvError::Empty) => None,
        Err(mpsc::TryRecvError::Disconnected) => {
            *pending = None;
            Some(Err(HttpProbeError::Terminal))
        }
    }
}

struct HttpProbe {
    result: std::result::Result<(), HttpProbeError>,
    observed_at: Instant,
}

fn bounded_probe_result(
    probe: HttpProbe,
    attempt_deadline: Instant,
    overall_deadline: Option<Instant>,
) -> std::result::Result<(), HttpProbeError> {
    if overall_deadline.is_some_and(|deadline| probe.observed_at >= deadline) {
        return Err(HttpProbeError::OverallTimeout);
    }
    if probe.observed_at >= attempt_deadline {
        return Err(HttpProbeError::AttemptTimeout);
    }
    probe.result
}

fn http_attempt(
    client: reqwest::blocking::Client,
    url: reqwest::Url,
    method: reqwest::Method,
    expected_status: Option<u16>,
    budget: Duration,
) -> std::result::Result<(), HttpProbeError> {
    let response = client
        .request(method, url)
        .timeout(budget)
        .send()
        .map_err(|error| {
            if error.is_timeout() {
                HttpProbeError::AttemptTimeout
            } else if error.is_connect() && !has_terminal_connect_failure(&error) {
                HttpProbeError::NotReady
            } else {
                HttpProbeError::Terminal
            }
        })?;
    let observed = response.status().as_u16();
    if expected_status
        .map(|expected| expected == observed)
        .unwrap_or((200..300).contains(&observed))
    {
        Ok(())
    } else {
        Err(HttpProbeError::NotReady)
    }
}

fn has_terminal_connect_failure(error: &reqwest::Error) -> bool {
    contains_tls_failure(error)
        || contains_permission_denied(error)
        || contains_local_resource_failure(error)
}

fn contains_tls_failure(error: &(dyn std::error::Error + 'static)) -> bool {
    if error.downcast_ref::<rustls::Error>().is_some() {
        return true;
    }
    if let Some(io_error) = error.downcast_ref::<std::io::Error>() {
        if let Some(source) = io_error.get_ref() {
            if contains_tls_failure(source) {
                return true;
            }
        }
    }
    error.source().is_some_and(contains_tls_failure)
}

fn contains_permission_denied(error: &(dyn std::error::Error + 'static)) -> bool {
    if let Some(io_error) = error.downcast_ref::<std::io::Error>() {
        if io_error.kind() == io::ErrorKind::PermissionDenied {
            return true;
        }
        if let Some(source) = io_error.get_ref() {
            if contains_permission_denied(source) {
                return true;
            }
        }
    }
    error.source().is_some_and(contains_permission_denied)
}

fn contains_local_resource_failure(error: &(dyn std::error::Error + 'static)) -> bool {
    if let Some(io_error) = error.downcast_ref::<std::io::Error>() {
        if io_error.raw_os_error().is_some_and(is_local_resource_error) {
            return true;
        }
        if let Some(source) = io_error.get_ref() {
            if contains_local_resource_failure(source) {
                return true;
            }
        }
    }
    error.source().is_some_and(contains_local_resource_failure)
}

fn is_local_resource_error(error: i32) -> bool {
    #[cfg(unix)]
    if matches!(
        error,
        libc::EMFILE | libc::ENFILE | libc::ENOBUFS | libc::ENOMEM
    ) {
        return true;
    }
    #[cfg(windows)]
    {
        use windows_sys::Win32::{
            Foundation::{ERROR_NOT_ENOUGH_MEMORY, ERROR_TOO_MANY_OPEN_FILES},
            Networking::WinSock::{WSAEMFILE, WSAENOBUFS},
        };

        if error == ERROR_NOT_ENOUGH_MEMORY as i32
            || error == ERROR_TOO_MANY_OPEN_FILES as i32
            || error == WSAEMFILE
            || error == WSAENOBUFS
        {
            return true;
        }
    }
    false
}

fn service_http_client(
    https: bool,
    readiness_deadline: Option<Instant>,
) -> Result<reqwest::blocking::Client> {
    let tls = if https {
        native_service_tls(readiness_deadline)?
    } else {
        tls_with_roots(rustls::RootCertStore::empty())?
    };
    reqwest::blocking::Client::builder()
        .no_proxy()
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .referer(false)
        .pool_max_idle_per_host(0)
        .use_preconfigured_tls(tls)
        .build()
        .map_err(|_| {
            Failure::new(
                Code::IoFailed,
                "HTTP readiness client initialization failed.",
            )
        })
}

fn native_service_tls(readiness_deadline: Option<Instant>) -> Result<rustls::ClientConfig> {
    wait_for_readiness_initialization(readiness_deadline, native_service_tls_blocking)?
}

fn native_service_tls_blocking() -> Result<rustls::ClientConfig> {
    // rustls-native-certs currently honors these OpenSSL-style overrides on
    // supported platforms. Service probes promise OS trust only, so scope the
    // workaround to root loading. An unfinished worker never permits a child
    // spawn, and process exit ends it after cancellation or readiness timeout.
    // Remove this workaround once the loader offers an explicit native-only API.
    let _overrides = NativeRootOverrides::without_custom_ca();
    let loaded = rustls_native_certs::load_native_certs();
    let mut roots = rustls::RootCertStore::empty();
    let (usable_roots, ignored_certificates) = roots.add_parsable_certificates(loaded.certs);
    tracing::debug!(
        operation = "run-with-service",
        usable_roots,
        ignored_certificates,
        loader_errors = loaded.errors.len(),
        "native_trust_loaded"
    );
    if roots.is_empty() {
        return runtime_failure("No usable native trust roots are available for HTTP readiness.");
    }
    tls_with_roots(roots)
}

fn wait_for_readiness_initialization<T: Send + 'static>(
    readiness_deadline: Option<Instant>,
    initialize: impl FnOnce() -> T + Send + 'static,
) -> Result<T> {
    let (sender, receiver) = mpsc::sync_channel(1);
    thread::Builder::new()
        .name("clibox-native-trust".into())
        .spawn(move || {
            let _ = sender.send(initialize());
        })
        .map_err(|error| {
            tracing::debug!(
                operation = "run-with-service",
                error_kind = ?error.kind(),
                stage = "native_trust_worker_spawn_failed",
                "run_readiness"
            );
            Failure::new(
                Code::IoFailed,
                "HTTP readiness trust initialization failed.",
            )
        })?;
    loop {
        check_cancelled()?;
        match receiver.try_recv() {
            Ok(initialized) => return Ok(initialized),
            Err(mpsc::TryRecvError::Disconnected) => {
                return runtime_failure("HTTP readiness trust initialization failed.");
            }
            Err(mpsc::TryRecvError::Empty) => (),
        }
        let wait = readiness_deadline
            .map(|deadline| deadline.saturating_duration_since(Instant::now()))
            .unwrap_or(POLL);
        if wait.is_zero() {
            return Err(Failure::new(
                Code::TerminationTimeout,
                "HTTP readiness trust initialization exceeded the readiness timeout.",
            ));
        }
        match receiver.recv_timeout(POLL.min(wait)) {
            Ok(initialized) => return Ok(initialized),
            Err(mpsc::RecvTimeoutError::Timeout) => (),
            Err(mpsc::RecvTimeoutError::Disconnected) => {
                return runtime_failure("HTTP readiness trust initialization failed.");
            }
        }
    }
}

fn tls_with_roots(roots: rustls::RootCertStore) -> Result<rustls::ClientConfig> {
    let mut tls = rustls::ClientConfig::builder_with_provider(Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .map_err(|_| Failure::new(Code::IoFailed, "HTTP readiness TLS initialization failed."))?
    .with_root_certificates(roots)
    .with_no_client_auth();
    tls.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];
    Ok(tls)
}

struct NativeRootOverrides {
    certificate_file: Option<OsString>,
    certificate_directory: Option<OsString>,
}

impl NativeRootOverrides {
    fn without_custom_ca() -> Self {
        let certificate_file = env::var_os("SSL_CERT_FILE");
        let certificate_directory = env::var_os("SSL_CERT_DIR");
        std::env::remove_var("SSL_CERT_FILE");
        std::env::remove_var("SSL_CERT_DIR");
        Self {
            certificate_file,
            certificate_directory,
        }
    }
}

impl Drop for NativeRootOverrides {
    fn drop(&mut self) {
        match self.certificate_file.take() {
            Some(value) => std::env::set_var("SSL_CERT_FILE", value),
            None => std::env::remove_var("SSL_CERT_FILE"),
        }
        match self.certificate_directory.take() {
            Some(value) => std::env::set_var("SSL_CERT_DIR", value),
            None => std::env::remove_var("SSL_CERT_DIR"),
        }
    }
}

#[derive(Default)]
struct Limits {
    overall: Option<Instant>,
    idle: Option<Duration>,
}

fn limits_expired_at(limits: &Limits, last_activity: Instant, observed_at: Instant) -> bool {
    limits
        .overall
        .is_some_and(|deadline| observed_at >= deadline)
        || limits
            .idle
            .is_some_and(|idle| observed_at.saturating_duration_since(last_activity) >= idle)
}

fn limits_expired_before_activity_refresh(
    limits: &Limits,
    last_activity: Instant,
    latest_activity: Instant,
    observed_at: Instant,
) -> bool {
    if limits
        .overall
        .is_some_and(|deadline| observed_at >= deadline)
    {
        return true;
    }
    let Some(idle_deadline) = limits.idle.and_then(|idle| last_activity.checked_add(idle)) else {
        return false;
    };
    if observed_at < idle_deadline {
        return false;
    }
    // A read that completed before the previous idle deadline extends the
    // limit. A later read cannot retroactively conceal that deadline.
    !(latest_activity > last_activity && latest_activity < idle_deadline)
}

fn supervision_poll_interval(limits: &Limits, last_activity: Instant, now: Instant) -> Duration {
    let idle = limits.idle.and_then(|idle| last_activity.checked_add(idle));
    limits
        .overall
        .into_iter()
        .chain(idle)
        .min()
        .map(|deadline| POLL.min(deadline.saturating_duration_since(now)))
        .unwrap_or(POLL)
}

fn run_once(
    plan: &environment::Plan,
    kill_after: Duration,
    limits: Limits,
    mut monitored_service: Option<&mut OwnedChild>,
) -> Result<Outcome> {
    if limits
        .overall
        .is_some_and(|deadline| Instant::now() >= deadline)
    {
        return Ok(Outcome::Code(124));
    }
    // Admission can publish rate-limit state or release a coordination lock
    // immediately before this boundary. Recheck cancellation before the
    // workload gains an opportunity to perform side effects.
    check_cancelled()?;
    let output_mode = if limits.idle.is_some() {
        OutputMode::WorkloadPiped
    } else {
        OutputMode::WorkloadInherited
    };
    let mut child = match spawn(plan, output_mode, limits.overall) {
        Ok(child) => child,
        Err(error) if error.code == Code::TerminationTimeout => return Ok(Outcome::Code(124)),
        Err(error) => return Err(error),
    };
    let mut last_activity = child
        .activity
        .as_ref()
        .map(Activity::last_observed_at)
        .unwrap_or_else(Instant::now);
    loop {
        let observed_at = Instant::now();
        let latest_activity = child
            .activity
            .as_ref()
            .map(Activity::last_observed_at)
            .unwrap_or(last_activity);
        if limits_expired_before_activity_refresh(
            &limits,
            last_activity,
            latest_activity,
            observed_at,
        ) {
            tracing::debug!(operation = "run", stage = "timeout", "run_cleanup");
            if cleanup_or_log(&mut child, kill_after) {
                return finish_timed_out_workload_output(&mut child);
            }
            return Ok(Outcome::Code(124));
        }
        last_activity = latest_activity;
        if child.output_failed() {
            cleanup_run_once_children(&mut child, &mut monitored_service, kill_after);
            return runtime_failure("Could not forward workload output.");
        }
        if let Some(service) = monitored_service.as_deref_mut() {
            if service.output_failed() {
                cleanup_after_managed_service_failure(&mut child, service, kill_after);
                return runtime_failure("Could not forward managed service output.");
            }
        }
        if runtime::cancelled() {
            if let Some(service) = monitored_service.as_deref_mut() {
                // Cancellation invalidates both owned process trees. Request
                // service termination before waiting for a stubborn workload's
                // grace period so it cannot continue serving side effects.
                request_termination_or_log(service);
            }
            if cleanup_or_log(&mut child, kill_after) {
                finish_cancelled_workload_output(&mut child)?;
            }
            return Err(Failure::new(
                Code::Cancelled,
                "Execution was cancelled; owned children were asked to stop.",
            ));
        }
        let service_completion = match monitored_service.as_deref_mut() {
            Some(service) => match service.completion() {
                Ok(completion) => completion,
                Err(error) => {
                    cleanup_after_managed_service_failure(&mut child, service, kill_after);
                    return Err(error);
                }
            },
            None => None,
        };
        let workload_completion = match child.completion() {
            Ok(completion) => completion,
            Err(error) => {
                cleanup_run_once_children(&mut child, &mut monitored_service, kill_after);
                return Err(error);
            }
        };
        if service_completion.is_some() {
            let service = monitored_service.expect("a completed service is monitored");
            cleanup_after_managed_service_failure(&mut child, service, kill_after);
            return runtime_failure("The managed service exited before the workload completed.");
        }
        if let Some(completion) = workload_completion {
            // A pipe reader can record its successful final read while the
            // completion worker is being observed. A read after the prior
            // idle deadline cannot retroactively extend that expired limit.
            let latest_activity = child
                .activity
                .as_ref()
                .map(Activity::last_observed_at)
                .unwrap_or(last_activity);
            if limits_expired_before_activity_refresh(
                &limits,
                last_activity,
                latest_activity,
                completion.observed_at,
            ) {
                tracing::debug!(operation = "run", stage = "timeout", "run_cleanup");
                if cleanup_or_log(&mut child, kill_after) {
                    return finish_timed_out_workload_output(&mut child);
                }
                return Ok(Outcome::Code(124));
            }
            last_activity = latest_activity;
            if child.output_failed() {
                cleanup_run_once_children(&mut child, &mut monitored_service, kill_after);
                return runtime_failure("Could not forward workload output.");
            }
            if let Some(service) = monitored_service.as_deref_mut() {
                // The service and workload completion workers can be
                // scheduled independently. If the service result has not
                // arrived yet, confirm that its owned tree still exists
                // before accepting the workload result.
                let service_exited = match service.completion() {
                    // Dedicated waiters do not establish a reliable exit
                    // order when both children have already completed. A
                    // managed service must remain alive until the workload
                    // completion is observed, so treat that ambiguity as a
                    // service failure instead of reporting success.
                    Ok(Some(_)) => true,
                    Ok(None) => match service.exit_before_workload_completion() {
                        Ok(exited) => exited,
                        Err(error) => {
                            cleanup_after_managed_service_failure(&mut child, service, kill_after);
                            return Err(error);
                        }
                    },
                    Err(error) => {
                        cleanup_after_managed_service_failure(&mut child, service, kill_after);
                        return Err(error);
                    }
                };
                if service_exited {
                    cleanup_after_managed_service_failure(&mut child, service, kill_after);
                    return runtime_failure(
                        "The managed service exited before the workload completed.",
                    );
                }
            }
            let status = completion.status;
            // Reaping the direct child does not end ownership of its process
            // group or Job Object. A background descendant can otherwise
            // retain an inherited pipe or outlive the wrapper after its parent
            // exits, so confirm bounded cleanup before returning this status.
            let descendants_running = match child.tree_running() {
                Ok(running) => running,
                Err(error) => {
                    cleanup_run_once_children(&mut child, &mut monitored_service, kill_after);
                    return Err(error);
                }
            };
            if descendants_running && !cleanup_or_log(&mut child, kill_after) {
                // Never join inherited pipes after unconfirmed cleanup: a
                // surviving descendant could hold one forever. Cleanup has
                // already exhausted its bounded termination attempts, so a
                // retryable direct-child status must not start overlapping
                // work without an owned handle for the surviving descendant.
                return runtime_failure("Owned workload cleanup could not be confirmed.");
            }
            if descendants_running && limits_expired_at(&limits, last_activity, Instant::now()) {
                return finish_timed_out_workload_output(&mut child);
            }
            return finish_workload_output(&mut child, &limits, status);
        }
        thread::sleep(supervision_poll_interval(
            &limits,
            last_activity,
            Instant::now(),
        ));
    }
}

fn finish_workload_output(
    child: &mut OwnedChild,
    limits: &Limits,
    status: ExitStatus,
) -> Result<Outcome> {
    match child.join_output_within(limits, None) {
        OutputJoin::Complete if child.output_failed() => {
            runtime_failure("Could not forward workload output.")
        }
        OutputJoin::Complete => Ok(Outcome::Child(status)),
        OutputJoin::Deadline => Ok(Outcome::Code(124)),
        OutputJoin::Cancelled => Err(Failure::new(
            Code::Cancelled,
            "Execution was cancelled after the workload completed.",
        )),
        OutputJoin::Failed => runtime_failure("Could not finish forwarding workload output."),
    }
}

fn finish_timed_out_workload_output(child: &mut OwnedChild) -> Result<Outcome> {
    match child.join_output_within(&Limits::default(), None) {
        OutputJoin::Complete if child.output_failed() => {
            runtime_failure("Could not forward workload output.")
        }
        OutputJoin::Complete => Ok(Outcome::Code(124)),
        OutputJoin::Deadline | OutputJoin::Failed => {
            runtime_failure("Could not finish forwarding workload output.")
        }
        OutputJoin::Cancelled => Err(Failure::new(
            Code::Cancelled,
            "Execution was cancelled while forwarding timed-out workload output.",
        )),
    }
}

fn finish_cancelled_workload_output(child: &mut OwnedChild) -> Result<()> {
    let cancellation_generation = runtime::cancellation_generation();
    match child.join_output_within(&Limits::default(), Some(cancellation_generation)) {
        OutputJoin::Complete if child.output_failed() => {
            runtime_failure("Could not forward workload output.")
        }
        OutputJoin::Complete | OutputJoin::Cancelled => Ok(()),
        OutputJoin::Deadline | OutputJoin::Failed => {
            runtime_failure("Could not finish forwarding workload output.")
        }
    }
}

fn cleanup_or_log(child: &mut OwnedChild, kill_after: Duration) -> bool {
    let initial_cancellation = runtime::cancelled().then(runtime::cancellation_generation);
    match child.cleanup(kill_after, initial_cancellation) {
        Ok(()) => true,
        Err(error) => {
            error.report("run");
            false
        }
    }
}

fn completion_or_cleanup(
    child: &mut OwnedChild,
    kill_after: Duration,
) -> Result<Option<Completion>> {
    match child.completion() {
        Ok(completion) => Ok(completion),
        Err(error) => {
            let _ = cleanup_or_log(child, kill_after);
            Err(error)
        }
    }
}

fn cleanup_run_once_children(
    workload: &mut OwnedChild,
    monitored_service: &mut Option<&mut OwnedChild>,
    kill_after: Duration,
) {
    let _ = cleanup_or_log(workload, kill_after);
    if let Some(service) = monitored_service.as_deref_mut() {
        let _ = cleanup_or_log(service, kill_after);
    }
}

fn cleanup_after_managed_service_failure(
    workload: &mut OwnedChild,
    service: &mut OwnedChild,
    kill_after: Duration,
) {
    // A failed service invalidates a running workload immediately. Request
    // workload termination before any service grace/confirmation wait so it
    // cannot keep performing side effects while service cleanup is bounded.
    request_termination_or_log(workload);
    let _ = cleanup_or_log(service, kill_after);
    let _ = cleanup_or_log(workload, kill_after);
}

fn request_termination_or_log(child: &mut OwnedChild) {
    if let Err(error) = child.request_graceful_termination() {
        error.report("run");
    }
}

enum OutputMode {
    WorkloadInherited,
    WorkloadPiped,
    Service,
}

struct OwnedChild {
    pid: u32,
    #[cfg(unix)]
    unix_ownership: UnixOwnership,
    #[cfg(unix)]
    reaped: bool,
    #[cfg(unix)]
    foreground_terminal: Option<Arc<ForegroundTerminal>>,
    #[cfg(windows)]
    job: Job,
    completions: mpsc::Receiver<Result<Completion>>,
    completion: Option<Completion>,
    completion_observation_failed: bool,
    activity: Option<Activity>,
    output_failure: Option<Arc<AtomicBool>>,
    output_threads: Vec<thread::JoinHandle<()>>,
}

#[derive(Clone)]
struct Completion {
    status: ExitStatus,
    observed_at: Instant,
}

#[cfg(unix)]
#[derive(Clone, Copy)]
enum UnixOwnership {
    ProcessGroup,
    DirectChild,
}

#[cfg(unix)]
struct ForegroundTerminal {
    descriptor: libc::c_int,
    parent_group: libc::pid_t,
    child_group: libc::pid_t,
}

#[cfg(unix)]
#[derive(Clone, Copy)]
struct ForegroundTerminalParent {
    descriptor: libc::c_int,
    group: libc::pid_t,
}

fn spawn(
    plan: &environment::Plan,
    mode: OutputMode,
    deadline: Option<Instant>,
) -> Result<OwnedChild> {
    check_spawn_boundary(deadline)?;
    let mut command = command_for_spawn(plan, deadline)?;
    // Windows resolves PATH/PATHEXT while building the command. The lookup is
    // supervised so cancellation and wrapper deadlines do not wait for a
    // stalled filesystem, and this boundary prevents a late result from
    // starting a child after the worker is no longer owned.
    check_spawn_boundary(deadline)?;
    #[cfg(unix)]
    let unix_ownership = unix_ownership();
    #[cfg(unix)]
    let foreground_parent_group = foreground_parent_group(unix_ownership, &mode)?;
    #[cfg(unix)]
    {
        // Descendant clibox wrappers must keep their workloads in this
        // wrapper's group. The outer wrapper can then terminate descendants
        // even after the inner wrapper exits. Parent executable and process
        // group state provide that evidence without changing workload values.
        configure_process_group(&mut command, unix_ownership);
    }
    #[cfg(windows)]
    configure_process_group(&mut command);
    let pipe = !matches!(mode, OutputMode::WorkloadInherited);
    command.stdin(if matches!(mode, OutputMode::Service) {
        Stdio::null()
    } else {
        Stdio::inherit()
    });
    command.stdout(if pipe {
        Stdio::piped()
    } else {
        Stdio::inherit()
    });
    command.stderr(if pipe {
        Stdio::piped()
    } else {
        Stdio::inherit()
    });
    let mut child = command.spawn().map_err(|_| {
        Failure::new(
            Code::SpawnFailed,
            "Could not start the child command; check its executable, PATH, permissions and \
             supported argument encoding.",
        )
    })?;
    tracing::debug!(
        operation = "run",
        pid = child.id(),
        stage = "started",
        "run_child"
    );
    let pid = child.id();
    #[cfg(unix)]
    let foreground_terminal = match foreground_parent_group {
        Some(parent) => match ForegroundTerminal::transfer(parent, child.id()) {
            Ok(terminal) => Some(Arc::new(terminal)),
            Err(error) => {
                let _ = signal_process_group(child.id(), true);
                let _ = child.wait();
                return Err(error);
            }
        },
        None => None,
    };
    #[cfg(windows)]
    let job = match Job::attach(&child) {
        Ok(job) => job,
        Err(error) => {
            // The child has not executed application code yet. It must never
            // escape a failed ownership setup.
            let _ = child.kill();
            let _ = child.wait();
            return Err(error);
        }
    };
    #[cfg(windows)]
    if let Err(error) = check_spawn_boundary(deadline) {
        let _ = job.signal(child.id(), true);
        let _ = child.wait();
        return Err(error);
    }
    #[cfg(windows)]
    if let Err(error) = resume_suspended_process(&child) {
        let _ = job.signal(child.id(), true);
        let _ = child.wait();
        return Err(error);
    }
    let activity = matches!(mode, OutputMode::WorkloadPiped).then(Activity::new);
    let output_failure = pipe.then(|| Arc::new(AtomicBool::new(false)));
    let stderr_for_both = matches!(mode, OutputMode::Service);
    let mut output_threads = Vec::new();
    if let Some(stdout) = child.stdout.take() {
        let output = forward(
            stdout,
            stderr_for_both,
            activity.clone(),
            output_failure.clone(),
        );
        let output = match output {
            Ok(output) => output,
            Err(error) => {
                return Err(supervision_spawn_failure(
                    &mut child,
                    pid,
                    #[cfg(unix)]
                    unix_ownership,
                    #[cfg(unix)]
                    foreground_terminal.as_deref(),
                    #[cfg(windows)]
                    &job,
                    &error,
                    "output_spawn_failed",
                ));
            }
        };
        output_threads.push(output);
    }
    if let Some(stderr) = child.stderr.take() {
        let output = forward(stderr, true, activity.clone(), output_failure.clone());
        let output = match output {
            Ok(output) => output,
            Err(error) => {
                return Err(supervision_spawn_failure(
                    &mut child,
                    pid,
                    #[cfg(unix)]
                    unix_ownership,
                    #[cfg(unix)]
                    foreground_terminal.as_deref(),
                    #[cfg(windows)]
                    &job,
                    &error,
                    "output_spawn_failed",
                ));
            }
        };
        output_threads.push(output);
    }
    let (completion_tx, completions) = mpsc::sync_channel(1);
    #[cfg(unix)]
    let completion_terminal = foreground_terminal.clone();
    // Keep ownership available until the completion worker is known to have
    // started. `thread::spawn` panics when the operating system refuses a
    // new thread, which would leave an already-started child unsupervised.
    let child = Arc::new(Mutex::new(Some(child)));
    let completion_child = Arc::clone(&child);
    let completion_thread = thread::Builder::new()
        .name("clibox-child-completion".into())
        .spawn(move || {
            let child = completion_child
                .lock()
                .unwrap_or_else(|poisoned| poisoned.into_inner())
                .take();
            let completion = match child {
                #[cfg(unix)]
                Some(child) => wait_for_completion(child, completion_terminal),
                #[cfg(not(unix))]
                Some(mut child) => child
                    .wait()
                    .map(|status| Completion {
                        status,
                        observed_at: Instant::now(),
                    })
                    .map_err(|error| Failure::io(&error)),
                None => runtime_failure("Could not transfer the owned child to its supervisor."),
            };
            let _ = completion_tx.send(completion);
        });
    if let Err(error) = completion_thread {
        let mut child = child
            .lock()
            .unwrap_or_else(|poisoned| poisoned.into_inner())
            .take()
            .ok_or_else(|| {
                Failure::new(
                    Code::IoFailed,
                    "Could not recover the child after its supervisor failed to start.",
                )
            })?;
        return Err(supervision_spawn_failure(
            &mut child,
            pid,
            #[cfg(unix)]
            unix_ownership,
            #[cfg(unix)]
            foreground_terminal.as_deref(),
            #[cfg(windows)]
            &job,
            &error,
            "completion_spawn_failed",
        ));
    }
    Ok(OwnedChild {
        pid,
        #[cfg(unix)]
        unix_ownership,
        #[cfg(unix)]
        reaped: false,
        #[cfg(unix)]
        foreground_terminal,
        #[cfg(windows)]
        job,
        completions,
        completion: None,
        completion_observation_failed: false,
        activity,
        output_failure,
        output_threads,
    })
}

fn command_for_spawn(
    plan: &environment::Plan,
    deadline: Option<Instant>,
) -> Result<ProcessCommand> {
    #[cfg(windows)]
    {
        let plan = plan.clone();
        return runtime::interruptible_until(deadline, move || environment::command(&plan));
    }
    #[cfg(not(windows))]
    {
        let _ = deadline;
        environment::command(plan)
    }
}

fn check_spawn_boundary(deadline: Option<Instant>) -> Result<()> {
    if deadline.is_some_and(|deadline| Instant::now() >= deadline) {
        return Err(Failure::new(
            Code::TerminationTimeout,
            "Execution time limit expired before the child process started.",
        ));
    }
    check_cancelled()
}

fn supervision_spawn_failure(
    child: &mut Child,
    pid: u32,
    #[cfg(unix)] unix_ownership: UnixOwnership,
    #[cfg(unix)] foreground_terminal: Option<&ForegroundTerminal>,
    #[cfg(windows)] job: &Job,
    error: &io::Error,
    stage: &'static str,
) -> Failure {
    let failure = Failure::io(error);
    tracing::error!(operation = "run", pid, stage, code = ?failure.code, "run_child");
    recover_unclaimed_child(
        child,
        pid,
        #[cfg(unix)]
        unix_ownership,
        #[cfg(unix)]
        foreground_terminal,
        #[cfg(windows)]
        job,
    );
    failure
}

fn recover_unclaimed_child(
    child: &mut Child,
    pid: u32,
    #[cfg(unix)] unix_ownership: UnixOwnership,
    #[cfg(unix)] foreground_terminal: Option<&ForegroundTerminal>,
    #[cfg(windows)] job: &Job,
) {
    #[cfg(unix)]
    {
        let cleanup = match unix_ownership {
            UnixOwnership::ProcessGroup => signal_process_group(pid, true),
            UnixOwnership::DirectChild => signal_process(pid, true),
        };
        if let Err(cleanup_error) = cleanup {
            cleanup_error.report("run");
            let _ = child.kill();
        }
        let _ = child.wait();
        if let Some(terminal) = foreground_terminal {
            terminal.restore();
        }
    }
    #[cfg(windows)]
    {
        if let Err(cleanup_error) = job.signal(pid, true) {
            cleanup_error.report("run");
            let _ = child.kill();
        }
        let _ = child.wait();
    }
}

fn forward(
    mut reader: impl Read + Send + 'static,
    stderr: bool,
    activity: Option<Activity>,
    output_failure: Option<Arc<AtomicBool>>,
) -> io::Result<thread::JoinHandle<()>> {
    thread::Builder::new()
        .name("clibox-output-forward".into())
        .spawn(move || {
            let mut buffer = [0u8; 8192];
            loop {
                let count = match reader.read(&mut buffer) {
                    Ok(0) => return,
                    Err(_) => {
                        tracing::debug!(
                            operation = "run",
                            stage = "forward-read-failed",
                            "run_output"
                        );
                        if let Some(output_failure) = &output_failure {
                            output_failure.store(true, Ordering::Release);
                        }
                        return;
                    }
                    Ok(count) => count,
                };
                // A successful read is workload activity even when a slow consumer
                // blocks forwarding these bytes for longer than the idle limit.
                // Retain the most recent read timestamp rather than a bounded
                // notification. Output can outpace supervisor polls, and a stale
                // earlier event must not make a later read look idle.
                if let Some(activity) = &activity {
                    activity.observe(Instant::now());
                }
                let write = if stderr {
                    let mut output = io::stderr().lock();
                    write_forwarded_output(&mut output, &buffer[..count])
                } else {
                    let mut output = io::stdout().lock();
                    write_forwarded_output(&mut output, &buffer[..count])
                };
                if write.is_err() {
                    tracing::debug!(operation = "run", stage = "forward-failed", "run_output");
                    if let Some(output_failure) = &output_failure {
                        output_failure.store(true, Ordering::Release);
                    }
                    return;
                }
            }
        })
}

fn write_forwarded_output(output: &mut impl Write, bytes: &[u8]) -> io::Result<()> {
    #[cfg(unix)]
    {
        with_sigttou_blocked(|| output.write_all(bytes).and_then(|()| output.flush()))
    }
    #[cfg(not(unix))]
    {
        output.write_all(bytes).and_then(|()| output.flush())
    }
}

#[derive(Clone)]
struct Activity {
    started_at: Instant,
    latest_elapsed_nanos: Arc<std::sync::atomic::AtomicU64>,
}

impl Activity {
    fn new() -> Self {
        Self::at(Instant::now())
    }

    fn at(started_at: Instant) -> Self {
        Self {
            started_at,
            latest_elapsed_nanos: Arc::new(std::sync::atomic::AtomicU64::new(0)),
        }
    }

    fn observe(&self, observed_at: Instant) {
        let elapsed = observed_at
            .saturating_duration_since(self.started_at)
            .as_nanos();
        let elapsed = u64::try_from(elapsed).unwrap_or(u64::MAX);
        self.latest_elapsed_nanos
            .fetch_max(elapsed, std::sync::atomic::Ordering::Release);
    }

    fn last_observed_at(&self) -> Instant {
        self.started_at
            .checked_add(Duration::from_nanos(
                self.latest_elapsed_nanos
                    .load(std::sync::atomic::Ordering::Acquire),
            ))
            .unwrap_or_else(Instant::now)
    }
}

impl OwnedChild {
    fn output_failed(&self) -> bool {
        self.output_failure
            .as_ref()
            .is_some_and(|failure| failure.load(Ordering::Acquire))
    }

    fn completion(&mut self) -> Result<Option<Completion>> {
        if self.completion.is_none() {
            match self.completions.try_recv() {
                Ok(Ok(completion)) => self.completion = Some(completion),
                Ok(Err(error)) => {
                    self.completion_observation_failed = true;
                    tracing::debug!(
                        operation = "run",
                        pid = self.pid,
                        stage = "completion-unavailable",
                        "run_cleanup"
                    );
                    return Err(error);
                }
                Err(mpsc::TryRecvError::Empty) => (),
                Err(mpsc::TryRecvError::Disconnected) => {
                    self.completion_observation_failed = true;
                    tracing::debug!(
                        operation = "run",
                        pid = self.pid,
                        stage = "completion-unavailable",
                        "run_cleanup"
                    );
                    return runtime_failure(
                        "Cannot observe the owned child process; cleanup may be incomplete.",
                    );
                }
            }
        }
        Ok(self.completion.clone())
    }

    fn request_graceful_termination(&mut self) -> Result<()> {
        if !self.cleanup_tree_running()? {
            return Ok(());
        }
        tracing::debug!(
            operation = "run",
            pid = self.pid,
            stage = "service_failed_workload_stop",
            "run_cleanup"
        );
        self.signal(false)?;
        Ok(())
    }

    fn cleanup(&mut self, kill_after: Duration, initial_cancellation: Option<usize>) -> Result<()> {
        // Continue to supervise the owned process group/job after the direct
        // child exits. A descendant can retain an inherited output pipe, and
        // a forwarding thread can block writing to the wrapper's consumer.
        // Bounded cleanup must never wait for either thread to drain.
        if !self.cleanup_tree_running()? {
            return Ok(());
        }
        tracing::debug!(
            operation = "run",
            pid = self.pid,
            stage = "graceful",
            "run_cleanup"
        );
        let graceful_delivered = self.signal(false)?;
        // A Windows Job can be force-terminated when its isolated process
        // group has no attached console to receive CTRL_BREAK. That fallback
        // is already forced, so skip grace and use the common confirmation
        // window below exactly once.
        let graceful_wait = graceful_cleanup_wait(graceful_delivered, kill_after);
        let graceful_deadline = Instant::now().checked_add(graceful_wait).ok_or_else(|| {
            Failure::new(Code::IoFailed, "Cleanup deadline cannot be represented.")
        })?;
        while Instant::now() < graceful_deadline {
            if !self.cleanup_tree_running()? {
                return Ok(());
            }
            if initial_cancellation
                .is_none_or(|generation| runtime::cancellation_generation() != generation)
                && runtime::cancelled()
            {
                break;
            }
            thread::sleep(POLL);
        }
        tracing::debug!(
            operation = "run",
            pid = self.pid,
            stage = "forced",
            "run_cleanup"
        );
        if graceful_delivered {
            // A zero grace interval still gives the delivered signal one
            // scheduling opportunity. Recheck before SIGKILL because macOS
            // retains the unreaped group leader while the live descendants
            // have already exited.
            if !self.cleanup_tree_running()? {
                return Ok(());
            }
            self.signal(true)?;
        }
        let confirmation = Instant::now()
            .checked_add(CLEANUP_CONFIRMATION)
            .ok_or_else(|| {
                Failure::new(Code::IoFailed, "Cleanup deadline cannot be represented.")
            })?;
        while Instant::now() < confirmation {
            if !self.cleanup_tree_running()? {
                return Ok(());
            }
            thread::sleep(POLL);
        }
        Err(Failure::new(
            Code::TerminationTimeout,
            "Owned child cleanup could not be confirmed before the bounded deadline.",
        ))
    }

    fn join_output_within(
        &mut self,
        limits: &Limits,
        ignored_cancellation_generation: Option<usize>,
    ) -> OutputJoin {
        let now = Instant::now();
        let fallback_deadline = now.checked_add(CLEANUP_CONFIRMATION).unwrap_or(now);
        loop {
            if self
                .output_threads
                .iter()
                .all(thread::JoinHandle::is_finished)
            {
                return if self.join_output() {
                    OutputJoin::Complete
                } else {
                    OutputJoin::Failed
                };
            }
            if ignored_cancellation_generation
                .is_none_or(|generation| runtime::cancellation_generation() != generation)
                && runtime::cancelled()
            {
                return OutputJoin::Cancelled;
            }
            let deadline = output_deadline(limits, self.activity.as_ref(), fallback_deadline);
            let remaining = deadline.saturating_duration_since(Instant::now());
            if remaining.is_zero() {
                return OutputJoin::Deadline;
            }
            thread::sleep(POLL.min(remaining));
        }
    }

    fn join_output(&mut self) -> bool {
        let mut joined = true;
        for handle in self.output_threads.drain(..) {
            joined &= handle.join().is_ok();
        }
        joined
    }

    #[cfg(unix)]
    fn signal(&self, force: bool) -> Result<bool> {
        match self.unix_ownership {
            UnixOwnership::ProcessGroup => signal_process_group(self.pid, force),
            UnixOwnership::DirectChild => signal_process(self.pid, force),
        }?;
        Ok(true)
    }

    #[cfg(windows)]
    fn signal(&self, force: bool) -> Result<bool> {
        self.job.signal(self.pid, force)
    }

    #[cfg(unix)]
    fn tree_running(&mut self) -> Result<bool> {
        if self.completion()?.is_some() {
            return match self.unix_ownership {
                UnixOwnership::ProcessGroup => completed_process_group_running(self.pid),
                // Nested wrappers deliberately own only their direct child.
                // Once that child has exited, never probe or signal its
                // reusable numeric PID on behalf of descendants owned by the
                // outer wrapper.
                UnixOwnership::DirectChild => Ok(false),
            };
        }
        match self.unix_ownership {
            UnixOwnership::ProcessGroup => process_group_running(self.pid),
            UnixOwnership::DirectChild => process_running(self.pid),
        }
    }

    fn cleanup_tree_running(&mut self) -> Result<bool> {
        if !self.completion_observation_failed {
            return self.tree_running();
        }
        #[cfg(unix)]
        {
            let direct_child_exited = self.reap_direct_child_for_cleanup()?;
            return match self.unix_ownership {
                UnixOwnership::ProcessGroup => {
                    if direct_child_exited {
                        completed_process_group_running(self.pid)
                    } else {
                        process_group_running(self.pid)
                    }
                }
                UnixOwnership::DirectChild => {
                    if direct_child_exited {
                        Ok(false)
                    } else {
                        process_running(self.pid)
                    }
                }
            };
        }
        #[cfg(windows)]
        {
            return self.job.is_running();
        }
        #[allow(unreachable_code)]
        runtime_failure("Owned child cleanup is unavailable on this platform.")
    }

    #[cfg(unix)]
    fn reap_direct_child_for_cleanup(&mut self) -> Result<bool> {
        if self.reaped {
            return Ok(true);
        }
        // The completion worker keeps the direct child waitable. If that
        // worker fails, use this independent nonblocking reap only for
        // cleanup, so a live process group still receives termination.
        loop {
            let mut status = 0;
            let observed =
                unsafe { libc::waitpid(self.pid as libc::pid_t, &mut status, libc::WNOHANG) };
            if observed == self.pid as libc::pid_t {
                self.reaped = true;
                return Ok(true);
            }
            if observed == 0 {
                return Ok(false);
            }
            let error = io::Error::last_os_error();
            if error.kind() == io::ErrorKind::Interrupted {
                continue;
            }
            return Err(Failure::io(&error));
        }
    }

    #[cfg(unix)]
    fn exit_observed_without_reaping(&self) -> Result<bool> {
        child_exit_observed_without_reaping(self.pid)
    }

    fn exit_before_workload_completion(&mut self) -> Result<bool> {
        #[cfg(unix)]
        {
            // `kill(-pgid, 0)` reports an unreaped zombie group leader as
            // present. Query its direct child state first so a delayed
            // completion worker cannot turn a service exit into success.
            if self.exit_observed_without_reaping()? {
                return Ok(true);
            }
        }
        self.tree_running().map(|running| !running)
    }

    #[cfg(unix)]
    fn reap_completed_child(&mut self) -> Result<()> {
        if self.reaped || self.completion.is_none() {
            return Ok(());
        }
        loop {
            let mut status = 0;
            let observed = unsafe { libc::waitpid(self.pid as libc::pid_t, &mut status, 0) };
            if observed == self.pid as libc::pid_t {
                self.reaped = true;
                return Ok(());
            }
            if observed == -1 {
                let error = io::Error::last_os_error();
                if error.kind() == io::ErrorKind::Interrupted {
                    continue;
                }
                return Err(Failure::io(&error));
            }
            return runtime_failure("Could not reap the completed owned child.");
        }
    }

    #[cfg(windows)]
    fn tree_running(&self) -> Result<bool> {
        self.job.is_running()
    }
}

fn output_deadline(
    limits: &Limits,
    activity: Option<&Activity>,
    fallback_deadline: Instant,
) -> Instant {
    let idle = activity.and_then(|activity| {
        limits
            .idle
            .and_then(|duration| activity.last_observed_at().checked_add(duration))
    });
    limits
        .overall
        .into_iter()
        .chain(idle)
        .min()
        .unwrap_or(fallback_deadline)
}

enum OutputJoin {
    Complete,
    Deadline,
    Cancelled,
    Failed,
}

fn graceful_cleanup_wait(graceful_delivered: bool, kill_after: Duration) -> Duration {
    if graceful_delivered {
        kill_after
    } else {
        Duration::ZERO
    }
}

#[cfg(unix)]
impl Drop for OwnedChild {
    fn drop(&mut self) {
        let _ = self.reap_completed_child();
        if let Some(terminal) = self.foreground_terminal.take() {
            terminal.restore();
        }
    }
}

#[cfg(unix)]
fn wait_for_completion(
    child: Child,
    foreground_terminal: Option<Arc<ForegroundTerminal>>,
) -> Result<Completion> {
    use std::os::unix::process::ExitStatusExt;

    let pid = child.id() as libc::pid_t;
    // Keep the child waitable until OwnedChild has finished every signal and
    // liveness decision. Reaping would make its numeric PID reusable while
    // cleanup still addresses the owned process group or direct child.
    drop(child);
    loop {
        let mut info = std::mem::MaybeUninit::<libc::siginfo_t>::zeroed();
        let observed = unsafe {
            libc::waitid(
                libc::P_PID,
                pid as libc::id_t,
                info.as_mut_ptr(),
                libc::WEXITED | libc::WSTOPPED | libc::WNOWAIT,
            )
        };
        if observed == -1 {
            let error = io::Error::last_os_error();
            if error.kind() == io::ErrorKind::Interrupted {
                continue;
            }
            return Err(Failure::io(&error));
        }
        let info = unsafe { info.assume_init() };
        if info.si_code == libc::CLD_STOPPED {
            consume_child_stop(pid)?;
            if let Some(terminal) = &foreground_terminal {
                terminal.suspend_wrapper()?;
            }
            continue;
        }
        let status = unsafe { info.si_status() };
        let raw_status = match info.si_code {
            libc::CLD_EXITED => status << 8,
            libc::CLD_KILLED => status,
            libc::CLD_DUMPED => status | 0x80,
            _ => return runtime_failure("Owned child reported an unsupported process state."),
        };
        if libc::WIFEXITED(raw_status) || libc::WIFSIGNALED(raw_status) {
            return Ok(Completion {
                status: ExitStatus::from_raw(raw_status),
                observed_at: Instant::now(),
            });
        }
        return runtime_failure("Owned child reported an unsupported process state.");
    }
}

#[cfg(unix)]
fn child_exit_observed_without_reaping(pid: u32) -> Result<bool> {
    loop {
        let mut info = std::mem::MaybeUninit::<libc::siginfo_t>::zeroed();
        let observed = unsafe {
            libc::waitid(
                libc::P_PID,
                pid as libc::id_t,
                info.as_mut_ptr(),
                libc::WEXITED | libc::WNOHANG | libc::WNOWAIT,
            )
        };
        if observed == 0 {
            let info = unsafe { info.assume_init() };
            return Ok(unsafe { info.si_pid() } == pid as libc::pid_t);
        }
        let error = io::Error::last_os_error();
        if error.kind() == io::ErrorKind::Interrupted {
            continue;
        }
        return Err(Failure::io(&error));
    }
}

#[cfg(unix)]
fn consume_child_stop(pid: libc::pid_t) -> Result<()> {
    loop {
        let mut info = std::mem::MaybeUninit::<libc::siginfo_t>::zeroed();
        let observed = unsafe {
            libc::waitid(
                libc::P_PID,
                pid as libc::id_t,
                info.as_mut_ptr(),
                libc::WSTOPPED,
            )
        };
        if observed == 0 {
            return Ok(());
        }
        let error = io::Error::last_os_error();
        if error.kind() == io::ErrorKind::Interrupted {
            continue;
        }
        return Err(Failure::io(&error));
    }
}

#[cfg(unix)]
fn unix_ownership() -> UnixOwnership {
    if nested_wrapper_state() {
        UnixOwnership::DirectChild
    } else {
        UnixOwnership::ProcessGroup
    }
}

#[cfg(unix)]
fn nested_wrapper_state() -> bool {
    let parent = unsafe { libc::getppid() };
    let own_process = unsafe { libc::getpid() };
    let process_group = unsafe { libc::getpgrp() };
    if parent <= 0 || own_process <= 0 || process_group <= 0 {
        return false;
    }
    let Ok(current) = env::current_exe().and_then(fs::canonicalize) else {
        return false;
    };
    if process_group == own_process
        && unsafe { libc::getpgid(parent) } != own_process
        && executable_matches(parent, &current)
    {
        return true;
    }

    // An installed Node launcher is an intermediate process: the outer
    // wrapper creates a group for it, and every nested wrapper under that
    // launcher must share the group. More launchers may be introduced by
    // further supported wrappers, so walk the contiguous ancestor segment in
    // that group until reaching its owning native wrapper. This is structural
    // process state, rather than launcher arguments or environment values
    // that workloads can control. Keep the walk bounded to fail closed if the
    // process hierarchy changes while it is being examined.
    const MAX_LAUNCHER_ANCESTORS: usize = 64;
    let mut ancestor = parent;
    for _ in 0..MAX_LAUNCHER_ANCESTORS {
        if ancestor <= 0 {
            return false;
        }
        if unsafe { libc::getpgid(ancestor) } != process_group {
            return executable_matches(ancestor, &current);
        }
        if !supported_node_launcher(ancestor) {
            return false;
        }
        let Some(next) = parent_process(ancestor) else {
            return false;
        };
        ancestor = next;
    }
    false
}

#[cfg(unix)]
fn executable_matches(process: libc::pid_t, current: &Path) -> bool {
    parent_executable(process)
        .and_then(|path| path.canonicalize().ok())
        .is_some_and(|executable| executable == current)
}

#[cfg(unix)]
fn supported_node_launcher(process: libc::pid_t) -> bool {
    parent_executable(process).is_some_and(|executable| {
        matches!(
            executable.file_name(),
            Some(name) if name == "node" || name == "nodejs"
        )
    })
}

#[cfg(target_os = "linux")]
fn parent_executable(parent: libc::pid_t) -> Option<PathBuf> {
    fs::read_link(format!("/proc/{parent}/exe")).ok()
}

#[cfg(target_os = "linux")]
fn parent_process(process: libc::pid_t) -> Option<libc::pid_t> {
    let stat = fs::read_to_string(format!("/proc/{process}/stat")).ok()?;
    let (_, fields) = stat.rsplit_once(") ")?;
    fields
        .split_whitespace()
        .nth(1)
        .and_then(|parent| parent.parse().ok())
}

#[cfg(target_os = "macos")]
fn parent_executable(parent: libc::pid_t) -> Option<PathBuf> {
    libproc::libproc::proc_pid::pidpath(parent)
        .ok()
        .map(PathBuf::from)
}

#[cfg(target_os = "macos")]
fn parent_process(process: libc::pid_t) -> Option<libc::pid_t> {
    use libproc::libproc::{bsd_info::BSDInfo, proc_pid::pidinfo};

    pidinfo::<BSDInfo>(process, 0)
        .ok()
        .and_then(|info| libc::pid_t::try_from(info.pbi_ppid).ok())
}

#[cfg(all(unix, not(any(target_os = "linux", target_os = "macos"))))]
fn parent_executable(_parent: libc::pid_t) -> Option<PathBuf> {
    None
}

#[cfg(all(unix, not(any(target_os = "linux", target_os = "macos"))))]
fn parent_process(_process: libc::pid_t) -> Option<libc::pid_t> {
    None
}

#[cfg(unix)]
fn foreground_parent_group(
    ownership: UnixOwnership,
    mode: &OutputMode,
) -> Result<Option<ForegroundTerminalParent>> {
    if !matches!(ownership, UnixOwnership::ProcessGroup) || matches!(mode, OutputMode::Service) {
        return Ok(None);
    }
    let parent_group = unsafe { libc::getpgrp() };
    if parent_group <= 0 {
        return Err(Failure::new(
            Code::IoFailed,
            "Could not identify the wrapper's terminal process group.",
        ));
    }
    let descriptors: &[libc::c_int] = match mode {
        OutputMode::WorkloadInherited => {
            &[libc::STDIN_FILENO, libc::STDOUT_FILENO, libc::STDERR_FILENO]
        }
        OutputMode::WorkloadPiped => &[libc::STDIN_FILENO],
        OutputMode::Service => unreachable!("managed services do not inherit a terminal"),
    };
    for &descriptor in descriptors {
        if unsafe { libc::isatty(descriptor) } == 0 {
            continue;
        }
        let foreground_group = unsafe { libc::tcgetpgrp(descriptor) };
        if foreground_group == -1 {
            let error = io::Error::last_os_error();
            if error.raw_os_error() == Some(libc::ENOTTY) {
                continue;
            }
            return Err(Failure::io(&error));
        }
        if foreground_group == parent_group {
            return Ok(Some(ForegroundTerminalParent {
                descriptor,
                group: parent_group,
            }));
        }
    }
    Ok(None)
}

#[cfg(unix)]
impl ForegroundTerminal {
    fn transfer(parent: ForegroundTerminalParent, child_pid: u32) -> Result<Self> {
        let child_group = child_pid as libc::pid_t;
        set_terminal_foreground_group(parent.descriptor, child_group)
            .map_err(|error| Failure::io(&error))?;
        if let Err(error) = continue_process_group(child_pid) {
            let _ = set_terminal_foreground_group(parent.descriptor, parent.group);
            return Err(error);
        }
        Ok(Self {
            descriptor: parent.descriptor,
            parent_group: parent.group,
            child_group,
        })
    }

    fn restore(&self) {
        if unsafe { libc::tcgetpgrp(self.descriptor) } != self.child_group {
            return;
        }
        if set_terminal_foreground_group(self.descriptor, self.parent_group).is_err() {
            tracing::debug!(
                operation = "run",
                child_group = self.child_group,
                stage = "terminal_restore_failed",
                "run_cleanup"
            );
        }
    }

    fn suspend_wrapper(&self) -> Result<()> {
        // Ctrl+Z is delivered to the foreground child group, not to this
        // wrapper. Observe that stop with waitpid, return terminal ownership
        // to the wrapper's job, then stop that whole job. The installed Node
        // launcher shares this group, so stopping only this process would not
        // return control to the shell. Once the shell continues the job, put
        // the child back in the foreground and continue its separate process
        // group before waiting again.
        set_terminal_foreground_group(self.descriptor, self.parent_group)
            .map_err(|error| Failure::io(&error))?;
        suspend_process_group(self.parent_group)?;
        set_terminal_foreground_group(self.descriptor, self.child_group)
            .map_err(|error| Failure::io(&error))?;
        continue_process_group(self.child_group as u32)
    }
}

#[cfg(unix)]
fn set_terminal_foreground_group(descriptor: libc::c_int, group: libc::pid_t) -> io::Result<()> {
    with_sigttou_blocked(|| {
        if unsafe { libc::tcsetpgrp(descriptor, group) } == -1 {
            Err(io::Error::last_os_error())
        } else {
            Ok(())
        }
    })
}

#[cfg(unix)]
fn with_sigttou_blocked<T>(operation: impl FnOnce() -> io::Result<T>) -> io::Result<T> {
    // A foreground workload leaves forwarding threads in the wrapper's
    // background group. TOSTOP would otherwise deliver SIGTTOU and stop that
    // group before it can supervise timeouts or restore the terminal.
    unsafe {
        let mut blocked = std::mem::MaybeUninit::<libc::sigset_t>::uninit();
        if libc::sigemptyset(blocked.as_mut_ptr()) == -1
            || libc::sigaddset(blocked.as_mut_ptr(), libc::SIGTTOU) == -1
        {
            return Err(io::Error::last_os_error());
        }
        let mut original = std::mem::MaybeUninit::<libc::sigset_t>::uninit();
        let blocked_result =
            libc::pthread_sigmask(libc::SIG_BLOCK, blocked.as_ptr(), original.as_mut_ptr());
        if blocked_result != 0 {
            return Err(io::Error::from_raw_os_error(blocked_result));
        }
        let result = operation();
        let restore_result =
            libc::pthread_sigmask(libc::SIG_SETMASK, original.as_ptr(), std::ptr::null_mut());
        if restore_result != 0 {
            return Err(io::Error::from_raw_os_error(restore_result));
        }
        result
    }
}

#[cfg(unix)]
fn configure_process_group(command: &mut ProcessCommand, ownership: UnixOwnership) {
    use std::os::unix::process::CommandExt;

    if matches!(ownership, UnixOwnership::DirectChild) {
        return;
    }
    unsafe {
        command.pre_exec(|| {
            if libc::setpgid(0, 0) == -1 {
                Err(io::Error::last_os_error())
            } else {
                Ok(())
            }
        });
    }
}

#[cfg(windows)]
fn configure_process_group(command: &mut ProcessCommand) {
    use std::os::windows::process::CommandExt;

    use windows_sys::Win32::System::Threading::{CREATE_NEW_PROCESS_GROUP, CREATE_SUSPENDED};

    command.creation_flags(CREATE_NEW_PROCESS_GROUP | CREATE_SUSPENDED);
}

#[cfg(windows)]
fn resume_suspended_process(child: &Child) -> Result<()> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::{CloseHandle, FALSE, INVALID_HANDLE_VALUE},
        System::{
            Diagnostics::ToolHelp::{
                CreateToolhelp32Snapshot, Thread32First, Thread32Next, TH32CS_SNAPTHREAD,
                THREADENTRY32,
            },
            Threading::{GetProcessId, OpenThread, ResumeThread, THREAD_SUSPEND_RESUME},
        },
    };

    let process = child.as_raw_handle() as _;
    let pid = unsafe { GetProcessId(process) };
    if pid == 0 {
        return Err(Failure::io(&io::Error::last_os_error()));
    }
    let snapshot = unsafe { CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0) };
    if snapshot == INVALID_HANDLE_VALUE {
        return Err(Failure::io(&io::Error::last_os_error()));
    }
    // std::process exposes the process handle but not the primary-thread
    // handle needed by ResumeThread. The snapshot is safe here because
    // CREATE_SUSPENDED prevents the child from creating any threads before
    // Job::attach completes. Remove this narrow workaround if std exposes the
    // primary thread handle for suspended children.
    let result = (|| {
        let mut entry = THREADENTRY32 {
            dwSize: std::mem::size_of::<THREADENTRY32>() as u32,
            ..Default::default()
        };
        if unsafe { Thread32First(snapshot, &mut entry) } == FALSE {
            return Err(Failure::io(&io::Error::last_os_error()));
        }
        let mut resumed = false;
        loop {
            if entry.th32OwnerProcessID == pid {
                let thread =
                    unsafe { OpenThread(THREAD_SUSPEND_RESUME, FALSE, entry.th32ThreadID) };
                if thread.is_null() {
                    return Err(Failure::io(&io::Error::last_os_error()));
                }
                let resume_result = unsafe { ResumeThread(thread) };
                unsafe {
                    CloseHandle(thread);
                }
                if resume_result == u32::MAX {
                    return Err(Failure::io(&io::Error::last_os_error()));
                }
                resumed = true;
            }
            if unsafe { Thread32Next(snapshot, &mut entry) } == FALSE {
                break;
            }
        }
        if !resumed {
            return runtime_failure("Could not resume the owned Windows child process.");
        }
        Ok(())
    })();
    unsafe {
        CloseHandle(snapshot);
    }
    result
}

#[cfg(unix)]
fn signal_process_group(pid: u32, force: bool) -> Result<()> {
    let signal = if force { libc::SIGKILL } else { libc::SIGTERM };
    let result = unsafe { libc::kill(-(pid as i32), signal) };
    if result == -1 {
        let error = io::Error::last_os_error();
        if error.raw_os_error() != Some(libc::ESRCH) {
            if error.raw_os_error() == Some(libc::EPERM) && !completed_process_group_running(pid)? {
                // macOS can report EPERM while an unreaped group leader is
                // the last listed member, even after every live descendant
                // accepted the preceding graceful signal. Recheck liveness
                // before failing cleanup; remove this narrow fallback if
                // Darwin gives zombie-only groups the usual ESRCH result.
                return Ok(());
            }
            tracing::error!(
                operation = "run",
                pid,
                force,
                error_kind = ?error.kind(),
                raw_os_error = ?error.raw_os_error(),
                stage = "group_signal_failed",
                "run_cleanup"
            );
            return Err(Failure::io(&error));
        }
    }
    Ok(())
}

#[cfg(unix)]
fn continue_process_group(pid: u32) -> Result<()> {
    let result = unsafe { libc::kill(-(pid as i32), libc::SIGCONT) };
    if result == -1 {
        let error = io::Error::last_os_error();
        if error.raw_os_error() != Some(libc::ESRCH) {
            return Err(Failure::io(&error));
        }
    }
    Ok(())
}

#[cfg(unix)]
fn suspend_process_group(group: libc::pid_t) -> Result<()> {
    let result = unsafe { libc::kill(-group, libc::SIGTSTP) };
    if result == -1 {
        return Err(Failure::io(&io::Error::last_os_error()));
    }
    Ok(())
}

#[cfg(unix)]
fn signal_process(pid: u32, force: bool) -> Result<()> {
    let signal = if force { libc::SIGKILL } else { libc::SIGTERM };
    let result = unsafe { libc::kill(pid as i32, signal) };
    if result == -1 {
        let error = io::Error::last_os_error();
        if error.raw_os_error() != Some(libc::ESRCH) {
            return Err(Failure::io(&error));
        }
    }
    Ok(())
}

#[cfg(unix)]
fn process_group_running(pid: u32) -> Result<bool> {
    let result = unsafe { libc::kill(-(pid as i32), 0) };
    process_running_result(result)
}

#[cfg(target_os = "linux")]
fn completed_process_group_running(group: u32) -> Result<bool> {
    for entry in fs::read_dir("/proc").map_err(|error| Failure::io(&error))? {
        let entry = entry.map_err(|error| Failure::io(&error))?;
        let name = entry.file_name();
        let Some(candidate) = name.to_str().and_then(|value| value.parse::<u32>().ok()) else {
            continue;
        };
        // The direct child is deliberately unreaped while cleanup is in
        // progress. It is a zombie, not a descendant that still needs a
        // termination signal.
        if candidate == group {
            continue;
        }
        let stat = match fs::read_to_string(entry.path().join("stat")) {
            Ok(stat) => stat,
            Err(error) if error.kind() == io::ErrorKind::NotFound => continue,
            Err(error) if error.kind() == io::ErrorKind::PermissionDenied => {
                // hidepid can deny unrelated process details. getpgid still
                // lets us fail closed if the inaccessible PID belongs to the
                // owned group, without rejecting unrelated entries.
                if linux_process_group_matches(candidate, group)? {
                    return Ok(true);
                }
                continue;
            }
            Err(error) => return Err(Failure::io(&error)),
        };
        if linux_process_group_member(&stat, group)? {
            return Ok(true);
        }
    }
    Ok(false)
}

#[cfg(target_os = "linux")]
fn linux_process_group_matches(pid: u32, group: u32) -> Result<bool> {
    let observed = unsafe { libc::getpgid(pid as libc::pid_t) };
    if observed == -1 {
        let error = io::Error::last_os_error();
        if error.raw_os_error() == Some(libc::ESRCH) {
            return Ok(false);
        }
        return Err(Failure::io(&error));
    }
    Ok(observed as u32 == group)
}

#[cfg(target_os = "linux")]
fn linux_process_group_member(stat: &str, group: u32) -> Result<bool> {
    // Linux /proc/<pid>/stat permits spaces and parentheses in comm, so parse
    // the stable fields only after the final closing parenthesis.
    let (_, fields) = stat
        .rsplit_once(')')
        .ok_or_else(|| Failure::new(Code::IoFailed, "Could not parse process status."))?;
    let mut fields = fields.split_whitespace();
    let state = fields
        .next()
        .ok_or_else(|| Failure::new(Code::IoFailed, "Could not parse process status."))?;
    let _parent = fields
        .next()
        .ok_or_else(|| Failure::new(Code::IoFailed, "Could not parse process status."))?;
    let process_group = fields
        .next()
        .ok_or_else(|| Failure::new(Code::IoFailed, "Could not parse process status."))?
        .parse::<u32>()
        .map_err(|_| Failure::new(Code::IoFailed, "Could not parse process status."))?;
    Ok(state != "Z" && process_group == group)
}

#[cfg(target_os = "macos")]
fn completed_process_group_running(group: u32) -> Result<bool> {
    const INITIAL_GROUP_MEMBERS: usize = 64;
    const MAXIMUM_GROUP_MEMBERS: usize = 4096;

    let mut members = vec![0; INITIAL_GROUP_MEMBERS];
    loop {
        let bytes = members
            .len()
            .checked_mul(std::mem::size_of::<libc::pid_t>())
            .and_then(|bytes| libc::c_int::try_from(bytes).ok())
            .ok_or_else(|| {
                Failure::new(Code::IoFailed, "Process-group member list is too large.")
            })?;
        // proc_listpgrppids returns a PID count, rather than a byte count.
        let count = unsafe {
            libc::proc_listpgrppids(group as libc::pid_t, members.as_mut_ptr().cast(), bytes)
        };
        if count < 0 {
            let error = io::Error::last_os_error();
            tracing::error!(
                operation = "run",
                group,
                error_kind = ?error.kind(),
                raw_os_error = ?error.raw_os_error(),
                stage = "group_list_failed",
                "run_cleanup"
            );
            return Err(Failure::io(&error));
        }
        let count = usize::try_from(count).map_err(|_| {
            Failure::new(Code::IoFailed, "Could not inspect the owned process group.")
        })?;
        if count > members.len() {
            return runtime_failure("Could not inspect the owned process group.");
        }
        if count == members.len() {
            if members.len() == MAXIMUM_GROUP_MEMBERS {
                return runtime_failure("Could not inspect the owned process group.");
            }
            members.resize((members.len() * 2).min(MAXIMUM_GROUP_MEMBERS), 0);
            continue;
        }
        for member in &members[..count] {
            // The direct child is deliberately retained as a zombie while
            // cleanup owns its process group. macOS can deny proc_pidinfo for
            // that unreaped group leader, but it cannot be a live descendant
            // that still needs a signal.
            if *member > 0
                && *member != group as libc::pid_t
                && macos_process_group_member_running(*member, group)?
            {
                return Ok(true);
            }
        }
        return Ok(false);
    }
}

#[cfg(target_os = "macos")]
fn macos_process_group_member_running(pid: libc::pid_t, group: u32) -> Result<bool> {
    // proc_listpgrppids includes zombie processes. Recheck both membership
    // and status before treating a listed PID as a live cleanup target.
    let mut info = std::mem::MaybeUninit::<libc::proc_bsdinfo>::zeroed();
    let bytes = libc::c_int::try_from(std::mem::size_of::<libc::proc_bsdinfo>())
        .map_err(|_| Failure::new(Code::IoFailed, "Could not inspect the owned process group."))?;
    let observed = unsafe {
        libc::proc_pidinfo(
            pid,
            libc::PROC_PIDTBSDINFO,
            0,
            info.as_mut_ptr().cast(),
            bytes,
        )
    };
    if observed == 0 {
        let error = io::Error::last_os_error();
        if error.raw_os_error() == Some(libc::ESRCH) {
            return Ok(false);
        }
        tracing::error!(
            operation = "run",
            pid,
            group,
            error_kind = ?error.kind(),
            raw_os_error = ?error.raw_os_error(),
            stage = "group_member_inspection_failed",
            "run_cleanup"
        );
        return Err(Failure::io(&error));
    }
    if observed != bytes {
        return runtime_failure("Could not inspect the owned process group.");
    }
    let info = unsafe { info.assume_init() };
    Ok(info.pbi_pgid == group && info.pbi_status != libc::SZOMB)
}

#[cfg(all(unix, not(any(target_os = "linux", target_os = "macos"))))]
fn completed_process_group_running(_group: u32) -> Result<bool> {
    runtime_failure("Could not inspect the owned process group on this platform.")
}

#[cfg(unix)]
fn process_running(pid: u32) -> Result<bool> {
    let result = unsafe { libc::kill(pid as i32, 0) };
    process_running_result(result)
}

#[cfg(unix)]
fn process_running_result(result: i32) -> Result<bool> {
    if result == 0 {
        return Ok(true);
    }
    let error = io::Error::last_os_error();
    match error.raw_os_error() {
        Some(libc::ESRCH) => Ok(false),
        Some(libc::EPERM) => Ok(true),
        _ => Err(Failure::io(&error)),
    }
}

#[cfg(windows)]
struct Job {
    handle: windows_sys::Win32::Foundation::HANDLE,
}

#[cfg(windows)]
impl Job {
    fn attach(child: &Child) -> Result<Self> {
        use std::os::windows::io::AsRawHandle;

        use windows_sys::Win32::{
            Foundation::CloseHandle,
            System::JobObjects::{
                AssignProcessToJobObject, CreateJobObjectW, JobObjectExtendedLimitInformation,
                SetInformationJobObject, JOBOBJECT_EXTENDED_LIMIT_INFORMATION,
                JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
            },
        };

        let handle = unsafe { CreateJobObjectW(std::ptr::null(), std::ptr::null()) };
        if handle.is_null() {
            return Err(Failure::io(&io::Error::last_os_error()));
        }
        let mut information = JOBOBJECT_EXTENDED_LIMIT_INFORMATION::default();
        information.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
        let configured = unsafe {
            SetInformationJobObject(
                handle,
                JobObjectExtendedLimitInformation,
                &information as *const _ as *const _,
                std::mem::size_of_val(&information) as u32,
            )
        } != 0;
        let assigned = configured
            && unsafe { AssignProcessToJobObject(handle, child.as_raw_handle() as _) } != 0;
        if !assigned {
            unsafe {
                CloseHandle(handle);
            }
            return Err(Failure::io(&io::Error::last_os_error()));
        }
        Ok(Self { handle })
    }

    fn signal(&self, pid: u32, force: bool) -> Result<bool> {
        use windows_sys::Win32::{
            Foundation::FALSE,
            System::{
                Console::{GenerateConsoleCtrlEvent, CTRL_BREAK_EVENT},
                JobObjects::TerminateJobObject,
            },
        };
        if force {
            if unsafe { TerminateJobObject(self.handle, 1) } == FALSE {
                return Err(Failure::io(&io::Error::last_os_error()));
            }
            return Ok(true);
        }
        if unsafe { GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid) } != FALSE {
            return Ok(true);
        }
        // A detached or headless process group cannot receive a console
        // control event. The Job still owns every descendant, so terminate it
        // now and let cleanup perform its normal bounded confirmation.
        if unsafe { TerminateJobObject(self.handle, 1) } == FALSE {
            return Err(Failure::io(&io::Error::last_os_error()));
        }
        Ok(false)
    }

    fn is_running(&self) -> Result<bool> {
        use windows_sys::Win32::System::JobObjects::{
            JobObjectBasicAccountingInformation, QueryInformationJobObject,
            JOBOBJECT_BASIC_ACCOUNTING_INFORMATION,
        };

        let mut information = JOBOBJECT_BASIC_ACCOUNTING_INFORMATION::default();
        let queried = unsafe {
            QueryInformationJobObject(
                self.handle,
                JobObjectBasicAccountingInformation,
                &mut information as *mut _ as *mut _,
                std::mem::size_of_val(&information) as u32,
                std::ptr::null_mut(),
            )
        };
        if queried == 0 {
            return Err(Failure::io(&io::Error::last_os_error()));
        }
        Ok(information.ActiveProcesses > 0)
    }
}

#[cfg(windows)]
impl Drop for Job {
    fn drop(&mut self) {
        unsafe {
            windows_sys::Win32::Foundation::CloseHandle(self.handle);
        }
    }
}

#[cfg(windows)]
fn atomic_rename(temporary: &Path, destination: &Path) -> io::Result<()> {
    use std::os::windows::ffi::OsStrExt;

    use windows_sys::Win32::Storage::FileSystem::{
        MoveFileExW, MOVEFILE_REPLACE_EXISTING, MOVEFILE_WRITE_THROUGH,
    };

    let source: Vec<u16> = temporary
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    let target: Vec<u16> = destination
        .as_os_str()
        .encode_wide()
        .chain(std::iter::once(0))
        .collect();
    if unsafe {
        MoveFileExW(
            source.as_ptr(),
            target.as_ptr(),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )
    } == 0
    {
        Err(io::Error::last_os_error())
    } else {
        Ok(())
    }
}

#[cfg(not(windows))]
fn atomic_rename(temporary: &Path, destination: &Path) -> io::Result<()> {
    fs::rename(temporary, destination)
}

#[derive(Clone, Copy)]
enum Namespace {
    Lock,
    Rate,
}

struct StateKey {
    lock_path: PathBuf,
    data_path: PathBuf,
}

impl StateKey {
    fn new(namespace: Namespace, name: &str, scope: &ScopeOptions) -> Result<Self> {
        if scope.scope == Scope::User && scope.project_dir.is_some() {
            return invalid("--project-dir is invalid with --scope user.");
        }
        let root = state_root()?;
        let identity = match scope.scope {
            Scope::Project => canonical_project(scope.project_dir.as_deref())?,
            Scope::User => b"current-user".to_vec(),
        };
        let kind = match namespace {
            Namespace::Lock => "lock",
            Namespace::Rate => "rate",
        };
        let machine = machine_identity()?;
        let digest = hash_key(kind, scope.scope, &machine, &identity, name);
        let namespace_root = root.join(match namespace {
            Namespace::Lock => "locks",
            Namespace::Rate => "buckets",
        });
        ensure_private_dir(&root)?;
        ensure_private_dir(&namespace_root)?;
        Ok(Self {
            lock_path: namespace_root.join(format!("{digest}.lock")),
            data_path: namespace_root.join(format!("{digest}.json")),
        })
    }
}

fn state_root() -> Result<PathBuf> {
    #[cfg(feature = "test-support")]
    if let Some(path) = test_state_root(env::var_os("CLIBOX_TEST_STATE_ROOT"))? {
        return Ok(path);
    }
    #[cfg(target_os = "linux")]
    {
        if let Some(value) = env::var_os("XDG_STATE_HOME") {
            let path = PathBuf::from(value);
            if path.is_absolute() {
                return Ok(path.join("clibox").join("run"));
            }
            return runtime_failure("XDG_STATE_HOME must be an absolute path.");
        }
        home_dir().map(|home| home.join(".local").join("state").join("clibox").join("run"))
    }
    #[cfg(target_os = "macos")]
    {
        home_dir().map(|home| {
            home.join("Library")
                .join("Application Support")
                .join("clibox")
                .join("run")
        })
    }
    #[cfg(windows)]
    {
        windows_local_app_data().map(|path| path.join("clibox").join("run"))
    }
}

#[cfg(feature = "test-support")]
fn test_state_root(value: Option<OsString>) -> Result<Option<PathBuf>> {
    let Some(value) = value else {
        return Ok(None);
    };
    let path = PathBuf::from(value);
    if !path.is_absolute() {
        return runtime_failure("CLIBOX_TEST_STATE_ROOT must be an absolute path.");
    }
    Ok(Some(path.join("clibox").join("run")))
}

#[cfg(all(test, feature = "test-support"))]
#[test]
fn test_state_root_requires_an_absolute_path() {
    let temporary = std::env::temp_dir();
    assert_eq!(
        test_state_root(Some(temporary.clone().into()))
            .expect("absolute test root")
            .expect("provided test root"),
        temporary.join("clibox").join("run")
    );
    assert!(test_state_root(Some("relative-test-root".into())).is_err());
}

#[cfg(windows)]
fn windows_local_app_data() -> Result<PathBuf> {
    use std::os::windows::ffi::OsStringExt;

    use windows_sys::Win32::{
        Foundation::{RPC_E_CHANGED_MODE, S_FALSE, S_OK},
        System::Com::{CoInitializeEx, CoTaskMemFree, CoUninitialize, COINIT_MULTITHREADED},
        UI::Shell::{FOLDERID_LocalAppData, SHGetKnownFolderPath},
    };

    let initialized = match unsafe { CoInitializeEx(std::ptr::null(), COINIT_MULTITHREADED as u32) }
    {
        S_OK | S_FALSE => true,
        // The thread already has a compatible COM initialization boundary for
        // the shell API. Do not balance an initialization this call did not
        // create.
        RPC_E_CHANGED_MODE => false,
        _ => {
            return runtime_failure("Cannot initialize the LocalAppData directory lookup.");
        }
    };
    let mut path = std::ptr::null_mut();
    let status =
        unsafe { SHGetKnownFolderPath(&FOLDERID_LocalAppData, 0, std::ptr::null_mut(), &mut path) };
    if status < 0 || path.is_null() {
        if initialized {
            unsafe {
                CoUninitialize();
            }
        }
        return runtime_failure("Cannot resolve the current user's LocalAppData state directory.");
    }
    // SHGetKnownFolderPath allocates a NUL-terminated UTF-16 string with the
    // COM task allocator. Copy it before balancing the caller initialization.
    let value = unsafe {
        let length = (0..).take_while(|index| *path.add(*index) != 0).count();
        PathBuf::from(OsString::from_wide(std::slice::from_raw_parts(
            path, length,
        )))
    };
    unsafe {
        CoTaskMemFree(path.cast());
        if initialized {
            CoUninitialize();
        }
    }
    value
        .is_absolute()
        .then_some(value)
        .ok_or_else(|| Failure::new(Code::IoFailed, "LocalAppData is not an absolute path."))
}

#[cfg(not(windows))]
fn home_dir() -> Result<PathBuf> {
    env::var_os("HOME")
        .map(PathBuf::from)
        .filter(|path| path.is_absolute())
        .ok_or_else(|| {
            Failure::new(
                Code::IoFailed,
                "Cannot resolve the current user's home directory.",
            )
        })
}

fn canonical_project(project_dir: Option<&Path>) -> Result<Vec<u8>> {
    let candidate = match project_dir {
        Some(path) => path.to_path_buf(),
        None => env::current_dir().map_err(|error| Failure::io(&error))?,
    };
    let actual = candidate
        .canonicalize()
        .map_err(|error| Failure::io(&error))?;
    if !actual.is_dir() {
        return invalid("--project-dir must name an existing directory.");
    }
    Ok(actual.into_os_string().as_encoded_bytes().to_vec())
}

fn hash_key(kind: &str, scope: Scope, machine: &[u8], identity: &[u8], name: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(kind.as_bytes());
    hasher.update([0]);
    hasher.update(match scope {
        Scope::Project => b"project".as_slice(),
        Scope::User => b"user".as_slice(),
    });
    hasher.update([0]);
    hasher.update(machine);
    hasher.update([0]);
    hasher.update(identity);
    hasher.update([0]);
    hasher.update(name.as_bytes());
    format!("{:x}", hasher.finalize())
}

#[cfg(target_os = "linux")]
fn machine_identity() -> Result<Vec<u8>> {
    for path in ["/etc/machine-id", "/var/lib/dbus/machine-id"] {
        if let Some(identity) = read_linux_identity(Path::new(path), valid_machine_id)? {
            return Ok(identity);
        }
    }
    // Minimal containers, including Alpine images without D-Bus, can omit both
    // persistent machine-ID files. Linux's boot ID is stable for every process
    // in that kernel instance, keeping local admission state coordinated until
    // the container or host restarts without publishing an identity file.
    read_linux_identity(Path::new("/proc/sys/kernel/random/boot_id"), valid_uuid)?
        .ok_or_else(|| Failure::new(Code::IoFailed, "The local machine identity is unavailable."))
}

#[cfg(any(target_os = "linux", test))]
fn read_linux_identity(path: &Path, valid: fn(&str) -> bool) -> Result<Option<Vec<u8>>> {
    let value = match fs::read_to_string(path) {
        Ok(value) => value,
        Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(None),
        Err(error) => return Err(Failure::io(&error)),
    };
    let value = value.trim_end();
    if !valid(value) {
        return runtime_failure("The local machine identity is invalid.");
    }
    Ok(Some(value.as_bytes().to_vec()))
}

#[cfg(any(target_os = "linux", test))]
fn valid_machine_id(value: &str) -> bool {
    value.len() == 32
        && value.bytes().all(|byte| byte.is_ascii_hexdigit())
        && value.bytes().any(|byte| byte != b'0')
}

#[cfg(any(target_os = "linux", windows, test))]
fn valid_uuid(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| match index {
            8 | 13 | 18 | 23 => byte == b'-',
            _ => byte.is_ascii_hexdigit(),
        })
        && value
            .bytes()
            .any(|byte| byte.is_ascii_hexdigit() && byte != b'0')
}

#[cfg(target_os = "macos")]
fn machine_identity() -> Result<Vec<u8>> {
    let mut identity = [0u8; 16];
    let timeout = libc::timespec {
        tv_sec: 1,
        tv_nsec: 0,
    };
    if unsafe { libc::gethostuuid(identity.as_mut_ptr(), &timeout) } != 0 {
        return Err(Failure::io(&io::Error::last_os_error()));
    }
    if identity.iter().all(|byte| *byte == 0) {
        return runtime_failure("The local machine identity is invalid.");
    }
    Ok(identity.to_vec())
}

#[cfg(windows)]
fn machine_identity() -> Result<Vec<u8>> {
    use windows_sys::Win32::{
        Foundation::ERROR_SUCCESS,
        System::Registry::{RegGetValueW, HKEY_LOCAL_MACHINE, RRF_RT_REG_SZ},
    };

    let key = wide("SOFTWARE\\Microsoft\\Cryptography");
    let name = wide("MachineGuid");
    let mut value = [0u16; 64];
    let mut value_bytes = std::mem::size_of_val(&value) as u32;
    let status = unsafe {
        RegGetValueW(
            HKEY_LOCAL_MACHINE,
            key.as_ptr(),
            name.as_ptr(),
            RRF_RT_REG_SZ,
            std::ptr::null_mut(),
            value.as_mut_ptr().cast(),
            &mut value_bytes,
        )
    };
    if status != ERROR_SUCCESS {
        return Err(Failure::io(&io::Error::from_raw_os_error(status as i32)));
    }
    let value_len = usize::try_from(value_bytes)
        .ok()
        .filter(|length| length % std::mem::size_of::<u16>() == 0)
        .map(|length| length / std::mem::size_of::<u16>())
        .filter(|&length| length > 0 && length <= value.len())
        .ok_or_else(|| Failure::new(Code::IoFailed, "The local machine identity is invalid."))?;
    let value = value[..value_len]
        .strip_suffix(&[0])
        .unwrap_or(&value[..value_len]);
    let identity = String::from_utf16(value)
        .map_err(|_| Failure::new(Code::IoFailed, "The local machine identity is invalid."))?;
    if !valid_uuid(&identity) {
        return runtime_failure("The local machine identity is invalid.");
    }
    Ok(identity.into_bytes())
}

#[cfg(windows)]
fn wide(value: &str) -> Vec<u16> {
    value.encode_utf16().chain(std::iter::once(0)).collect()
}

fn ensure_private_dir(path: &Path) -> Result<()> {
    #[cfg(unix)]
    ensure_private_state_ancestors(path)?;
    let existed = path.try_exists().map_err(|error| Failure::io(&error))?;
    fs::create_dir_all(path).map_err(|error| Failure::io(&error))?;
    #[cfg(unix)]
    if !existed {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(path, fs::Permissions::from_mode(0o700))
            .map_err(|error| Failure::io(&error))?;
    }
    #[cfg(target_os = "macos")]
    if !existed {
        clear_macos_acl(path)?;
    }
    #[cfg(windows)]
    {
        let directory = open_state_directory(path)?;
        ensure_windows_state_object(&directory, true)?;
        if !existed {
            // A state directory is created only after its handle proves it is
            // not a reparse substitute owned by another account. Restrict its
            // DACL before creating lock or bucket files beneath it.
            restrict_windows_state_dacl(&directory, true)?;
        }
        ensure_windows_private_dacl(&directory)?;
        Ok(())
    }
    #[cfg(not(windows))]
    {
        let metadata = fs::symlink_metadata(path).map_err(|error| Failure::io(&error))?;
        if !metadata.file_type().is_dir() || metadata.file_type().is_symlink() {
            return runtime_failure("Execution state directory is not a safe directory.");
        }
        #[cfg(unix)]
        {
            use std::os::unix::fs::PermissionsExt;
            if metadata.permissions().mode() & 0o077 != 0 || !owned_by_effective_user(&metadata) {
                return runtime_failure(
                    "Execution state directory permissions or ownership are unsafe.",
                );
            }
        }
        #[cfg(target_os = "macos")]
        ensure_private_macos_acl(path)?;
        Ok(())
    }
}

#[cfg(unix)]
fn ensure_private_state_ancestors(path: &Path) -> Result<()> {
    use std::os::unix::fs::{MetadataExt, PermissionsExt};

    if !path.is_absolute() {
        return runtime_failure("Execution state directory must be an absolute path.");
    }

    let mut ancestors = path.ancestors().collect::<Vec<_>>();
    ancestors.reverse();
    let mut parent_mode = None;
    for ancestor in ancestors {
        let link_metadata = match fs::symlink_metadata(ancestor) {
            Ok(metadata) => metadata,
            Err(error) if error.kind() == io::ErrorKind::NotFound => {
                if parent_mode.is_some_and(|mode| mode & 0o022 != 0) {
                    return runtime_failure(
                        "Execution state directory permissions or ownership are unsafe.",
                    );
                }
                break;
            }
            Err(error) => return Err(Failure::io(&error)),
        };
        let metadata = if link_metadata.file_type().is_symlink() {
            if !owned_by_effective_user(&link_metadata) && !owned_by_root(&link_metadata) {
                return runtime_failure(
                    "Execution state directory permissions or ownership are unsafe.",
                );
            }
            fs::metadata(ancestor).map_err(|error| Failure::io(&error))?
        } else {
            link_metadata
        };
        if !metadata.file_type().is_dir() || metadata.file_type().is_symlink() {
            return runtime_failure("Execution state directory is not a safe directory.");
        }
        if !state_ancestor_ownership_is_safe(metadata.uid(), unsafe { libc::geteuid() }) {
            return runtime_failure(
                "Execution state directory permissions or ownership are unsafe.",
            );
        }
        if let Some(parent_mode) = parent_mode {
            let parent_is_private = parent_mode & 0o022 == 0;
            let parent_is_sticky = parent_mode & 0o1000 != 0;
            if !(parent_is_private || parent_is_sticky && owned_by_effective_user(&metadata)) {
                return runtime_failure(
                    "Execution state directory permissions or ownership are unsafe.",
                );
            }
        }
        parent_mode = Some(metadata.permissions().mode());
    }
    Ok(())
}

#[cfg(unix)]
fn state_ancestor_ownership_is_safe(owner: u32, effective_user: u32) -> bool {
    owner == 0 || owner == effective_user
}

#[cfg(windows)]
fn open_windows_state_lock(path: &Path) -> Result<(File, bool)> {
    use std::os::windows::fs::OpenOptionsExt;

    use windows_sys::Win32::{
        Foundation::{GENERIC_READ, GENERIC_WRITE},
        Storage::FileSystem::{FILE_FLAG_OPEN_REPARSE_POINT, READ_CONTROL, WRITE_DAC},
    };

    let open = |create_new| {
        let mut options = OpenOptions::new();
        options.read(true).write(true);
        if create_new {
            options.create_new(true);
        }
        options
            .access_mode(GENERIC_READ | GENERIC_WRITE | READ_CONTROL | WRITE_DAC)
            .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT)
            .open(path)
    };
    match open(false) {
        Ok(file) => Ok((file, false)),
        Err(error) if error.kind() == io::ErrorKind::NotFound => match open(true) {
            Ok(file) => Ok((file, true)),
            // Another same-user invocation can create the lock after the
            // first open reports NotFound. Reopen it as existing state so its
            // DACL is verified rather than silently rewritten.
            Err(error) if error.kind() == io::ErrorKind::AlreadyExists => open(false)
                .map(|file| (file, false))
                .map_err(|error| Failure::io(&error)),
            Err(error) => Err(Failure::io(&error)),
        },
        Err(error) => Err(Failure::io(&error)),
    }
}

fn open_lock(path: &Path) -> Result<File> {
    #[cfg(unix)]
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};

    #[cfg(target_os = "macos")]
    let existed = path.try_exists().map_err(|error| Failure::io(&error))?;
    #[cfg(windows)]
    let (file, created) = open_windows_state_lock(path)?;
    #[cfg(not(windows))]
    let file = {
        let mut options = OpenOptions::new();
        options.create(true).read(true).write(true);
        #[cfg(unix)]
        {
            options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
        }
        options.open(path).map_err(|error| Failure::io(&error))?
    };
    #[cfg(target_os = "macos")]
    if !existed {
        clear_macos_acl(path)?;
    }
    let metadata = file.metadata().map_err(|error| Failure::io(&error))?;
    if !metadata.is_file() {
        return runtime_failure("Execution state lock is not a regular file.");
    }
    #[cfg(unix)]
    if metadata.permissions().mode() & 0o077 != 0
        || metadata.nlink() != 1
        || !owned_by_effective_user(&metadata)
    {
        return runtime_failure(
            "Execution state lock permissions, links, or ownership are unsafe.",
        );
    }
    #[cfg(target_os = "macos")]
    ensure_private_macos_acl(path)?;
    #[cfg(windows)]
    {
        ensure_windows_state_object(&file, false)?;
        if created {
            // A new child inherits the directory ACE, but that does not make
            // its descriptor protected. Install a direct DACL before it can
            // become accepted coordination state; existing state still fails
            // closed instead of being silently repaired.
            restrict_windows_state_dacl(&file, false)?;
        }
        ensure_windows_private_dacl(&file)?;
    }
    Ok(file)
}

fn try_acquire(path: &Path) -> Result<Option<File>> {
    let file = open_lock(path)?;
    match FileExt::try_lock(&file) {
        Ok(()) => Ok(Some(file)),
        Err(TryLockError::WouldBlock) => Ok(None),
        Err(TryLockError::Error(error)) => Err(Failure::io(&error)),
    }
}

fn acquire(path: &Path, deadline: Option<Instant>) -> Result<File> {
    let mut attempted = false;
    loop {
        check_cancelled()?;
        if admission_deadline_expired(deadline, attempted) {
            return Err(Failure::new(
                Code::TerminationTimeout,
                "Execution admission wait timed out.",
            ));
        }
        attempted = true;
        if let Some(lock) = try_acquire(path)? {
            return Ok(lock);
        }
        if deadline.is_some_and(|limit| Instant::now() >= limit) {
            return Err(Failure::new(
                Code::TerminationTimeout,
                "Execution admission wait timed out.",
            ));
        }
        thread::sleep(POLL);
    }
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Bucket {
    version: u8,
    limit: u64,
    period_ms: u64,
    burst: u64,
    tokens: f64,
    refill_utc_ms: i64,
}

impl Bucket {
    fn wait_duration(&self) -> Duration {
        let missing = (1.0 - self.tokens).max(0.0);
        let millis = (missing * self.period_ms as f64 / self.limit as f64)
            .ceil()
            .max(1.0);
        Duration::from_millis(millis.min(u64::MAX as f64) as u64)
    }
}

fn read_bucket(path: &Path, limit: u64, period: Duration, burst: u64, now: i64) -> Result<Bucket> {
    let period_ms = u64::try_from(period.as_millis())
        .map_err(|_| Failure::new(Code::InvalidInput, "Duration is too large."))?;
    let mut bucket = match open_state_for_read(path) {
        Ok(mut file) => {
            let metadata = file.metadata().map_err(|error| Failure::io(&error))?;
            ensure_safe_state_metadata(&metadata)?;
            #[cfg(target_os = "macos")]
            ensure_private_macos_acl(path)?;
            if metadata.len() > MAX_BUCKET_STATE_BYTES {
                return runtime_failure("Execution rate-limit state is malformed or unsupported.");
            }
            #[cfg(windows)]
            {
                ensure_windows_state_object(&file, false)?;
                ensure_windows_private_dacl(&file)?;
            }
            let mut bytes = Vec::with_capacity((MAX_BUCKET_STATE_BYTES + 1) as usize);
            Read::by_ref(&mut file)
                .take(MAX_BUCKET_STATE_BYTES + 1)
                .read_to_end(&mut bytes)
                .map_err(|error| Failure::io(&error))?;
            if bytes.len() as u64 > MAX_BUCKET_STATE_BYTES {
                return runtime_failure("Execution rate-limit state is malformed or unsupported.");
            }
            serde_json::from_slice::<Bucket>(&bytes).map_err(|_| {
                Failure::new(
                    Code::IoFailed,
                    "Execution rate-limit state is malformed or unsupported.",
                )
            })?
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => Bucket {
            version: STATE_VERSION,
            limit,
            period_ms,
            burst,
            tokens: burst as f64,
            refill_utc_ms: now,
        },
        Err(error) => return Err(Failure::io(&error)),
    };
    if bucket.version != STATE_VERSION
        || bucket.limit == 0
        || bucket.period_ms == 0
        || bucket.burst == 0
        || bucket.refill_utc_ms < 0
        || !bucket.tokens.is_finite()
        || !(0.0..=bucket.burst as f64).contains(&bucket.tokens)
    {
        return runtime_failure("Execution rate-limit state is malformed or unsupported.");
    }
    if bucket.limit != limit || bucket.period_ms != period_ms || bucket.burst != burst {
        return runtime_failure(
            "Existing rate-limit configuration does not match this invocation.",
        );
    }
    if now > bucket.refill_utc_ms {
        let elapsed = now.checked_sub(bucket.refill_utc_ms).ok_or_else(|| {
            Failure::new(
                Code::IoFailed,
                "Execution rate-limit state is malformed or unsupported.",
            )
        })? as f64;
        bucket.tokens = (bucket.tokens + elapsed * bucket.limit as f64 / bucket.period_ms as f64)
            .min(bucket.burst as f64);
        bucket.refill_utc_ms = now;
    }
    Ok(bucket)
}

fn write_bucket(path: &Path, bucket: &Bucket) -> Result<()> {
    let bytes = serde_json::to_vec(bucket).map_err(|_| {
        Failure::new(
            Code::IoFailed,
            "Execution rate-limit state could not be serialized.",
        )
    })?;
    let parent = path.parent().ok_or_else(|| {
        Failure::new(
            Code::IoFailed,
            "Execution rate-limit state location is invalid.",
        )
    })?;
    let temporary = parent.join(format!(
        ".{}.{}.{:032x}.tmp",
        path.file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("state"),
        std::process::id(),
        rand::rng().random::<u128>()
    ));
    let result = (|| -> Result<()> {
        let mut options = OpenOptions::new();
        options.create_new(true).write(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
        }
        #[cfg(windows)]
        {
            use std::os::windows::fs::OpenOptionsExt;

            use windows_sys::Win32::{
                Foundation::GENERIC_WRITE,
                Storage::FileSystem::{FILE_FLAG_OPEN_REPARSE_POINT, READ_CONTROL, WRITE_DAC},
            };
            options
                .access_mode(GENERIC_WRITE | READ_CONTROL | WRITE_DAC)
                .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT);
        }
        let mut file = options
            .open(&temporary)
            .map_err(|error| Failure::io(&error))?;
        #[cfg(target_os = "macos")]
        clear_macos_acl(&temporary)?;
        #[cfg(windows)]
        {
            ensure_windows_state_object(&file, false)?;
            restrict_windows_state_dacl(&file, false)?;
            ensure_windows_private_dacl(&file)?;
        }
        file.write_all(&bytes)
            .map_err(|error| Failure::io(&error))?;
        file.sync_all().map_err(|error| Failure::io(&error))?;
        atomic_rename(&temporary, path).map_err(|error| Failure::io(&error))?;
        sync_state_directory(parent).map_err(|error| Failure::io(&error))
    })();
    if result.is_err() {
        let _ = fs::remove_file(&temporary);
    }
    result
}

fn sync_state_directory(parent: &Path) -> io::Result<()> {
    #[cfg(not(windows))]
    {
        File::open(parent).and_then(|directory| directory.sync_all())
    }
    #[cfg(windows)]
    {
        // MoveFileExW uses MOVEFILE_WRITE_THROUGH, which is the Windows
        // durability boundary for this replacement. Directories cannot be
        // opened through std::fs::File without directory-specific flags.
        let _ = parent;
        Ok(())
    }
}

fn open_state_for_read(path: &Path) -> io::Result<File> {
    let mut options = OpenOptions::new();
    options.read(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        // A substituted FIFO blocks a blocking read-only open before its
        // metadata can be checked. Open nonblocking so every non-regular
        // state object is rejected through the normal fail-closed validation.
        options.custom_flags(libc::O_NOFOLLOW | libc::O_NONBLOCK);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;

        use windows_sys::Win32::{
            Foundation::GENERIC_READ,
            Storage::FileSystem::{FILE_FLAG_OPEN_REPARSE_POINT, READ_CONTROL},
        };
        options
            .access_mode(GENERIC_READ | READ_CONTROL)
            .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT);
    }
    options.open(path)
}

fn ensure_safe_state_metadata(metadata: &fs::Metadata) -> Result<()> {
    if !metadata.is_file() {
        return runtime_failure("Execution rate-limit state is not a safe regular file.");
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        if metadata.permissions().mode() & 0o077 != 0
            || metadata.nlink() != 1
            || !owned_by_effective_user(metadata)
        {
            return runtime_failure(
                "Execution rate-limit state permissions, links, or ownership are unsafe.",
            );
        }
    }
    Ok(())
}

#[cfg(unix)]
fn owned_by_effective_user(metadata: &fs::Metadata) -> bool {
    use std::os::unix::fs::MetadataExt;

    metadata.uid() == unsafe { libc::geteuid() }
}

#[cfg(unix)]
fn owned_by_root(metadata: &fs::Metadata) -> bool {
    use std::os::unix::fs::MetadataExt;

    metadata.uid() == 0
}

#[cfg(target_os = "macos")]
const MACOS_ACL_TYPE_EXTENDED: i32 = 0x100;
#[cfg(target_os = "macos")]
const MACOS_ACL_FIRST_ENTRY: i32 = 0;

#[cfg(target_os = "macos")]
type MacosAcl = *mut std::ffi::c_void;

#[cfg(target_os = "macos")]
unsafe extern "C" {
    fn acl_free(object: MacosAcl) -> i32;
    fn acl_get_entry(acl: MacosAcl, entry_id: i32, entry: *mut MacosAcl) -> i32;
    fn acl_get_file(path: *const libc::c_char, acl_type: i32) -> MacosAcl;
    fn acl_init(count: libc::c_int) -> MacosAcl;
    fn acl_set_file(path: *const libc::c_char, acl_type: i32, acl: MacosAcl) -> i32;
}

#[cfg(target_os = "macos")]
struct MacosAclGuard(MacosAcl);

#[cfg(target_os = "macos")]
impl Drop for MacosAclGuard {
    fn drop(&mut self) {
        unsafe {
            acl_free(self.0);
        }
    }
}

#[cfg(target_os = "macos")]
fn macos_acl_path(path: &Path) -> Result<std::ffi::CString> {
    use std::os::unix::ffi::OsStrExt;

    std::ffi::CString::new(path.as_os_str().as_bytes())
        .map_err(|_| Failure::new(Code::IoFailed, "Execution state path is invalid."))
}

#[cfg(target_os = "macos")]
fn clear_macos_acl(path: &Path) -> Result<()> {
    let Some(current) = macos_acl(path)? else {
        return Ok(());
    };
    if !macos_acl_has_entry(&current)? {
        return Ok(());
    }

    let path = macos_acl_path(path)?;
    let acl = unsafe { acl_init(0) };
    if acl.is_null() {
        return Err(Failure::io(&io::Error::last_os_error()));
    }
    let acl = MacosAclGuard(acl);
    if unsafe { acl_set_file(path.as_ptr(), MACOS_ACL_TYPE_EXTENDED, acl.0) } != 0 {
        return Err(Failure::io(&io::Error::last_os_error()));
    }
    Ok(())
}

#[cfg(target_os = "macos")]
fn ensure_private_macos_acl(path: &Path) -> Result<()> {
    let Some(acl) = macos_acl(path)? else {
        return Ok(());
    };
    if macos_acl_has_entry(&acl)? {
        return runtime_failure("Execution state access controls are unsafe.");
    }
    Ok(())
}

#[cfg(target_os = "macos")]
fn macos_acl(path: &Path) -> Result<Option<MacosAclGuard>> {
    let path = macos_acl_path(path)?;
    let acl = unsafe { acl_get_file(path.as_ptr(), MACOS_ACL_TYPE_EXTENDED) };
    if acl.is_null() {
        let error = io::Error::last_os_error();
        if error.raw_os_error() == Some(libc::ENOENT) {
            return Ok(None);
        }
        return Err(Failure::io(&error));
    }
    Ok(Some(MacosAclGuard(acl)))
}

#[cfg(target_os = "macos")]
fn macos_acl_has_entry(acl: &MacosAclGuard) -> Result<bool> {
    let mut entry = std::ptr::null_mut();
    match unsafe { acl_get_entry(acl.0, MACOS_ACL_FIRST_ENTRY, &mut entry) } {
        0 => Ok(true),
        1 => Ok(false),
        _ => Err(Failure::io(&io::Error::last_os_error())),
    }
}

#[cfg(windows)]
fn open_state_directory(path: &Path) -> Result<File> {
    use std::os::windows::fs::OpenOptionsExt;

    use windows_sys::Win32::Storage::FileSystem::{
        FILE_FLAG_BACKUP_SEMANTICS, FILE_FLAG_OPEN_REPARSE_POINT, FILE_READ_ATTRIBUTES,
        READ_CONTROL, WRITE_DAC,
    };

    OpenOptions::new()
        .access_mode(FILE_READ_ATTRIBUTES | READ_CONTROL | WRITE_DAC)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OPEN_REPARSE_POINT)
        .open(path)
        .map_err(|error| Failure::io(&error))
}

#[cfg(windows)]
fn ensure_windows_state_object(file: &File, directory: bool) -> Result<()> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::Storage::FileSystem::{
        GetFileInformationByHandle, BY_HANDLE_FILE_INFORMATION, FILE_ATTRIBUTE_REPARSE_POINT,
    };

    let mut information = std::mem::MaybeUninit::<BY_HANDLE_FILE_INFORMATION>::uninit();
    if unsafe { GetFileInformationByHandle(file.as_raw_handle(), information.as_mut_ptr()) } == 0 {
        return runtime_failure("Execution state object metadata could not be verified.");
    }
    let information = unsafe { information.assume_init() };
    if information.dwFileAttributes & FILE_ATTRIBUTE_REPARSE_POINT != 0
        || (!directory && information.nNumberOfLinks != 1)
    {
        return runtime_failure("Execution state object links are unsafe.");
    }
    Ok(())
}

#[cfg(windows)]
fn restrict_windows_state_dacl(file: &File, directory: bool) -> Result<()> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::ERROR_SUCCESS,
        Security::{
            AddAccessAllowedAceEx,
            Authorization::{SetSecurityInfo, SE_FILE_OBJECT},
            GetLengthSid, InitializeAcl, ACL, ACL_REVISION, CONTAINER_INHERIT_ACE,
            DACL_SECURITY_INFORMATION, OBJECT_INHERIT_ACE, PROTECTED_DACL_SECURITY_INFORMATION,
        },
        Storage::FileSystem::FILE_ALL_ACCESS,
    };

    let user_token = current_user_token()?;
    let user = token_user_sid(&user_token)?;
    let default_owner_token = current_default_owner_token()?;
    let default_owner = token_owner_sid(&default_owner_token)?;
    // Refuse to alter an object owned by another account even if a permissive
    // parent DACL temporarily granted this process WRITE_DAC access.
    ensure_windows_state_owner(file, user, default_owner)?;
    let size = std::mem::size_of::<ACL>()
        + std::mem::size_of::<windows_sys::Win32::Security::ACCESS_ALLOWED_ACE>()
        - std::mem::size_of::<u32>()
        + unsafe { GetLengthSid(user) as usize };
    let mut bytes = vec![0u8; size];
    let acl = bytes.as_mut_ptr().cast::<ACL>();
    let inheritance = if directory {
        CONTAINER_INHERIT_ACE | OBJECT_INHERIT_ACE
    } else {
        0
    };
    if unsafe { InitializeAcl(acl, size as u32, ACL_REVISION) } == 0
        || unsafe { AddAccessAllowedAceEx(acl, ACL_REVISION, inheritance, FILE_ALL_ACCESS, user) }
            == 0
    {
        return runtime_failure("Execution state access controls could not be initialized.");
    }
    if unsafe {
        SetSecurityInfo(
            file.as_raw_handle(),
            SE_FILE_OBJECT,
            DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            acl,
            std::ptr::null(),
        )
    } != ERROR_SUCCESS
    {
        return runtime_failure("Execution state access controls could not be restricted.");
    }
    Ok(())
}

#[cfg(windows)]
fn ensure_windows_state_owner(
    file: &File,
    user: windows_sys::Win32::Security::PSID,
    default_owner: windows_sys::Win32::Security::PSID,
) -> Result<()> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::{LocalFree, ERROR_SUCCESS},
        Security::{
            Authorization::{GetSecurityInfo, SE_FILE_OBJECT},
            OWNER_SECURITY_INFORMATION,
        },
    };

    let mut owner = std::ptr::null_mut();
    let mut descriptor = std::ptr::null_mut();
    if unsafe {
        GetSecurityInfo(
            file.as_raw_handle(),
            SE_FILE_OBJECT,
            OWNER_SECURITY_INFORMATION,
            &mut owner,
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            std::ptr::null_mut(),
            &mut descriptor,
        )
    } != ERROR_SUCCESS
    {
        return runtime_failure("Execution state ownership could not be verified.");
    }
    let owned_by_current_identity = owner_belongs_to_current_identity(owner, user, default_owner);
    unsafe {
        LocalFree(descriptor);
    }
    if !owned_by_current_identity {
        return runtime_failure("Execution state ownership is unsafe.");
    }
    Ok(())
}

#[cfg(windows)]
fn ensure_windows_private_dacl(file: &File) -> Result<()> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::{LocalFree, ERROR_SUCCESS},
        Security::{
            Authorization::{GetSecurityInfo, SE_FILE_OBJECT},
            EqualSid, GetAce, GetAclInformation, GetSecurityDescriptorControl, ACCESS_ALLOWED_ACE,
            ACL_SIZE_INFORMATION, DACL_SECURITY_INFORMATION, OWNER_SECURITY_INFORMATION,
            SE_DACL_PROTECTED,
        },
        Storage::FileSystem::FILE_ALL_ACCESS,
        System::SystemServices::ACCESS_ALLOWED_ACE_TYPE,
    };

    let user_token = current_user_token()?;
    let user = token_user_sid(&user_token)?;
    let default_owner_token = current_default_owner_token()?;
    let default_owner = token_owner_sid(&default_owner_token)?;
    let mut owner = std::ptr::null_mut();
    let mut dacl = std::ptr::null_mut();
    let mut descriptor = std::ptr::null_mut();
    let status = unsafe {
        GetSecurityInfo(
            file.as_raw_handle(),
            SE_FILE_OBJECT,
            OWNER_SECURITY_INFORMATION | DACL_SECURITY_INFORMATION,
            &mut owner,
            std::ptr::null_mut(),
            &mut dacl,
            std::ptr::null_mut(),
            &mut descriptor,
        )
    };
    if status != ERROR_SUCCESS {
        return runtime_failure("Execution state ownership could not be verified.");
    }
    let result = (|| {
        let mut control = 0;
        let mut revision = 0;
        if dacl.is_null()
            || unsafe { GetSecurityDescriptorControl(descriptor, &mut control, &mut revision) } == 0
            || control & SE_DACL_PROTECTED == 0
            || !owner_belongs_to_current_identity(owner, user, default_owner)
        {
            return runtime_failure("Execution state ownership is unsafe.");
        }
        let mut information = ACL_SIZE_INFORMATION::default();
        if unsafe {
            GetAclInformation(
                dacl,
                (&mut information as *mut ACL_SIZE_INFORMATION).cast(),
                std::mem::size_of_val(&information) as u32,
                windows_sys::Win32::Security::AclSizeInformation,
            )
        } == 0
            || information.AceCount != 1
        {
            return runtime_failure("Execution state access controls are unsafe.");
        }
        let mut raw_ace = std::ptr::null_mut();
        if unsafe { GetAce(dacl, 0, &mut raw_ace) } == 0 || raw_ace.is_null() {
            return runtime_failure("Execution state access controls are unsafe.");
        }
        let ace = unsafe { &*raw_ace.cast::<ACCESS_ALLOWED_ACE>() };
        let sid = (&ace.SidStart as *const u32).cast_mut().cast();
        if ace.Header.AceType as u32 != ACCESS_ALLOWED_ACE_TYPE
            || ace.Mask != FILE_ALL_ACCESS
            || unsafe { EqualSid(sid, user) } == 0
        {
            return runtime_failure("Execution state access controls are unsafe.");
        }
        Ok(())
    })();
    unsafe {
        LocalFree(descriptor);
    }
    result
}

#[cfg(windows)]
fn current_user_token() -> Result<Vec<u8>> {
    use windows_sys::Win32::Security::{TokenUser, TOKEN_USER};

    current_token_information(TokenUser, std::mem::size_of::<TOKEN_USER>())
}

#[cfg(windows)]
fn current_default_owner_token() -> Result<Vec<u8>> {
    use windows_sys::Win32::Security::{TokenOwner, TOKEN_OWNER};

    current_token_information(TokenOwner, std::mem::size_of::<TOKEN_OWNER>())
}

#[cfg(windows)]
fn current_token_information(
    information_class: windows_sys::Win32::Security::TOKEN_INFORMATION_CLASS,
    minimum_length: usize,
) -> Result<Vec<u8>> {
    use windows_sys::Win32::{
        Foundation::CloseHandle,
        Security::{GetTokenInformation, TOKEN_QUERY},
        System::Threading::{GetCurrentProcess, OpenProcessToken},
    };

    let mut handle = std::ptr::null_mut();
    if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut handle) } == 0 {
        return runtime_failure("Current-user ownership could not be verified.");
    }
    let result = (|| {
        let mut length = 0;
        unsafe {
            GetTokenInformation(
                handle,
                information_class,
                std::ptr::null_mut(),
                0,
                &mut length,
            );
        }
        if length < minimum_length as u32 {
            return runtime_failure("Current-user ownership could not be verified.");
        }
        let mut bytes = vec![0u8; length as usize];
        if unsafe {
            GetTokenInformation(
                handle,
                information_class,
                bytes.as_mut_ptr().cast(),
                length,
                &mut length,
            )
        } == 0
        {
            return runtime_failure("Current-user ownership could not be verified.");
        }
        Ok(bytes)
    })();
    unsafe {
        CloseHandle(handle);
    }
    result
}

#[cfg(windows)]
fn token_user_sid(token: &[u8]) -> Result<windows_sys::Win32::Security::PSID> {
    use windows_sys::Win32::Security::TOKEN_USER;

    let user = unsafe { std::ptr::read_unaligned(token.as_ptr().cast::<TOKEN_USER>()) };
    if user.User.Sid.is_null() {
        return runtime_failure("Current-user ownership could not be verified.");
    }
    Ok(user.User.Sid)
}

#[cfg(windows)]
fn token_owner_sid(token: &[u8]) -> Result<windows_sys::Win32::Security::PSID> {
    use windows_sys::Win32::Security::TOKEN_OWNER;

    let owner = unsafe { std::ptr::read_unaligned(token.as_ptr().cast::<TOKEN_OWNER>()) };
    if owner.Owner.is_null() {
        return runtime_failure("Current-user ownership could not be verified.");
    }
    Ok(owner.Owner)
}

#[cfg(windows)]
fn owner_belongs_to_current_identity(
    owner: windows_sys::Win32::Security::PSID,
    user: windows_sys::Win32::Security::PSID,
    default_owner: windows_sys::Win32::Security::PSID,
) -> bool {
    use windows_sys::Win32::Security::EqualSid;

    if owner.is_null() {
        return false;
    }
    // Elevated Windows tokens can create objects owned by TokenOwner (for
    // example the Administrators group) rather than TokenUser. Accept only
    // that token-declared alternative; the DACL remains an exact single ACE
    // for TokenUser, so no inherited group access is retained.
    unsafe { EqualSid(owner, user) != 0 || EqualSid(owner, default_owner) != 0 }
}

fn retryable_status(status: &ExitStatus, selected: &std::collections::BTreeSet<i32>) -> bool {
    #[cfg(unix)]
    {
        use std::os::unix::process::ExitStatusExt;
        if status.signal().is_some() {
            return false;
        }
    }
    status
        .code()
        .filter(|code| *code != 0)
        .is_some_and(|code| selected.is_empty() || selected.contains(&code))
}

fn retry_delay(
    initial: Duration,
    maximum: Duration,
    factor: u32,
    attempt: u32,
    jitter: Jitter,
) -> Duration {
    let mut millis = initial.as_millis();
    let maximum = maximum.as_millis();
    for _ in 1..attempt {
        if millis >= maximum || factor == 1 {
            break;
        }
        millis = millis.saturating_mul(factor as u128).min(maximum);
    }
    let millis = u64::try_from(millis).unwrap_or(u64::MAX);
    match jitter {
        Jitter::None => Duration::from_millis(millis),
        Jitter::Full => Duration::from_millis(rand::rng().random_range(0..=millis)),
    }
}

fn deadline(start: Instant, duration: Option<Duration>) -> Result<Option<Instant>> {
    duration
        .filter(|duration| !duration.is_zero())
        .map(|duration| {
            start
                .checked_add(duration)
                .ok_or_else(|| Failure::new(Code::InvalidInput, "Duration is too large."))
        })
        .transpose()
}

/// Admission `0` means an immediate decision: it disables no execution
/// timeout, because it applies only while waiting for a lock or token.
fn admission_deadline(start: Instant, duration: Option<Duration>) -> Result<Option<Instant>> {
    duration
        .map(|duration| {
            start
                .checked_add(duration)
                .ok_or_else(|| Failure::new(Code::InvalidInput, "Duration is too large."))
        })
        .transpose()
}

fn admission_deadline_expired(deadline: Option<Instant>, attempted: bool) -> bool {
    attempted && deadline.is_some_and(|limit| Instant::now() >= limit)
}

fn clip_to_deadline(duration: Duration, deadline: Option<Instant>) -> Duration {
    deadline
        .map(|limit| duration.min(limit.saturating_duration_since(Instant::now())))
        .unwrap_or(duration)
}

fn sleep_cancellable(duration: Duration) -> Result<()> {
    let end = Instant::now()
        .checked_add(duration)
        .ok_or_else(|| Failure::new(Code::InvalidInput, "Duration is too large."))?;
    while Instant::now() < end {
        check_cancelled()?;
        thread::sleep(POLL.min(end.saturating_duration_since(Instant::now())));
    }
    Ok(())
}

enum ManagedServiceWait {
    Elapsed,
    Exited,
}

fn wait_for_managed_service(
    service: &mut OwnedChild,
    duration: Duration,
) -> Result<ManagedServiceWait> {
    let outcome = wait_for_service_completion(duration, || {
        ensure_managed_service_output(service.output_failed())?;
        Ok(service.completion()?.is_some())
    })?;
    if matches!(outcome, ManagedServiceWait::Exited) {
        tracing::debug!(
            operation = "run-with-service",
            stage = "readiness_service_exited",
            "run_readiness"
        );
    }
    Ok(outcome)
}

fn ensure_managed_service_output(forwarding_failed: bool) -> Result<()> {
    if forwarding_failed {
        return runtime_failure("Could not forward managed service output.");
    }
    Ok(())
}

fn wait_for_service_completion(
    duration: Duration,
    mut completed: impl FnMut() -> Result<bool>,
) -> Result<ManagedServiceWait> {
    let end = Instant::now()
        .checked_add(duration)
        .ok_or_else(|| Failure::new(Code::InvalidInput, "Duration is too large."))?;
    loop {
        check_cancelled()?;
        if completed()? {
            return Ok(ManagedServiceWait::Exited);
        }
        let remaining = end.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Ok(ManagedServiceWait::Elapsed);
        }
        thread::sleep(POLL.min(remaining));
    }
}

fn check_cancelled() -> Result<()> {
    runtime::check_cancelled()
}

fn unix_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .ok()
        .and_then(|duration| i64::try_from(duration.as_millis()).ok())
        .unwrap_or(0)
}

fn runtime_failure<T>(message: &'static str) -> Result<T> {
    Err(Failure::new(Code::IoFailed, message))
}

fn invalid<T>(message: &'static str) -> Result<T> {
    Err(Failure::new(Code::InvalidInput, message))
}

fn parse_name(value: &str) -> std::result::Result<String, &'static str> {
    if (1..=128).contains(&value.len())
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        Ok(value.to_owned())
    } else {
        Err("Use an ASCII name of 1 to 128 letters, digits, dots, underscores, or hyphens.")
    }
}

fn parse_duration(value: &str, allow_zero: bool) -> std::result::Result<Duration, &'static str> {
    if value == "0" && allow_zero {
        return Ok(Duration::ZERO);
    }
    let (digits, multiplier) = [("ms", 1u64), ("s", 1_000), ("m", 60_000), ("h", 3_600_000)]
        .into_iter()
        .find_map(|(suffix, multiplier)| {
            value
                .strip_suffix(suffix)
                .map(|digits| (digits, multiplier))
        })
        .ok_or("Use a nonnegative integer duration with ms, s, m, or h.")?;
    if digits.is_empty() || !digits.bytes().all(|byte| byte.is_ascii_digit()) {
        return Err("Use a nonnegative integer duration.");
    }
    let millis = digits
        .parse::<u64>()
        .ok()
        .and_then(|number| number.checked_mul(multiplier))
        .ok_or("Duration is too large.")?;
    if millis == 0 && !allow_zero {
        return Err("This duration must be positive.");
    }
    let duration = Duration::from_millis(millis);
    if Instant::now().checked_add(duration).is_none() {
        return Err("Duration is too large.");
    }
    Ok(duration)
}

fn parse_positive_duration(value: &str) -> std::result::Result<Duration, &'static str> {
    parse_duration(value, false)
}

fn parse_optional_duration(value: &str) -> std::result::Result<Duration, &'static str> {
    parse_duration(value, true)
}

fn parse_positive_u64(value: &str) -> std::result::Result<u64, &'static str> {
    value
        .parse::<u64>()
        .ok()
        .filter(|value| *value > 0)
        .ok_or("Use a positive integer.")
}

fn parse_token_count(value: &str) -> std::result::Result<u64, &'static str> {
    parse_positive_u64(value).and_then(|value| {
        (value <= MAX_EXACT_TOKEN_COUNT)
            .then_some(value)
            .ok_or("Use a token burst no greater than 9007199254740992.")
    })
}

fn parse_positive_u32(value: &str) -> std::result::Result<u32, &'static str> {
    value
        .parse::<u32>()
        .ok()
        .filter(|value| *value > 0)
        .ok_or("Use a positive integer.")
}

fn parse_exit_code(value: &str) -> std::result::Result<i32, &'static str> {
    value
        .parse::<i32>()
        .ok()
        .filter(|value| *value > 0)
        .ok_or("Use a positive numeric child exit code.")
}

fn parse_status(value: &str) -> std::result::Result<u16, &'static str> {
    value
        .parse::<u16>()
        .ok()
        .filter(|value| (200..=599).contains(value))
        .ok_or("Select an exact HTTP status from 200 to 599.")
}

fn parse_http_url(value: &str) -> std::result::Result<reqwest::Url, &'static str> {
    const ERROR: &str = "Use an absolute http or https URL without user information and with a \
                         valid host and port.";
    let (scheme, rest) = value.split_once("://").ok_or(ERROR)?;
    if !scheme.eq_ignore_ascii_case("http") && !scheme.eq_ignore_ascii_case("https") {
        return Err(ERROR);
    }
    let authority = rest.split(['/', '?', '#']).next().ok_or(ERROR)?;
    if authority.is_empty()
        || authority.contains('@')
        || value.contains('\\')
        || value.chars().any(char::is_whitespace)
        || value.chars().any(char::is_control)
        || authority.ends_with(':')
    {
        return Err(ERROR);
    }
    let url = reqwest::Url::parse(value).map_err(|_| ERROR)?;
    if url.host().is_none()
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port_or_known_default() == Some(0)
    {
        return Err(ERROR);
    }
    Ok(url)
}

#[cfg(all(test, unix))]
mod rate_limit_tests {
    use std::{
        ffi::CString,
        os::unix::{ffi::OsStrExt, fs::OpenOptionsExt},
    };

    use fs4::FileExt;

    use super::*;

    #[test]
    fn negative_refill_baseline_fails_closed() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("bucket.json");
        let bucket = Bucket {
            version: STATE_VERSION,
            limit: 1,
            period_ms: 1_000,
            burst: 1,
            tokens: 0.0,
            refill_utc_ms: -1,
        };
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o600)
            .open(&path)
            .unwrap();
        file.write_all(&serde_json::to_vec(&bucket).unwrap())
            .unwrap();
        drop(file);

        let error = match read_bucket(&path, 1, Duration::from_secs(1), 1, 0) {
            Ok(_) => panic!("overflowing state must be rejected"),
            Err(error) => error,
        };

        assert_eq!(error.code, Code::IoFailed);
    }

    #[test]
    fn oversized_bucket_state_fails_before_deserialization() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("bucket.json");
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o600)
            .open(&path)
            .unwrap();
        file.write_all(&vec![b'x'; (MAX_BUCKET_STATE_BYTES + 1) as usize])
            .unwrap();
        drop(file);

        let error = match read_bucket(&path, 1, Duration::from_secs(1), 1, 0) {
            Ok(_) => panic!("oversized state must be rejected"),
            Err(error) => error,
        };

        assert_eq!(error.code, Code::IoFailed);
    }

    #[test]
    fn unknown_bucket_fields_fail_closed() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("bucket.json");
        let mut file = OpenOptions::new()
            .create_new(true)
            .write(true)
            .mode(0o600)
            .open(&path)
            .unwrap();
        file.write_all(
            br#"{"version":1,"limit":1,"period_ms":1000,"burst":1,"tokens":1.0,"refill_utc_ms":0,"unexpected":true}"#,
        )
        .unwrap();
        drop(file);

        let error = match read_bucket(&path, 1, Duration::from_secs(1), 1, 0) {
            Ok(_) => panic!("state with unknown fields must be rejected"),
            Err(error) => error,
        };

        assert_eq!(error.code, Code::IoFailed);
    }

    #[test]
    fn fifo_bucket_state_fails_closed_without_waiting_for_a_writer() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("bucket.fifo");
        let path_c = CString::new(path.as_os_str().as_bytes()).unwrap();
        assert_eq!(unsafe { libc::mkfifo(path_c.as_ptr(), 0o600) }, 0);

        let error = match read_bucket(&path, 1, Duration::from_secs(1), 1, 0) {
            Ok(_) => panic!("a FIFO state path must be rejected"),
            Err(error) => error,
        };

        assert_eq!(error.code, Code::IoFailed);
    }

    #[test]
    fn admission_deadline_precedes_a_later_lock_retry() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("admission.lock");
        let holder = open_lock(&path).unwrap();
        FileExt::lock(&holder).unwrap();
        let release = thread::spawn(move || {
            thread::sleep(Duration::from_millis(10));
            drop(holder);
        });

        let deadline = Instant::now() + Duration::from_millis(1);
        let error = match acquire(&path, Some(deadline)) {
            Ok(_) => panic!("a lock released after the deadline must not be acquired"),
            Err(error) => error,
        };

        release.join().unwrap();
        assert_eq!(error.code, Code::TerminationTimeout);
    }

    #[test]
    fn linux_machine_identity_accepts_dbus_and_boot_fallbacks() {
        let directory = tempfile::tempdir().unwrap();
        let dbus = directory.path().join("dbus-machine-id");
        let boot = directory.path().join("boot-id");
        fs::write(&dbus, b"0123456789abcdef0123456789abcdef\n").unwrap();
        fs::write(&boot, b"01234567-89ab-cdef-0123-456789abcdef\n").unwrap();

        assert_eq!(
            read_linux_identity(&dbus, valid_machine_id).unwrap(),
            Some(b"0123456789abcdef0123456789abcdef".to_vec())
        );
        assert_eq!(
            read_linux_identity(&boot, valid_uuid).unwrap(),
            Some(b"01234567-89ab-cdef-0123-456789abcdef".to_vec())
        );
    }

    #[test]
    fn linux_machine_identity_rejects_the_all_zero_sentinel() {
        assert!(!valid_machine_id("00000000000000000000000000000000"));
    }

    #[test]
    fn machine_guid_requires_a_nonzero_structural_uuid() {
        assert!(valid_uuid("01234567-89ab-cdef-0123-456789abcdef"));
        assert!(!valid_uuid("00000000-0000-0000-0000-000000000000"));
        assert!(!valid_uuid("{01234567-89ab-cdef-0123-456789abcdef}"));
        assert!(!valid_uuid("-"));
    }
}

#[cfg(test)]
mod lifecycle_tests {
    use super::*;

    #[test]
    fn permission_denied_connect_errors_are_terminal() {
        let denied = io::Error::from(io::ErrorKind::PermissionDenied);
        let refused = io::Error::from(io::ErrorKind::ConnectionRefused);

        assert!(contains_permission_denied(&denied));
        assert!(!contains_permission_denied(&refused));
    }

    #[cfg(unix)]
    #[test]
    fn local_resource_connect_errors_are_terminal() {
        let exhausted = io::Error::from_raw_os_error(libc::EMFILE);
        let refused = io::Error::from(io::ErrorKind::ConnectionRefused);

        assert!(contains_local_resource_failure(&exhausted));
        assert!(!contains_local_resource_failure(&refused));
    }

    #[test]
    fn readiness_rejects_a_success_observed_after_its_deadline() {
        let start = Instant::now();
        let attempt_deadline = start.checked_add(Duration::from_millis(10)).unwrap();
        let observed_at = attempt_deadline
            .checked_add(Duration::from_millis(1))
            .unwrap();

        assert!(matches!(
            bounded_probe_result(
                HttpProbe {
                    result: Ok(()),
                    observed_at,
                },
                attempt_deadline,
                Some(attempt_deadline),
            ),
            Err(HttpProbeError::OverallTimeout)
        ));
    }

    #[test]
    fn readiness_rejects_an_attempt_observed_after_its_budget() {
        let start = Instant::now();
        let attempt_deadline = start.checked_add(Duration::from_millis(10)).unwrap();
        let observed_at = attempt_deadline
            .checked_add(Duration::from_millis(1))
            .unwrap();
        let overall_deadline = observed_at.checked_add(Duration::from_millis(10)).unwrap();

        assert!(matches!(
            bounded_probe_result(
                HttpProbe {
                    result: Ok(()),
                    observed_at,
                },
                attempt_deadline,
                Some(overall_deadline),
            ),
            Err(HttpProbeError::AttemptTimeout)
        ));
    }

    #[test]
    fn readiness_uses_a_queued_success_observed_before_its_deadline() {
        let observed_at = Instant::now();
        let deadline = observed_at.checked_add(Duration::from_millis(1)).unwrap();
        let (sender, receiver) = mpsc::sync_channel(1);
        sender
            .send(HttpProbe {
                result: Ok(()),
                observed_at,
            })
            .unwrap();

        thread::sleep(Duration::from_millis(2));

        let mut pending = Some(PendingHttpProbe {
            receiver,
            attempt_deadline: deadline,
        });

        assert!(matches!(
            queued_probe_result(&mut pending, Some(deadline)),
            Some(Ok(()))
        ));
        assert!(pending.is_none());
    }

    #[test]
    fn readiness_retains_an_unfinished_probe_for_the_next_poll() {
        let deadline = Instant::now().checked_add(Duration::from_secs(1)).unwrap();
        let (sender, receiver) = mpsc::sync_channel(1);
        let mut pending = Some(PendingHttpProbe {
            receiver,
            attempt_deadline: deadline,
        });

        assert!(queued_probe_result(&mut pending, None).is_none());
        assert!(pending.is_some());

        sender
            .send(HttpProbe {
                result: Err(HttpProbeError::NotReady),
                observed_at: Instant::now(),
            })
            .unwrap();
        assert!(matches!(
            queued_probe_result(&mut pending, None),
            Some(Err(HttpProbeError::NotReady))
        ));
        assert!(pending.is_none());
    }

    #[test]
    fn managed_service_wait_rechecks_completion_before_its_delay_elapses() {
        let mut checks = 0;

        let outcome = wait_for_service_completion(Duration::from_secs(1), || {
            checks += 1;
            Ok(checks == 2)
        })
        .unwrap();

        assert!(matches!(outcome, ManagedServiceWait::Exited));
        assert_eq!(checks, 2);
    }

    #[test]
    fn managed_service_wait_stops_before_polling_when_forwarding_fails() {
        let mut completion_checks = 0;

        let error = match wait_for_service_completion(Duration::from_secs(1), || {
            ensure_managed_service_output(true)?;
            completion_checks += 1;
            Ok(false)
        }) {
            Ok(_) => panic!("a forwarding failure must stop readiness polling"),
            Err(error) => error,
        };

        assert_eq!(error.code, Code::IoFailed);
        assert_eq!(completion_checks, 0);
    }

    #[cfg(unix)]
    #[test]
    fn observes_an_exited_child_without_consuming_its_status() {
        let mut child = ProcessCommand::new("sh")
            .args(["-c", "exit 0"])
            .spawn()
            .unwrap();
        let observed = (0..50).any(|_| {
            if child_exit_observed_without_reaping(child.id()).unwrap() {
                true
            } else {
                thread::sleep(Duration::from_millis(1));
                false
            }
        });
        if !observed {
            let _ = child.kill();
            let _ = child.wait();
            panic!("did not observe child exit without reaping it");
        }

        assert!(child.wait().unwrap().success());
    }

    #[test]
    fn native_trust_initialization_stops_waiting_at_the_readiness_deadline() {
        let (release, blocked_loader) = mpsc::sync_channel(0);
        let deadline = Instant::now()
            .checked_add(Duration::from_millis(1))
            .unwrap();

        let error = wait_for_readiness_initialization(Some(deadline), move || {
            let _ = blocked_loader.recv();
        })
        .unwrap_err();

        assert_eq!(error.code, Code::TerminationTimeout);
        release.send(()).unwrap();
    }

    #[test]
    fn forced_cleanup_skips_the_grace_window() {
        let kill_after = Duration::from_secs(5);

        assert_eq!(graceful_cleanup_wait(true, kill_after), kill_after);
        assert_eq!(graceful_cleanup_wait(false, kill_after), Duration::ZERO);
    }

    #[cfg(unix)]
    #[test]
    fn completion_failure_still_terminates_the_owned_direct_child() {
        let mut process = ProcessCommand::new("sh")
            .args(["-c", "trap 'exit 0' TERM; while :; do :; done"])
            .spawn()
            .unwrap();
        let pid = process.id();
        let (sender, completions) = mpsc::sync_channel(1);
        let mut child = OwnedChild {
            pid,
            unix_ownership: UnixOwnership::DirectChild,
            reaped: false,
            foreground_terminal: None,
            completions,
            completion: None,
            completion_observation_failed: false,
            activity: None,
            output_failure: None,
            output_threads: Vec::new(),
        };
        sender
            .send(runtime_failure("The completion worker failed."))
            .unwrap();

        let error = match child.completion() {
            Ok(_) => panic!("the injected completion failure must be observed"),
            Err(error) => error,
        };
        assert_eq!(error.code, Code::IoFailed);
        assert!(child.cleanup(Duration::from_millis(20), None).is_ok());
        assert_eq!(unsafe { libc::kill(pid as libc::pid_t, 0) }, -1);
        assert_eq!(io::Error::last_os_error().raw_os_error(), Some(libc::ESRCH));
        let _ = process.wait();
    }

    #[test]
    fn retry_delay_stops_when_the_cap_cannot_change() {
        assert_eq!(
            retry_delay(
                Duration::from_millis(1),
                Duration::from_millis(1),
                1,
                u32::MAX,
                Jitter::None,
            ),
            Duration::from_millis(1),
        );
    }

    #[test]
    fn completion_observed_after_a_limit_is_a_timeout() {
        let start = Instant::now();
        let deadline = start.checked_add(Duration::from_millis(1)).unwrap();
        let completion = deadline.checked_add(Duration::from_millis(1)).unwrap();

        assert!(limits_expired_at(
            &Limits {
                overall: Some(deadline),
                idle: None,
            },
            start,
            completion,
        ));
        assert!(limits_expired_at(
            &Limits {
                overall: None,
                idle: Some(Duration::from_millis(1)),
            },
            start,
            completion,
        ));
    }

    #[test]
    fn spawn_boundary_rejects_an_expired_deadline() {
        let deadline = Instant::now()
            .checked_sub(Duration::from_millis(1))
            .unwrap();

        let error = check_spawn_boundary(Some(deadline)).unwrap_err();

        assert_eq!(error.code, Code::TerminationTimeout);
    }

    #[test]
    fn activity_retains_the_latest_read_before_supervision() {
        let start = Instant::now();
        let first = start.checked_add(Duration::from_millis(1)).unwrap();
        let latest = start.checked_add(Duration::from_millis(10)).unwrap();
        let activity = Activity::at(start);

        activity.observe(first);
        activity.observe(latest);

        assert_eq!(activity.last_observed_at(), latest);
    }

    #[test]
    fn completion_uses_output_activity_refreshed_at_its_decision_boundary() {
        let start = Instant::now();
        let completion = start.checked_add(Duration::from_millis(10)).unwrap();
        let final_read = start.checked_add(Duration::from_millis(9)).unwrap();
        let activity = Activity::at(start);
        activity.observe(final_read);
        let limits = Limits {
            overall: None,
            idle: Some(Duration::from_millis(5)),
        };

        assert!(limits_expired_at(&limits, start, completion));
        assert!(!limits_expired_at(
            &limits,
            activity.last_observed_at(),
            completion,
        ));
    }

    #[test]
    fn supervision_does_not_allow_late_output_to_clear_an_expired_idle_limit() {
        let start = Instant::now();
        let idle = Duration::from_millis(5);
        let deadline = start.checked_add(idle).unwrap();
        let observed_at = deadline.checked_add(Duration::from_millis(1)).unwrap();
        let early_activity = deadline.checked_sub(Duration::from_millis(1)).unwrap();
        let late_activity = deadline.checked_add(Duration::from_millis(1)).unwrap();
        let limits = Limits {
            overall: None,
            idle: Some(idle),
        };

        assert!(!limits_expired_before_activity_refresh(
            &limits,
            start,
            early_activity,
            observed_at,
        ));
        assert!(limits_expired_before_activity_refresh(
            &limits,
            start,
            late_activity,
            observed_at,
        ));
    }

    #[test]
    fn supervision_wakes_at_the_earliest_idle_deadline() {
        let start = Instant::now();
        let idle = Duration::from_millis(5);
        let limits = Limits {
            overall: None,
            idle: Some(idle),
        };

        assert_eq!(supervision_poll_interval(&limits, start, start), idle);
    }

    #[test]
    fn output_join_uses_one_fixed_default_deadline() {
        let start = Instant::now();
        let fallback_deadline = start.checked_add(CLEANUP_CONFIRMATION).unwrap();

        assert_eq!(
            output_deadline(&Limits::default(), None, fallback_deadline),
            fallback_deadline
        );
    }
}

#[cfg(test)]
mod state_key_tests {
    use super::*;

    #[test]
    fn bucket_write_ignores_a_stale_pid_temporary_file() {
        let temporary = tempfile::tempdir().unwrap();
        let bucket_path = temporary.path().join("admission.json");
        let stale = temporary
            .path()
            .join(format!(".admission.json.{}.tmp", std::process::id()));
        fs::write(&stale, b"interrupted write").unwrap();

        write_bucket(
            &bucket_path,
            &Bucket {
                version: STATE_VERSION,
                limit: 1,
                period_ms: 1,
                burst: 1,
                tokens: 1.0,
                refill_utc_ms: 0,
            },
        )
        .unwrap();

        assert!(bucket_path.is_file());
        assert!(stale.is_file());
    }

    #[cfg(unix)]
    #[test]
    fn state_directory_rejects_a_writable_ancestor() {
        use std::os::unix::fs::PermissionsExt;

        let temporary = tempfile::tempdir().unwrap();
        let unsafe_ancestor = temporary.path().join("unsafe");
        fs::create_dir(&unsafe_ancestor).unwrap();
        fs::set_permissions(&unsafe_ancestor, fs::Permissions::from_mode(0o777)).unwrap();

        let error = ensure_private_dir(&unsafe_ancestor.join("state")).unwrap_err();
        assert_eq!(error.code, Code::IoFailed);
    }

    #[cfg(unix)]
    #[test]
    fn state_directory_rejects_any_foreign_owner_ancestor() {
        assert!(!state_ancestor_ownership_is_safe(501, 502));
        assert!(state_ancestor_ownership_is_safe(0, 502));
        assert!(state_ancestor_ownership_is_safe(502, 502));
    }

    #[cfg(unix)]
    #[test]
    fn state_directory_accepts_a_trusted_symlinked_ancestor() {
        use std::os::unix::fs::{symlink, PermissionsExt};

        let temporary = tempfile::tempdir().unwrap();
        let target = temporary.path().join("target");
        fs::create_dir(&target).unwrap();
        fs::set_permissions(&target, fs::Permissions::from_mode(0o700)).unwrap();
        let link = temporary.path().join("state-home");
        symlink(&target, &link).unwrap();

        ensure_private_dir(&link.join("state")).unwrap();
        assert!(target.join("state").is_dir());
    }

    #[test]
    fn machine_identity_namespaces_coordination_keys() {
        let first = hash_key(
            "lock",
            Scope::Project,
            b"machine-one",
            b"project",
            "migration",
        );
        let second = hash_key(
            "lock",
            Scope::Project,
            b"machine-two",
            b"project",
            "migration",
        );

        assert_ne!(first, second);
    }
}

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
        mpsc, Arc,
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
    use super::*;

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
    let client = service_http_client(options.url.scheme() == "https")?;
    let ready_deadline = deadline(start, Some(options.ready_timeout))?;

    // The bounded preflight deliberately happens before spawning either child.
    match check_http(&options, &client, ready_deadline) {
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
            return wait_for_external_service(&options, &client, &workload, ready_deadline);
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
    let service_plan = service.expect("service is checked above");
    let mut service_child = spawn(&service_plan, OutputMode::Service)?;
    loop {
        if service_child.output_failed() {
            let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
            return runtime_failure("Could not forward managed service output.");
        }
        if runtime::cancelled() {
            let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
            return check_cancelled().and_then(|()| unreachable!());
        }
        if completion_or_cleanup(&mut service_child, options.workload.kill_after)?.is_some() {
            let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
            return runtime_failure("The managed service exited before it became ready.");
        }
        match check_http(&options, &client, ready_deadline) {
            Ok(()) => break,
            Err(code) if code.retryable() => {
                // The service can exit while one bounded HTTP attempt is in
                // progress. Prefer that owned-process failure to reporting a
                // coincident readiness deadline.
                if completion_or_cleanup(&mut service_child, options.workload.kill_after)?.is_some()
                {
                    let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
                    return runtime_failure("The managed service exited before it became ready.");
                }
                if ready_deadline.is_some_and(|deadline| Instant::now() >= deadline) {
                    let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
                    return Ok(Outcome::Code(124));
                }
                if let Err(error) =
                    sleep_cancellable(clip_to_deadline(options.interval, ready_deadline))
                {
                    let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
                    return Err(error);
                }
            }
            Err(HttpProbeError::OverallTimeout) => {
                if completion_or_cleanup(&mut service_child, options.workload.kill_after)?.is_some()
                {
                    let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
                    return runtime_failure("The managed service exited before it became ready.");
                }
                let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
                return Ok(Outcome::Code(124));
            }
            Err(HttpProbeError::Cancelled) => {
                let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
                return check_cancelled().and_then(|()| unreachable!());
            }
            Err(_) => {
                let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
                return runtime_failure("HTTP readiness check failed; check the endpoint.");
            }
        }
    }
    let outcome = match run_once(
        &workload,
        options.workload.kill_after,
        Limits::default(),
        Some(&mut service_child),
    ) {
        Ok(outcome) => outcome,
        Err(error) => {
            let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
            return Err(error);
        }
    };
    if !cleanup_or_log(&mut service_child, options.workload.kill_after)
        && matches!(outcome, Outcome::Child(status) if status.success())
    {
        return runtime_failure("Managed service cleanup could not be confirmed.");
    }
    Ok(outcome)
}

fn wait_for_external_service(
    options: &Service,
    client: &reqwest::blocking::Client,
    workload: &environment::Plan,
    ready_deadline: Option<Instant>,
) -> Result<Outcome> {
    // The caller already performed a retryable preflight. Treat it as the
    // first unsuccessful probe so external endpoints never receive an
    // immediate duplicate request before their configured polling interval.
    sleep_cancellable(clip_to_deadline(options.interval, ready_deadline))?;
    loop {
        check_cancelled()?;
        match check_http(options, client, ready_deadline) {
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
) -> std::result::Result<(), HttpProbeError> {
    let remaining = deadline.map(|limit| limit.saturating_duration_since(Instant::now()));
    if remaining == Some(Duration::ZERO) {
        return Err(HttpProbeError::OverallTimeout);
    }
    let budget = remaining
        .map(|remaining| remaining.min(options.attempt_timeout))
        .unwrap_or(options.attempt_timeout);
    let method = match options.method {
        HttpMethod::Get => reqwest::Method::GET,
        HttpMethod::Head => reqwest::Method::HEAD,
    };
    let (sender, receiver) = mpsc::sync_channel(1);
    let client = client.clone();
    let url = options.url.clone();
    let expected_status = options.status;
    thread::spawn(move || {
        let result = http_attempt(client, url, method, expected_status, budget);
        let _ = sender.send(result);
    });
    let attempt_deadline = Instant::now()
        .checked_add(budget)
        .ok_or(HttpProbeError::Terminal)?;
    loop {
        if runtime::cancelled() {
            return Err(HttpProbeError::Cancelled);
        }
        let until_attempt = attempt_deadline.saturating_duration_since(Instant::now());
        if until_attempt.is_zero() {
            return Err(HttpProbeError::AttemptTimeout);
        }
        let until_overall = deadline
            .map(|limit| limit.saturating_duration_since(Instant::now()))
            .unwrap_or(until_attempt);
        if until_overall.is_zero() {
            return Err(HttpProbeError::OverallTimeout);
        }
        match receiver.recv_timeout(POLL.min(until_attempt).min(until_overall)) {
            Ok(result) => return result,
            Err(mpsc::RecvTimeoutError::Timeout) => (),
            Err(mpsc::RecvTimeoutError::Disconnected) => return Err(HttpProbeError::Terminal),
        }
    }
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
            } else if error.is_connect() && !has_tls_failure(&error) {
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

fn has_tls_failure(error: &reqwest::Error) -> bool {
    contains_tls_failure(error)
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

fn service_http_client(https: bool) -> Result<reqwest::blocking::Client> {
    let tls = if https {
        native_service_tls()?
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

fn native_service_tls() -> Result<rustls::ClientConfig> {
    // rustls-native-certs currently honors these OpenSSL-style overrides on
    // supported platforms. Service probes promise OS trust only, and run no
    // child until this preflight completes, so scope the workaround to root
    // loading and restore the caller's environment before any workload spawn.
    // Remove it once the loader offers an explicit native-only API.
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
    let mut child = spawn(plan, output_mode)?;
    let mut last_activity = child
        .activity
        .as_ref()
        .map(Activity::last_observed_at)
        .unwrap_or_else(Instant::now);
    loop {
        if let Some(activity) = &child.activity {
            last_activity = activity.last_observed_at();
        }
        if child.output_failed() {
            cleanup_run_once_children(&mut child, &mut monitored_service, kill_after);
            return runtime_failure("Could not forward workload output.");
        }
        if let Some(service) = monitored_service.as_deref_mut() {
            if service.output_failed() {
                let _ = cleanup_or_log(service, kill_after);
                let _ = cleanup_or_log(&mut child, kill_after);
                return runtime_failure("Could not forward managed service output.");
            }
        }
        if runtime::cancelled() {
            let _ = cleanup_or_log(&mut child, kill_after);
            return Err(Failure::new(
                Code::Cancelled,
                "Execution was cancelled; owned children were asked to stop.",
            ));
        }
        let service_completion = match monitored_service.as_deref_mut() {
            Some(service) => match service.completion() {
                Ok(completion) => completion,
                Err(error) => {
                    let _ = cleanup_or_log(service, kill_after);
                    let _ = cleanup_or_log(&mut child, kill_after);
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
        if service_completion.is_some_and(|service| {
            service_exited_before_workload(
                service.observed_at,
                workload_completion
                    .as_ref()
                    .map(|workload| workload.observed_at),
            )
        }) {
            let service = monitored_service.expect("a completed service is monitored");
            let _ = cleanup_or_log(service, kill_after);
            let _ = cleanup_or_log(&mut child, kill_after);
            return runtime_failure("The managed service exited before the workload completed.");
        }
        if let Some(completion) = workload_completion {
            // A pipe reader can record its successful final read while the
            // completion worker is being observed. Refresh the timestamp at
            // this decision boundary so that read still resets idle time.
            if let Some(activity) = &child.activity {
                last_activity = activity.last_observed_at();
            }
            if child.output_failed() {
                cleanup_run_once_children(&mut child, &mut monitored_service, kill_after);
                return runtime_failure("Could not forward workload output.");
            }
            if limits_expired_at(&limits, last_activity, completion.observed_at) {
                tracing::debug!(operation = "run", stage = "timeout", "run_cleanup");
                let _ = cleanup_or_log(&mut child, kill_after);
                return Ok(Outcome::Code(124));
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
                // already sent graceful and forced signals within its bounded
                // deadlines, so preserve the direct child's status rather
                // than turn a completed workload into a hang.
                return Ok(Outcome::Child(status));
            }
            if descendants_running {
                // Cleanup is bounded, but an output forwarding thread can
                // still be blocked by the wrapper's own downstream pipe. Do
                // not turn a successful bounded cleanup into an unbounded
                // output join; returning drops the detached forwarders.
                return Ok(Outcome::Child(status));
            }
            child.join_output();
            if child.output_failed() {
                return runtime_failure("Could not forward workload output.");
            }
            return Ok(Outcome::Child(status));
        }
        if limits_expired_at(&limits, last_activity, Instant::now()) {
            tracing::debug!(operation = "run", stage = "timeout", "run_cleanup");
            let _ = cleanup_or_log(&mut child, kill_after);
            return Ok(Outcome::Code(124));
        }
        thread::sleep(POLL);
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
    foreground_terminal: Option<Arc<ForegroundTerminal>>,
    #[cfg(windows)]
    job: Job,
    completions: mpsc::Receiver<Result<Completion>>,
    completion: Option<Completion>,
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
    parent_group: libc::pid_t,
    child_group: libc::pid_t,
}

fn spawn(plan: &environment::Plan, mode: OutputMode) -> Result<OwnedChild> {
    let mut command = environment::command(plan)?;
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
    #[cfg(unix)]
    let foreground_terminal = match foreground_parent_group {
        Some(parent_group) => match ForegroundTerminal::transfer(parent_group, child.id()) {
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
        output_threads.push(forward(
            stdout,
            stderr_for_both,
            activity.clone(),
            output_failure.clone(),
        ));
    }
    if let Some(stderr) = child.stderr.take() {
        output_threads.push(forward(
            stderr,
            true,
            activity.clone(),
            output_failure.clone(),
        ));
    }
    let pid = child.id();
    let (completion_tx, completions) = mpsc::sync_channel(1);
    #[cfg(unix)]
    let completion_terminal = foreground_terminal.clone();
    thread::spawn(move || {
        #[cfg(unix)]
        let completion = wait_for_completion(child, completion_terminal);
        #[cfg(not(unix))]
        let completion = child
            .wait()
            .map(|status| Completion {
                status,
                observed_at: Instant::now(),
            })
            .map_err(|error| Failure::io(&error));
        let _ = completion_tx.send(completion);
    });
    Ok(OwnedChild {
        pid,
        #[cfg(unix)]
        unix_ownership,
        #[cfg(unix)]
        foreground_terminal,
        #[cfg(windows)]
        job,
        completions,
        completion: None,
        activity,
        output_failure,
        output_threads,
    })
}

fn forward(
    mut reader: impl Read + Send + 'static,
    stderr: bool,
    activity: Option<Activity>,
    output_failure: Option<Arc<AtomicBool>>,
) -> thread::JoinHandle<()> {
    thread::spawn(move || {
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
                output
                    .write_all(&buffer[..count])
                    .and_then(|()| output.flush())
            } else {
                let mut output = io::stdout().lock();
                output
                    .write_all(&buffer[..count])
                    .and_then(|()| output.flush())
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
            .fetch_max(elapsed, std::sync::atomic::Ordering::Relaxed);
    }

    fn last_observed_at(&self) -> Instant {
        self.started_at
            .checked_add(Duration::from_nanos(
                self.latest_elapsed_nanos
                    .load(std::sync::atomic::Ordering::Relaxed),
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
                Ok(completion) => self.completion = Some(completion?),
                Err(mpsc::TryRecvError::Empty) => (),
                Err(mpsc::TryRecvError::Disconnected) => {
                    return runtime_failure(
                        "Cannot observe the owned child process; cleanup may be incomplete.",
                    );
                }
            }
        }
        Ok(self.completion.clone())
    }

    fn cleanup(&mut self, kill_after: Duration, initial_cancellation: Option<usize>) -> Result<()> {
        // Continue to supervise the owned process group/job after the direct
        // child exits. A descendant can retain an inherited output pipe, and
        // a forwarding thread can block writing to the wrapper's consumer.
        // Bounded cleanup must never wait for either thread to drain.
        if !self.tree_running()? {
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
        // group has no attached console to receive CTRL_BREAK. In that case
        // the fallback has already skipped graceful shutdown, so spend only
        // the bounded confirmation interval waiting for the Job to drain.
        let graceful_wait = if graceful_delivered {
            kill_after
        } else {
            CLEANUP_CONFIRMATION
        };
        let graceful_deadline = Instant::now().checked_add(graceful_wait).ok_or_else(|| {
            Failure::new(Code::IoFailed, "Cleanup deadline cannot be represented.")
        })?;
        while Instant::now() < graceful_deadline {
            if !self.tree_running()? {
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
            self.signal(true)?;
        }
        let confirmation = Instant::now()
            .checked_add(CLEANUP_CONFIRMATION)
            .ok_or_else(|| {
                Failure::new(Code::IoFailed, "Cleanup deadline cannot be represented.")
            })?;
        while Instant::now() < confirmation {
            if !self.tree_running()? {
                return Ok(());
            }
            thread::sleep(POLL);
        }
        Err(Failure::new(
            Code::TerminationTimeout,
            "Owned child cleanup could not be confirmed before the bounded deadline.",
        ))
    }

    fn join_output(&mut self) {
        for handle in self.output_threads.drain(..) {
            let _ = handle.join();
        }
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
    fn tree_running(&self) -> Result<bool> {
        match self.unix_ownership {
            UnixOwnership::ProcessGroup => process_group_running(self.pid),
            UnixOwnership::DirectChild => process_running(self.pid),
        }
    }

    #[cfg(windows)]
    fn tree_running(&self) -> Result<bool> {
        self.job.is_running()
    }
}

#[cfg(unix)]
impl Drop for OwnedChild {
    fn drop(&mut self) {
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
    // waitpid owns reaping from this point. Keeping std::process::Child would
    // add no supervision capability, and dropping it never terminates a child.
    drop(child);
    loop {
        let mut status = 0;
        let observed = unsafe { libc::waitpid(pid, &mut status, libc::WUNTRACED) };
        if observed == -1 {
            let error = io::Error::last_os_error();
            if error.kind() == io::ErrorKind::Interrupted {
                continue;
            }
            return Err(Failure::io(&error));
        }
        if libc::WIFSTOPPED(status) {
            if let Some(terminal) = &foreground_terminal {
                terminal.suspend_wrapper()?;
            }
            continue;
        }
        if libc::WIFEXITED(status) || libc::WIFSIGNALED(status) {
            return Ok(Completion {
                status: ExitStatus::from_raw(status),
                observed_at: Instant::now(),
            });
        }
        return runtime_failure("Owned child reported an unsupported process state.");
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

    // The installed launcher is a supported intermediate process: the outer
    // wrapper creates its group for Node, and the inner native wrapper shares
    // that group. Verify the complete parent/grandparent relationship rather
    // than trusting launcher arguments or environment values, which workloads
    // can control.
    if process_group != parent || unsafe { libc::getpgid(parent) } != parent {
        return false;
    }
    let Some(grandparent) = parent_process(parent) else {
        return false;
    };
    grandparent > 0
        && unsafe { libc::getpgid(grandparent) } != process_group
        && executable_matches(grandparent, &current)
}

#[cfg(unix)]
fn executable_matches(process: libc::pid_t, current: &Path) -> bool {
    parent_executable(process)
        .and_then(|path| path.canonicalize().ok())
        .is_some_and(|executable| executable == current)
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
) -> Result<Option<libc::pid_t>> {
    if !matches!(ownership, UnixOwnership::ProcessGroup)
        || matches!(mode, OutputMode::Service)
        || unsafe { libc::isatty(libc::STDIN_FILENO) } == 0
    {
        return Ok(None);
    }
    let foreground_group = unsafe { libc::tcgetpgrp(libc::STDIN_FILENO) };
    if foreground_group == -1 {
        let error = io::Error::last_os_error();
        if error.raw_os_error() == Some(libc::ENOTTY) {
            return Ok(None);
        }
        return Err(Failure::io(&error));
    }
    let parent_group = unsafe { libc::getpgrp() };
    if parent_group <= 0 {
        return Err(Failure::new(
            Code::IoFailed,
            "Could not identify the wrapper's terminal process group.",
        ));
    }
    Ok((foreground_group == parent_group).then_some(parent_group))
}

#[cfg(unix)]
impl ForegroundTerminal {
    fn transfer(parent_group: libc::pid_t, child_pid: u32) -> Result<Self> {
        let child_group = child_pid as libc::pid_t;
        set_terminal_foreground_group(child_group).map_err(|error| Failure::io(&error))?;
        if let Err(error) = continue_process_group(child_pid) {
            let _ = set_terminal_foreground_group(parent_group);
            return Err(error);
        }
        Ok(Self {
            parent_group,
            child_group,
        })
    }

    fn restore(&self) {
        if unsafe { libc::tcgetpgrp(libc::STDIN_FILENO) } != self.child_group {
            return;
        }
        if set_terminal_foreground_group(self.parent_group).is_err() {
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
        set_terminal_foreground_group(self.parent_group).map_err(|error| Failure::io(&error))?;
        suspend_process_group(self.parent_group)?;
        set_terminal_foreground_group(self.child_group).map_err(|error| Failure::io(&error))?;
        continue_process_group(self.child_group as u32)
    }
}

#[cfg(unix)]
fn set_terminal_foreground_group(group: libc::pid_t) -> io::Result<()> {
    // A completed workload leaves the wrapper in a background group until its
    // terminal is restored. Block SIGTTOU in this thread around tcsetpgrp so
    // restoration cannot stop the wrapper before it returns control to the
    // invoking shell.
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
        let result = libc::tcsetpgrp(libc::STDIN_FILENO, group);
        let operation_error = io::Error::last_os_error();
        let restore_result =
            libc::pthread_sigmask(libc::SIG_SETMASK, original.as_ptr(), std::ptr::null_mut());
        if restore_result != 0 {
            return Err(io::Error::from_raw_os_error(restore_result));
        }
        if result == -1 {
            return Err(operation_error);
        }
    }
    Ok(())
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
    value.len() == 32 && value.bytes().all(|byte| byte.is_ascii_hexdigit())
}

#[cfg(any(target_os = "linux", test))]
fn valid_uuid(value: &str) -> bool {
    value.len() == 36
        && value.bytes().enumerate().all(|(index, byte)| match index {
            8 | 13 | 18 | 23 => byte == b'-',
            _ => byte.is_ascii_hexdigit(),
        })
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
    if identity.is_empty()
        || !identity
            .bytes()
            .all(|byte| byte.is_ascii_hexdigit() || matches!(byte, b'-' | b'{' | b'}'))
    {
        return runtime_failure("The local machine identity is invalid.");
    }
    Ok(identity.into_bytes())
}

#[cfg(windows)]
fn wide(value: &str) -> Vec<u16> {
    value.encode_utf16().chain(std::iter::once(0)).collect()
}

fn ensure_private_dir(path: &Path) -> Result<()> {
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
            restrict_windows_state_directory(&directory)?;
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

fn open_lock(path: &Path) -> Result<File> {
    #[cfg(unix)]
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};

    #[cfg(target_os = "macos")]
    let existed = path.try_exists().map_err(|error| Failure::io(&error))?;
    let mut options = OpenOptions::new();
    options.create(true).read(true).write(true);
    #[cfg(unix)]
    {
        options.mode(0o600).custom_flags(libc::O_NOFOLLOW);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;

        use windows_sys::Win32::{
            Foundation::{GENERIC_READ, GENERIC_WRITE},
            Storage::FileSystem::{FILE_FLAG_OPEN_REPARSE_POINT, READ_CONTROL},
        };
        options
            .access_mode(GENERIC_READ | GENERIC_WRITE | READ_CONTROL)
            .custom_flags(FILE_FLAG_OPEN_REPARSE_POINT);
    }
    let file = options.open(path).map_err(|error| Failure::io(&error))?;
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
        ".{}.{}.tmp",
        path.file_name()
            .and_then(|name| name.to_str())
            .unwrap_or("state"),
        std::process::id()
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
                Storage::FileSystem::{FILE_FLAG_OPEN_REPARSE_POINT, READ_CONTROL},
            };
            options
                .access_mode(GENERIC_WRITE | READ_CONTROL)
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
        options.custom_flags(libc::O_NOFOLLOW);
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
fn restrict_windows_state_directory(file: &File) -> Result<()> {
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
    if unsafe { InitializeAcl(acl, size as u32, ACL_REVISION) } == 0
        || unsafe {
            AddAccessAllowedAceEx(
                acl,
                ACL_REVISION,
                CONTAINER_INHERIT_ACE | OBJECT_INHERIT_ACE,
                FILE_ALL_ACCESS,
                user,
            )
        } == 0
    {
        return runtime_failure(
            "Execution state directory access controls could not be initialized.",
        );
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
        return runtime_failure(
            "Execution state directory access controls could not be restricted.",
        );
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
            EqualSid, GetAce, GetAclInformation, ACCESS_ALLOWED_ACE, ACL_SIZE_INFORMATION,
            DACL_SECURITY_INFORMATION, OWNER_SECURITY_INFORMATION,
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
        if dacl.is_null() || !owner_belongs_to_current_identity(owner, user, default_owner) {
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

fn service_exited_before_workload(service: Instant, workload: Option<Instant>) -> bool {
    workload.is_none_or(|workload| service < workload)
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
    use std::os::unix::fs::OpenOptionsExt;

    use fs4::FileExt;

    use super::*;

    #[test]
    fn overflowing_refill_baseline_fails_closed() {
        let directory = tempfile::tempdir().unwrap();
        let path = directory.path().join("bucket.json");
        let bucket = Bucket {
            version: STATE_VERSION,
            limit: 1,
            period_ms: 1_000,
            burst: 1,
            tokens: 0.0,
            refill_utc_ms: i64::MIN,
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
}

#[cfg(test)]
mod lifecycle_tests {
    use super::*;

    #[test]
    fn service_completion_requires_a_strictly_earlier_event() {
        let start = Instant::now();
        let later = start.checked_add(Duration::from_millis(1)).unwrap();

        assert!(service_exited_before_workload(start, None));
        assert!(service_exited_before_workload(start, Some(later)));
        assert!(!service_exited_before_workload(later, Some(start)));
        assert!(!service_exited_before_workload(start, Some(start)));
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
}

#[cfg(test)]
mod state_key_tests {
    use super::*;

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

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
    sync::{mpsc, Arc},
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

fn rate_limit(options: RateLimit) -> Result<Outcome> {
    let plan = environment::prepare(options.workload.args)?;
    let key = StateKey::new(Namespace::Rate, &options.name, &options.scope)?;
    let start = Instant::now();
    let deadline = admission_deadline(start, options.wait_timeout)?;
    loop {
        check_cancelled()?;
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

    let service_plan = service.expect("service is checked above");
    let mut service_child = spawn(&service_plan, OutputMode::Service)?;
    loop {
        if runtime::cancelled() {
            let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
            return check_cancelled().and_then(|()| unreachable!());
        }
        if service_child.try_wait()?.is_some() {
            let _ = cleanup_or_log(&mut service_child, options.workload.kill_after);
            return runtime_failure("The managed service exited before it became ready.");
        }
        match check_http(&options, &client, ready_deadline) {
            Ok(()) => break,
            Err(code) if code.retryable() => {
                // The service can exit while one bounded HTTP attempt is in
                // progress. Prefer that owned-process failure to reporting a
                // coincident readiness deadline.
                if service_child.try_wait()?.is_some() {
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
                if service_child.try_wait()?.is_some() {
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
            } else if error.is_connect() {
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
    let output_mode = if limits.idle.is_some() {
        OutputMode::WorkloadPiped
    } else {
        OutputMode::WorkloadInherited
    };
    let mut child = spawn(plan, output_mode)?;
    let mut last_activity = Instant::now();
    loop {
        if let Some(activity) = &child.activity {
            while activity.try_recv().is_ok() {
                last_activity = Instant::now();
            }
        }
        if runtime::cancelled() {
            let _ = cleanup_or_log(&mut child, kill_after);
            return Err(Failure::new(
                Code::Cancelled,
                "Execution was cancelled; owned children were asked to stop.",
            ));
        }
        if let Some(service) = monitored_service.as_deref_mut() {
            if service.try_wait()?.is_some() {
                let _ = cleanup_or_log(service, kill_after);
                let _ = cleanup_or_log(&mut child, kill_after);
                return runtime_failure(
                    "The managed service exited before the workload completed.",
                );
            }
        }
        if let Some(status) = child.try_wait()? {
            // Reaping the direct child does not end ownership of its process
            // group or Job Object. A background descendant can otherwise
            // retain an inherited pipe or outlive the wrapper after its parent
            // exits, so confirm bounded cleanup before returning this status.
            let descendants_running = child.tree_running()?;
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
            return Ok(Outcome::Child(status));
        }
        let timed_out = limits
            .overall
            .is_some_and(|deadline| Instant::now() >= deadline)
            || limits
                .idle
                .is_some_and(|idle| last_activity.elapsed() >= idle);
        if timed_out {
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

enum OutputMode {
    WorkloadInherited,
    WorkloadPiped,
    Service,
}

struct OwnedChild {
    child: Child,
    #[cfg(windows)]
    job: Job,
    activity: Option<mpsc::Receiver<()>>,
    output_threads: Vec<thread::JoinHandle<()>>,
}

fn spawn(plan: &environment::Plan, mode: OutputMode) -> Result<OwnedChild> {
    let mut command = environment::command(plan)?;
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
    let (activity_tx, activity) = if matches!(mode, OutputMode::WorkloadPiped) {
        let (tx, rx) = mpsc::sync_channel(1);
        (Some(tx), Some(rx))
    } else {
        (None, None)
    };
    let stderr_for_both = matches!(mode, OutputMode::Service);
    let mut output_threads = Vec::new();
    if let Some(stdout) = child.stdout.take() {
        output_threads.push(forward(stdout, stderr_for_both, activity_tx.clone()));
    }
    if let Some(stderr) = child.stderr.take() {
        output_threads.push(forward(stderr, true, activity_tx));
    }
    #[cfg(windows)]
    let job = match Job::attach(&child) {
        Ok(job) => job,
        Err(error) => {
            // A process that cannot be placed in the ownership job must not
            // survive a failed wrapper start without a cleanup attempt.
            let _ = child.kill();
            let _ = child.wait();
            return Err(error);
        }
    };
    Ok(OwnedChild {
        child,
        #[cfg(windows)]
        job,
        activity,
        output_threads,
    })
}

fn forward(
    mut reader: impl Read + Send + 'static,
    stderr: bool,
    activity: Option<mpsc::SyncSender<()>>,
) -> thread::JoinHandle<()> {
    thread::spawn(move || {
        let mut buffer = [0u8; 8192];
        loop {
            let count = match reader.read(&mut buffer) {
                Ok(0) | Err(_) => return,
                Ok(count) => count,
            };
            // A successful read is workload activity even when a slow consumer
            // blocks forwarding these bytes for longer than the idle limit.
            if let Some(sender) = &activity {
                let _ = sender.try_send(());
            }
            let write = if stderr {
                io::stderr().lock().write_all(&buffer[..count])
            } else {
                io::stdout().lock().write_all(&buffer[..count])
            };
            if write.is_err() {
                return;
            }
        }
    })
}

impl OwnedChild {
    fn try_wait(&mut self) -> Result<Option<ExitStatus>> {
        self.child.try_wait().map_err(|_| {
            Failure::new(
                Code::IoFailed,
                "Cannot observe the owned child process; cleanup may be incomplete.",
            )
        })
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
            pid = self.child.id(),
            stage = "graceful",
            "run_cleanup"
        );
        self.signal(false)?;
        let graceful_deadline = Instant::now().checked_add(kill_after).ok_or_else(|| {
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
            pid = self.child.id(),
            stage = "forced",
            "run_cleanup"
        );
        self.signal(true)?;
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
    fn signal(&self, force: bool) -> Result<()> {
        signal_process_tree(self.child.id(), force)
    }

    #[cfg(windows)]
    fn signal(&self, force: bool) -> Result<()> {
        self.job.signal(self.child.id(), force)
    }

    #[cfg(unix)]
    fn tree_running(&self) -> Result<bool> {
        process_tree_running(self.child.id())
    }

    #[cfg(windows)]
    fn tree_running(&self) -> Result<bool> {
        self.job.is_running()
    }
}

#[cfg(unix)]
fn configure_process_group(command: &mut ProcessCommand) {
    use std::os::unix::process::CommandExt;

    // Every wrapper owns a dedicated group. An outer wrapper signals the inner
    // clibox process, which then performs its own bounded cleanup for its group.
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
    command.creation_flags(windows_sys::Win32::System::Threading::CREATE_NEW_PROCESS_GROUP);
}

#[cfg(unix)]
fn signal_process_tree(pid: u32, force: bool) -> Result<()> {
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
fn process_tree_running(pid: u32) -> Result<bool> {
    let result = unsafe { libc::kill(-(pid as i32), 0) };
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

    fn signal(&self, pid: u32, force: bool) -> Result<()> {
        use windows_sys::Win32::{
            Foundation::FALSE,
            System::{
                Console::{GenerateConsoleCtrlEvent, CTRL_BREAK_EVENT},
                JobObjects::TerminateJobObject,
            },
        };
        let result = if force {
            unsafe { TerminateJobObject(self.handle, 1) }
        } else {
            unsafe { GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pid) }
        };
        if result == FALSE {
            return Err(Failure::io(&io::Error::last_os_error()));
        }
        Ok(())
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
        let digest = hash_key(kind, scope.scope, &identity, name);
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

fn hash_key(kind: &str, scope: Scope, identity: &[u8], name: &str) -> String {
    let mut hasher = Sha256::new();
    hasher.update(kind.as_bytes());
    hasher.update([0]);
    hasher.update(match scope {
        Scope::Project => b"project".as_slice(),
        Scope::User => b"user".as_slice(),
    });
    hasher.update([0]);
    hasher.update(identity);
    hasher.update([0]);
    hasher.update(name.as_bytes());
    format!("{:x}", hasher.finalize())
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
        return Ok(());
    }
    let metadata = fs::symlink_metadata(path).map_err(|error| Failure::io(&error))?;
    if !metadata.file_type().is_dir() || metadata.file_type().is_symlink() {
        return runtime_failure("Execution state directory is not a safe directory.");
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if metadata.permissions().mode() & 0o077 != 0 {
            return runtime_failure("Execution state directory permissions are unsafe.");
        }
    }
    Ok(())
}

fn open_lock(path: &Path) -> Result<File> {
    #[cfg(unix)]
    use std::os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt};

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
    let metadata = file.metadata().map_err(|error| Failure::io(&error))?;
    if !metadata.is_file() {
        return runtime_failure("Execution state lock is not a regular file.");
    }
    #[cfg(unix)]
    if metadata.permissions().mode() & 0o077 != 0 || metadata.nlink() != 1 {
        return runtime_failure("Execution state lock permissions or links are unsafe.");
    }
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
    loop {
        check_cancelled()?;
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
            #[cfg(windows)]
            {
                ensure_windows_state_object(&file, false)?;
                ensure_windows_private_dacl(&file)?;
            }
            let mut bytes = Vec::new();
            file.read_to_end(&mut bytes)
                .map_err(|error| Failure::io(&error))?;
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
        let elapsed = (now - bucket.refill_utc_ms) as f64;
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
        if metadata.permissions().mode() & 0o077 != 0 || metadata.nlink() != 1 {
            return runtime_failure("Execution rate-limit state permissions or links are unsafe.");
        }
    }
    Ok(())
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
        Foundation::{ERROR_SUCCESS, GENERIC_ALL},
        Security::{
            AddAccessAllowedAceEx,
            Authorization::{SetSecurityInfo, SE_FILE_OBJECT},
            GetLengthSid, InitializeAcl, ACL, ACL_REVISION, CONTAINER_INHERIT_ACE,
            DACL_SECURITY_INFORMATION, OBJECT_INHERIT_ACE, PROTECTED_DACL_SECURITY_INFORMATION,
        },
    };

    let token = current_user_token()?;
    let user = token_user_sid(&token)?;
    // Refuse to alter an object owned by another account even if a permissive
    // parent DACL temporarily granted this process WRITE_DAC access.
    ensure_windows_state_owner(file, user)?;
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
                GENERIC_ALL,
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
fn ensure_windows_state_owner(file: &File, user: windows_sys::Win32::Security::PSID) -> Result<()> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::{LocalFree, ERROR_SUCCESS},
        Security::{
            Authorization::{GetSecurityInfo, SE_FILE_OBJECT},
            EqualSid, OWNER_SECURITY_INFORMATION,
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
    let owned_by_current_user = !owner.is_null() && unsafe { EqualSid(owner, user) } != 0;
    unsafe {
        LocalFree(descriptor);
    }
    if !owned_by_current_user {
        return runtime_failure("Execution state ownership is unsafe.");
    }
    Ok(())
}

#[cfg(windows)]
fn ensure_windows_private_dacl(file: &File) -> Result<()> {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::{LocalFree, ERROR_SUCCESS, GENERIC_ALL},
        Security::{
            Authorization::{GetSecurityInfo, SE_FILE_OBJECT},
            EqualSid, GetAce, GetAclInformation, ACCESS_ALLOWED_ACE, ACL_SIZE_INFORMATION,
            DACL_SECURITY_INFORMATION, OWNER_SECURITY_INFORMATION,
        },
        System::SystemServices::ACCESS_ALLOWED_ACE_TYPE,
    };

    let token = current_user_token()?;
    let user = token_user_sid(&token)?;
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
        if owner.is_null() || dacl.is_null() || unsafe { EqualSid(owner, user) } == 0 {
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
            || ace.Mask != GENERIC_ALL
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
    use windows_sys::Win32::{
        Foundation::CloseHandle,
        Security::{GetTokenInformation, TokenUser, TOKEN_QUERY, TOKEN_USER},
        System::Threading::{GetCurrentProcess, OpenProcessToken},
    };

    let mut handle = std::ptr::null_mut();
    if unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut handle) } == 0 {
        return runtime_failure("Current-user ownership could not be verified.");
    }
    let result = (|| {
        let mut length = 0;
        unsafe {
            GetTokenInformation(handle, TokenUser, std::ptr::null_mut(), 0, &mut length);
        }
        if length < std::mem::size_of::<TOKEN_USER>() as u32 {
            return runtime_failure("Current-user ownership could not be verified.");
        }
        let mut bytes = vec![0u8; length as usize];
        if unsafe {
            GetTokenInformation(
                handle,
                TokenUser,
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

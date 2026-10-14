// SPDX-License-Identifier: Apache-2.0
// Closed callers validate/compile their destinations before reaching OS
// dispatch. `dispatch` always uses the operating-system default browser (OAuth
// authorization depends on that); `open_external` honors a saved browser choice
// for user-facing links.
use std::{
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::{
        Mutex, MutexGuard,
        atomic::{AtomicBool, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

use crate::{
    NativeFailure,
    browser_discovery::{self, LaunchSpec},
    browser_preferences::BrowserChoice,
    external_url::UrlClass,
};

const INSTALLED_GRACE: Duration = Duration::from_secs(2);

/// Serializes the commit-to-spawn transition of an OS default browser dispatch
/// with the retirement of the flag it observes. Retirement (a closed Settings
/// screen, window close or host stop) and the transition that checks the flag
/// and starts the process are mutually exclusive, so a dispatch either never
/// spawns after retirement or finishes its spawn first; a process that was
/// already spawned is then terminated or its dispatch never reports success.
/// The lock is held only across that check and `spawn()`: it takes no other
/// lock, so retirement may hold any caller lock while waiting for it.
static COMMIT: Mutex<()> = Mutex::new(());

fn commit_lock() -> MutexGuard<'static, ()> {
    COMMIT
        .lock()
        .unwrap_or_else(|poisoned| poisoned.into_inner())
}

/// Retires a dispatch flag. Every writer of a flag that `dispatch` observes
/// must use this instead of storing directly, or the commit-to-spawn race
/// remains open.
pub(crate) fn retire(flag: &AtomicBool) {
    let _commit = commit_lock();
    flag.store(true, Ordering::Release);
}

/// Points where a retirement can interleave with one dispatch. The production
/// observer ignores them; tests retire the flag at each one.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum DispatchPhase {
    /// The flag was checked clear and the commit lock is held; `spawn()` is
    /// next.
    Committing,
    /// The process exists and the commit lock is released.
    Spawned,
    /// The process exited successfully; the final retirement check is next.
    Exited,
}

pub(crate) fn dispatch(url: &str, stopped: &AtomicBool) -> std::result::Result<(), NativeFailure> {
    if stopped.load(Ordering::Acquire) {
        return Err(NativeFailure::Stopped);
    }
    #[cfg(target_os = "macos")]
    let mut command = Command::new("/usr/bin/open");
    #[cfg(target_os = "linux")]
    let mut command = Command::new("/usr/bin/xdg-open");
    #[cfg(windows)]
    let mut command = {
        let root = std::env::var_os("SystemRoot").ok_or(NativeFailure::SidecarMissing)?;
        let mut command =
            Command::new(std::path::PathBuf::from(root).join("System32/rundll32.exe"));
        command.arg("url.dll,FileProtocolHandler");
        command
    };
    command
        .arg(url)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    apply_environment(&mut command, false);
    dispatch_command(command, stopped, &mut |_| {})
}

fn dispatch_command(
    mut command: Command,
    stopped: &AtomicBool,
    observe: &mut dyn FnMut(DispatchPhase),
) -> std::result::Result<(), NativeFailure> {
    let mut child = {
        // Check and spawn under one lock: a retirement either lands before the
        // check and refuses this dispatch, or waits until the process exists.
        let _commit = commit_lock();
        if stopped.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        observe(DispatchPhase::Committing);
        command.spawn().map_err(|_| NativeFailure::SidecarFailed)?
    };
    observe(DispatchPhase::Spawned);
    let until = Instant::now() + Duration::from_secs(5);
    loop {
        if stopped.load(Ordering::Acquire) {
            // Retired after the process started: terminate it and never
            // report the dispatch as successful.
            let _ = child.kill();
            let _ = child.wait();
            return Err(NativeFailure::Stopped);
        }
        match child.try_wait() {
            Ok(Some(status)) if status.success() => {
                observe(DispatchPhase::Exited);
                // Retirement stores under the commit lock, so this read is
                // ordered against it: success is reported only if no
                // retirement completed before the process finished.
                let _commit = commit_lock();
                return if stopped.load(Ordering::Acquire) {
                    Err(NativeFailure::Stopped)
                } else {
                    Ok(())
                };
            }
            Ok(Some(_)) => return Err(NativeFailure::SidecarFailed),
            Ok(None) if Instant::now() < until => thread::sleep(Duration::from_millis(10)),
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(NativeFailure::TimedOut);
            }
        }
    }
}

/// Credential-free launch environment: nothing is inherited except a short
/// display/session allowlist.
fn apply_environment(command: &mut Command, installed_browser: bool) {
    command.env_clear();
    let mut names = vec![
        "HOME",
        "USERPROFILE",
        "SystemRoot",
        "WINDIR",
        "TMPDIR",
        "TEMP",
        "TMP",
        "DISPLAY",
        "XAUTHORITY",
        "XDG_RUNTIME_DIR",
        "DBUS_SESSION_BUS_ADDRESS",
        "XDG_CURRENT_DESKTOP",
    ];
    if installed_browser {
        // Browsers need their own profile and display locations, but still
        // receive no tokens, provider variables or shell state.
        names.extend([
            "APPDATA",
            "LOCALAPPDATA",
            "ProgramFiles",
            "ProgramFiles(x86)",
            "ProgramData",
            "WAYLAND_DISPLAY",
            "XDG_SESSION_TYPE",
            "XDG_CONFIG_HOME",
            "XDG_DATA_HOME",
            "XDG_DATA_DIRS",
            "LANG",
        ]);
    }
    for name in names {
        if let Some(value) = std::env::var_os(name) {
            command.env(name, value);
        }
    }
    #[cfg(unix)]
    command.env(
        "PATH",
        if installed_browser {
            "/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin"
        } else {
            "/usr/bin:/bin:/usr/sbin:/sbin"
        },
    );
}

/// Opens a validated URL in the browser selected for its class. A saved
/// identity is resolved through a fresh native inventory immediately before
/// launch. There is no fallback to another browser, no retry and no second
/// launch: a missing or failing browser is reported and the choice is kept.
pub(crate) fn open_external(
    url: &str,
    class: UrlClass,
    choice: &BrowserChoice,
    stopped: &AtomicBool,
) -> Result<(), NativeFailure> {
    open_external_with(url, class, choice, stopped, &browser_discovery::resolve)
}

fn open_external_with(
    url: &str,
    class: UrlClass,
    choice: &BrowserChoice,
    stopped: &AtomicBool,
    resolve: &dyn Fn(&str) -> Result<browser_discovery::InstalledBrowser, NativeFailure>,
) -> Result<(), NativeFailure> {
    let browser = choice.log_identity();
    if stopped.load(Ordering::Acquire) {
        tracing::warn!(operation = "external_url_open", url_class = ?class, browser, phase = "rejected", code = "stopped");
        return Err(NativeFailure::Stopped);
    }
    let result = match choice {
        BrowserChoice::SystemDefault => dispatch(url, stopped),
        BrowserChoice::Installed { identity } => {
            tracing::info!(operation = "external_url_open", url_class = ?class, browser, phase = "resolving");
            resolve(identity).and_then(|installed| launch(&installed.launch, url, stopped))
        }
    };
    match &result {
        Ok(()) => {
            tracing::info!(operation = "external_url_open", url_class = ?class, browser, phase = "dispatched")
        }
        Err(code) => {
            tracing::warn!(operation = "external_url_open", url_class = ?class, browser, phase = "failed", ?code)
        }
    }
    result
}

fn program_path(program: &str) -> Option<PathBuf> {
    let direct = Path::new(program);
    if direct.is_absolute() {
        return direct.is_file().then(|| direct.to_owned());
    }
    // Relative launcher names (for example `env` or `firefox`) resolve only in
    // fixed system locations, never in the inherited PATH or working directory.
    if program.is_empty() || program.contains(['/', '\\']) {
        return None;
    }
    #[cfg(unix)]
    let directories = [
        "/usr/local/bin",
        "/usr/bin",
        "/bin",
        "/snap/bin",
        "/var/lib/flatpak/exports/bin",
    ];
    #[cfg(not(unix))]
    let directories: [&str; 0] = [];
    directories
        .iter()
        .map(|directory| Path::new(directory).join(program))
        .find(|candidate| candidate.is_file())
}

/// Launches argv `[program, ..args]` with the URL as one value. Installed
/// browsers may keep running as the launched process, so a child that is still
/// alive after a short grace period is a successful dispatch and is detached
/// and reaped on its own thread; it is never killed or relaunched.
pub(crate) fn launch(
    spec: &LaunchSpec,
    url: &str,
    stopped: &AtomicBool,
) -> Result<(), NativeFailure> {
    let program = program_path(&spec.program).ok_or(NativeFailure::BrowserUnavailable)?;
    let mut command = Command::new(program);
    command
        .args(spec.argv(url))
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null());
    apply_environment(&mut command, true);
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        command.process_group(0);
    }
    let mut child = spawn_retrying_text_file_busy(&mut command, stopped)?;
    let until = Instant::now() + INSTALLED_GRACE;
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                return if status.success() {
                    Ok(())
                } else {
                    Err(NativeFailure::BrowserLaunchFailed)
                };
            }
            Ok(None) if stopped.load(Ordering::Acquire) => {
                thread::spawn(move || {
                    let _ = child.wait();
                });
                return Err(NativeFailure::Stopped);
            }
            Ok(None) if Instant::now() < until => thread::sleep(Duration::from_millis(10)),
            Ok(None) => {
                thread::spawn(move || {
                    let _ = child.wait();
                });
                return Ok(());
            }
            Err(_) => return Err(NativeFailure::BrowserLaunchFailed),
        }
    }
}

/// Spawns the browser, retrying only when exec fails with ETXTBSY ("text file
/// busy"). On Linux a concurrent fork elsewhere in the process can briefly
/// inherit a write descriptor to a freshly written executable; exec then
/// fails although nothing ran, so a retry cannot launch the browser twice.
/// Every other error is returned immediately. Remove this when launches no
/// longer target freshly written executables (only tests do today).
#[cfg(target_os = "linux")]
fn spawn_retrying_text_file_busy(
    command: &mut Command,
    stopped: &AtomicBool,
) -> Result<std::process::Child, NativeFailure> {
    spawn_retrying_text_file_busy_with(stopped, || command.spawn(), thread::sleep)
}

#[cfg(target_os = "linux")]
fn spawn_retrying_text_file_busy_with<T>(
    stopped: &AtomicBool,
    mut spawn: impl FnMut() -> std::io::Result<T>,
    mut sleep: impl FnMut(Duration),
) -> Result<T, NativeFailure> {
    // ETXTBSY is 26 on every Linux architecture.
    const TEXT_FILE_BUSY: i32 = 26;
    let mut attempts = 0u64;
    loop {
        // A failed exec did not launch a browser. Recheck shutdown after each
        // backoff before another attempt can start one.
        if stopped.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        match spawn() {
            Err(error) if error.raw_os_error() == Some(TEXT_FILE_BUSY) && attempts < 20 => {
                attempts += 1;
                sleep(Duration::from_millis(5 * attempts));
            }
            Ok(child) => return Ok(child),
            Err(_) => return Err(NativeFailure::BrowserLaunchFailed),
        }
    }
}

/// Other platforms do not report ETXTBSY for this race; spawn once.
#[cfg(not(target_os = "linux"))]
fn spawn_retrying_text_file_busy(
    command: &mut Command,
    _stopped: &AtomicBool,
) -> Result<std::process::Child, NativeFailure> {
    command
        .spawn()
        .map_err(|_| NativeFailure::BrowserLaunchFailed)
}

#[cfg(all(test, unix))]
mod tests {
    use std::{fs, os::unix::fs::PermissionsExt, sync::Arc};

    use super::*;
    use crate::browser_discovery::{BrowserEntry, InstalledBrowser, LaunchArg};

    fn script(directory: &Path, body: &str) -> PathBuf {
        let path = directory.join("browser.sh");
        fs::write(&path, format!("#!/bin/sh\n{body}\n")).unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).unwrap();
        path
    }
    fn spec(program: &Path) -> LaunchSpec {
        LaunchSpec {
            program: program.to_str().unwrap().into(),
            args: vec![LaunchArg::Text("--flag".into()), LaunchArg::Url],
        }
    }
    fn browser(program: &Path) -> InstalledBrowser {
        InstalledBrowser {
            entry: BrowserEntry {
                identity: "fixture.desktop".into(),
                name: "Fixture".into(),
            },
            launch: spec(program),
        }
    }

    // Freshly written scripts can fail to spawn with ETXTBSY while another
    // test thread's fork still holds the script's write descriptor before its
    // exec. Such a spawn failure never runs the script, so it leaves no `ran`
    // marker; the launcher itself still performs exactly one launch per call.
    // A launch that ran the script (marker present) or succeeded is returned
    // unchanged, so a real failure or a duplicate launch is still observable.
    fn launch_until_spawned(
        spec: &LaunchSpec,
        url: &str,
        ran: &Path,
    ) -> std::result::Result<(), NativeFailure> {
        let mut result = launch(spec, url, &AtomicBool::new(false));
        for _ in 0..50 {
            if result.is_ok() || ran.exists() {
                break;
            }
            thread::sleep(Duration::from_millis(20));
            result = launch(spec, url, &AtomicBool::new(false));
        }
        result
    }

    // OS dispatch retirement table (the device open lifecycle table in
    // docs/cmds-delidev-grok-subscription-contract.md). A retirement can land
    // at five points; the commit lock makes the first two exclusive with the
    // spawn itself.
    //
    // - before the commit: no process starts; the result is Stopped.
    // - while committing (blocked on the lock): the process spawns, is
    //   terminated and the result is Stopped.
    // - after the spawn, launcher running or already exited: the process is
    //   terminated or reaped; the result is Stopped.
    // - after a successful exit, before the final check: the process is reaped;
    //   the result is Stopped, never Ok.
    // - after the dispatch returned Ok: the result is unchanged.
    fn shell(body: &str) -> Command {
        let mut command = Command::new("/bin/sh");
        command
            .args(["-c", body])
            .stdin(Stdio::null())
            .stdout(Stdio::null())
            .stderr(Stdio::null());
        command
    }
    fn process_alive(pid: &str) -> bool {
        Command::new("/bin/sh")
            .args(["-c", &format!("kill -0 {pid} 2>/dev/null")])
            .status()
            .unwrap()
            .success()
    }
    // Each launcher records its pid, then either exits at once or keeps
    // running until the dispatcher terminates it.
    fn launcher(dir: &Path, long_running: bool) -> (Command, PathBuf) {
        let pid = dir.join("pid");
        let tail = if long_running {
            "exec sleep 30"
        } else {
            "exit 0"
        };
        (
            shell(&format!("echo $$ > '{}'; {tail}", pid.display())),
            pid,
        )
    }
    fn assert_terminated(pid: &Path) {
        if let Ok(pid) = fs::read_to_string(pid) {
            assert!(
                !process_alive(pid.trim()),
                "launcher {pid} survived retirement"
            );
        }
    }

    #[test]
    fn retirement_before_the_commit_refuses_without_starting_a_process() {
        let dir = tempfile::tempdir().unwrap();
        let (command, pid) = launcher(dir.path(), false);
        let stopped = AtomicBool::new(false);
        retire(&stopped);
        let mut phases = Vec::new();
        let result = dispatch_command(command, &stopped, &mut |phase| phases.push(phase));
        assert_eq!(result, Err(NativeFailure::Stopped));
        assert!(phases.is_empty(), "a retired dispatch reached {phases:?}");
        assert!(!pid.exists(), "a retired dispatch started a process");
    }

    #[test]
    fn retirement_cannot_land_between_the_flag_check_and_the_spawn() {
        for long_running in [false, true] {
            let dir = tempfile::tempdir().unwrap();
            let (command, pid) = launcher(dir.path(), long_running);
            let stopped = Arc::new(AtomicBool::new(false));
            let mut retirer = None;
            let started = Instant::now();
            let result = dispatch_command(command, &stopped, &mut |phase| {
                if phase != DispatchPhase::Committing {
                    return;
                }
                let flag = Arc::clone(&stopped);
                retirer = Some(thread::spawn(move || retire(&flag)));
                // The retirement is blocked on the commit lock this dispatch
                // holds, so the flag cannot change before the spawn.
                thread::sleep(Duration::from_millis(150));
                assert!(
                    !stopped.load(Ordering::Acquire),
                    "retirement landed inside the commit-to-spawn transition"
                );
            });
            retirer.expect("commit phase observed").join().unwrap();
            assert!(stopped.load(Ordering::Acquire));
            assert_eq!(result, Err(NativeFailure::Stopped), "long={long_running}");
            assert!(started.elapsed() < Duration::from_secs(10));
            assert_terminated(&pid);
        }
    }

    #[test]
    fn retirement_after_the_spawn_terminates_the_launcher_and_never_reports_success() {
        for long_running in [false, true] {
            let dir = tempfile::tempdir().unwrap();
            let (command, pid) = launcher(dir.path(), long_running);
            let stopped = AtomicBool::new(false);
            let started = Instant::now();
            let result = dispatch_command(command, &stopped, &mut |phase| {
                if phase == DispatchPhase::Spawned {
                    retire(&stopped);
                }
            });
            assert_eq!(result, Err(NativeFailure::Stopped), "long={long_running}");
            assert!(started.elapsed() < Duration::from_secs(10));
            assert_terminated(&pid);
        }
    }

    #[test]
    fn retirement_after_a_successful_exit_is_never_reported_as_success() {
        let dir = tempfile::tempdir().unwrap();
        let (command, pid) = launcher(dir.path(), false);
        let stopped = AtomicBool::new(false);
        let result = dispatch_command(command, &stopped, &mut |phase| {
            if phase == DispatchPhase::Exited {
                retire(&stopped);
            }
        });
        // The launcher may be observed as stopped before it exits; either way
        // the retired dispatch is not a success.
        assert_eq!(result, Err(NativeFailure::Stopped));
        assert_terminated(&pid);
    }

    #[test]
    fn retirement_after_dispatch_returned_leaves_the_result_unchanged() {
        let dir = tempfile::tempdir().unwrap();
        let (command, pid) = launcher(dir.path(), false);
        let stopped = AtomicBool::new(false);
        assert_eq!(dispatch_command(command, &stopped, &mut |_| {}), Ok(()));
        retire(&stopped);
        assert!(stopped.load(Ordering::Acquire));
        assert_terminated(&pid);
    }

    #[test]
    fn a_failing_or_missing_launcher_is_still_a_sidecar_failure() {
        let stopped = AtomicBool::new(false);
        assert_eq!(
            dispatch_command(shell("exit 3"), &stopped, &mut |_| {}),
            Err(NativeFailure::SidecarFailed)
        );
        assert_eq!(
            dispatch_command(Command::new("/nonexistent/launcher"), &stopped, &mut |_| {}),
            Err(NativeFailure::SidecarFailed)
        );
    }

    #[test]
    fn url_is_exactly_one_argv_value_without_shell_interpretation_or_credentials() {
        let dir = tempfile::tempdir().unwrap();
        let out = dir.path().join("record");
        let program = script(
            dir.path(),
            &format!(
                "printf '%s\\n' \"$#\" \"$1\" \"$2\" > '{0}'; env | grep -c -E \
                 'TOKEN|SECRET|OPENAI' >> '{0}'; exit 0",
                out.display()
            ),
        );
        // SAFETY: tests in this module do not read this variable concurrently.
        unsafe { std::env::set_var("DELIDEV_FIXTURE_SECRET_TOKEN", "x") };
        let url = "https://example.com/?a=$(touch pwned)&b=;`id`|x";
        launch_until_spawned(&spec(&program), url, &out).unwrap();
        let record = fs::read_to_string(&out).unwrap();
        let lines: Vec<_> = record.lines().collect();
        assert_eq!(lines[0], "2");
        assert_eq!(lines[1], "--flag");
        assert_eq!(lines[2], url);
        assert_eq!(lines[3], "0");
        assert!(!dir.path().join("pwned").exists());
    }

    #[test]
    fn missing_program_and_nonzero_exit_are_distinct_failures_without_retry() {
        let dir = tempfile::tempdir().unwrap();
        let count = dir.path().join("count");
        let program = script(
            dir.path(),
            &format!("echo x >> '{}'; exit 3", count.display()),
        );
        assert_eq!(
            launch_until_spawned(&spec(&program), "https://example.com/", &count),
            Err(NativeFailure::BrowserLaunchFailed)
        );
        assert_eq!(fs::read_to_string(&count).unwrap().lines().count(), 1);
        assert_eq!(
            launch(
                &spec(&dir.path().join("absent")),
                "https://example.com/",
                &AtomicBool::new(false)
            ),
            Err(NativeFailure::BrowserUnavailable)
        );
    }

    #[test]
    fn a_long_running_browser_is_dispatched_and_never_killed() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("alive");
        let program = script(
            dir.path(),
            &format!("sleep 3; echo done > '{}'", marker.display()),
        );
        let started = Instant::now();
        launch_until_spawned(&spec(&program), "https://example.com/", &marker).unwrap();
        assert!(started.elapsed() < Duration::from_millis(2900));
        // The detached browser must still finish its own work; poll generously
        // because the host may be heavily loaded.
        let until = Instant::now() + Duration::from_secs(15);
        while !marker.exists() && Instant::now() < until {
            std::thread::sleep(Duration::from_millis(50));
        }
        assert!(marker.exists(), "browser process was killed after dispatch");
    }

    #[test]
    fn stopped_fence_prevents_any_launch() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("ran");
        let program = script(dir.path(), &format!("echo x > '{}'", marker.display()));
        let stopped = AtomicBool::new(true);
        let installed = browser(&program);
        let result = open_external_with(
            "https://example.com/",
            UrlClass::General,
            &BrowserChoice::Installed {
                identity: "fixture.desktop".into(),
            },
            &stopped,
            &|_| Ok(installed.clone()),
        );
        assert_eq!(result, Err(NativeFailure::Stopped));
        assert!(!marker.exists());
    }

    #[test]
    fn unresolved_identity_reports_unavailable_without_fallback_dispatch() {
        let stopped = AtomicBool::new(false);
        let result = open_external_with(
            "https://example.com/",
            UrlClass::Local,
            &BrowserChoice::Installed {
                identity: "removed.desktop".into(),
            },
            &stopped,
            &|_| Err(NativeFailure::BrowserUnavailable),
        );
        assert_eq!(result, Err(NativeFailure::BrowserUnavailable));
    }

    #[test]
    fn selected_identity_launches_exactly_once_through_its_resolved_recipe() {
        let dir = tempfile::tempdir().unwrap();
        let count = dir.path().join("count");
        let program = script(dir.path(), &format!("echo \"$2\" >> '{}'", count.display()));
        let installed = browser(&program);
        open_external_with(
            "https://localhost.example/x",
            UrlClass::General,
            &BrowserChoice::Installed {
                identity: "fixture.desktop".into(),
            },
            &AtomicBool::new(false),
            &|identity| {
                assert_eq!(identity, "fixture.desktop");
                Ok(installed.clone())
            },
        )
        .unwrap();
        assert_eq!(
            fs::read_to_string(&count).unwrap(),
            "https://localhost.example/x\n"
        );
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn shutdown_during_text_file_busy_backoff_prevents_retry_spawn() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("ran");
        let program = script(dir.path(), &format!("echo x >> '{}'", marker.display()));
        let mut writer = Some(fs::OpenOptions::new().write(true).open(&program).unwrap());
        let stopped = AtomicBool::new(false);
        let mut command = Command::new(&program);
        let mut backoffs = 0;

        let result = spawn_retrying_text_file_busy_with(
            &stopped,
            || command.spawn(),
            |delay| {
                backoffs += 1;
                stopped.store(true, Ordering::Release);
                drop(writer.take());
                thread::sleep(delay);
            },
        );

        assert_eq!(backoffs, 1);
        assert!(matches!(result, Err(NativeFailure::Stopped)));
        assert!(!marker.exists());
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn text_file_busy_retry_launches_once_after_writer_closes() {
        let dir = tempfile::tempdir().unwrap();
        let marker = dir.path().join("ran");
        let program = script(dir.path(), &format!("echo x >> '{}'", marker.display()));
        let mut writer = Some(fs::OpenOptions::new().write(true).open(&program).unwrap());
        let stopped = AtomicBool::new(false);
        let mut command = Command::new(&program);
        let mut backoffs = 0;

        let mut child = spawn_retrying_text_file_busy_with(
            &stopped,
            || command.spawn(),
            |delay| {
                backoffs += 1;
                drop(writer.take());
                thread::sleep(delay);
            },
        )
        .unwrap();

        assert_eq!(backoffs, 1);
        assert!(child.wait().unwrap().success());
        assert_eq!(fs::read_to_string(marker).unwrap(), "x\n");
    }

    #[cfg(target_os = "linux")]
    #[test]
    fn retry_helper_keeps_immediate_stop_and_other_spawn_error_distinct() {
        let stopped = AtomicBool::new(true);
        let result = spawn_retrying_text_file_busy_with(
            &stopped,
            || -> std::io::Result<()> { panic!("spawn after shutdown") },
            |_| panic!("backoff after shutdown"),
        );
        assert!(matches!(result, Err(NativeFailure::Stopped)));

        stopped.store(false, Ordering::Release);
        let mut attempts = 0;
        let result = spawn_retrying_text_file_busy_with(
            &stopped,
            || -> std::io::Result<()> {
                attempts += 1;
                Err(std::io::ErrorKind::PermissionDenied.into())
            },
            |_| panic!("backoff after non-ETXTBSY error"),
        );
        assert_eq!(attempts, 1);
        assert!(matches!(result, Err(NativeFailure::BrowserLaunchFailed)));
    }
}

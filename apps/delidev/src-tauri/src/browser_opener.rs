// SPDX-License-Identifier: Apache-2.0
// Closed callers validate/compile their destinations before reaching OS
// dispatch. `dispatch` always uses the operating-system default browser (OAuth
// authorization depends on that); `open_external` honors a saved browser choice
// for user-facing links.
use std::{
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::atomic::{AtomicBool, Ordering},
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
    let mut child = command.spawn().map_err(|_| NativeFailure::SidecarFailed)?;
    let until = Instant::now() + Duration::from_secs(5);
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                return if status.success() {
                    Ok(())
                } else {
                    Err(NativeFailure::SidecarFailed)
                };
            }
            Ok(None) if Instant::now() < until && !stopped.load(Ordering::Acquire) => {
                thread::sleep(Duration::from_millis(10))
            }
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
    let mut child = command
        .spawn()
        .map_err(|_| NativeFailure::BrowserLaunchFailed)?;
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

#[cfg(all(test, unix))]
mod tests {
    use std::{fs, os::unix::fs::PermissionsExt};

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
        launch(&spec(&program), url, &AtomicBool::new(false)).unwrap();
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
            launch(
                &spec(&program),
                "https://example.com/",
                &AtomicBool::new(false)
            ),
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
        launch(
            &spec(&program),
            "https://example.com/",
            &AtomicBool::new(false),
        )
        .unwrap();
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
}

// SPDX-License-Identifier: Apache-2.0
//! Native process-ownership tests. This harness-less test binary first
//! dispatches the supervisor, like Go's TestMain, because `process::run`
//! re-executes the current executable as the supervisor. Helper modes then
//! run as descendants, and finally every test case runs on its own thread.
//!
//! Arguments filter test names by substring.

use std::{
    fs,
    panic::AssertUnwindSafe,
    time::{Duration, Instant},
};

use anyhow::anyhow;
use cachecow::{
    Context, paths,
    process::{self, Command, Hooks, Scope, Streams, SupervisorHooks},
};

fn test_hooks() -> SupervisorHooks {
    SupervisorHooks {
        ready: Some(Box::new(|s: &Scope| {
            fs::write(paths::join2(&s.directory, "test-ready"), "ready")?;
            Ok(())
        })),
        before_start: Some(Box::new(|s: &Scope, c: &mut Command| {
            if paths::base(&c.path).starts_with("before-start-failure") {
                return Err(anyhow!("injected preparation failure"));
            }
            c.env.push(format!("CCOW_TEST_SUPERVISOR={}", s.id));
            Ok(())
        })),
        quiesce: Some(Box::new(|s: &Scope| {
            if process::reconcile(s).is_ok() {
                return Err(anyhow!("completion written before IPC quiescence"));
            }
            fs::write(paths::join2(&s.directory, "test-quiesced"), "quiesced")?;
            Ok(())
        })),
        drained: Some(Box::new(|s: &Scope| {
            process::reconcile(s)?;
            fs::write(paths::join2(&s.directory, "test-drained"), "drained")?;
            Ok(())
        })),
    }
}

/// A private state root; the TempDir guard must outlive the test.
fn state_root() -> (tempfile::TempDir, String) {
    let dir = tempfile::tempdir().expect("temporary directory");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(dir.path(), fs::Permissions::from_mode(0o700)).unwrap();
    }
    #[cfg(windows)]
    windows_tests::make_private(dir.path().to_str().unwrap());
    let resolved = paths::eval_symlinks(dir.path().to_str().unwrap()).unwrap();
    (dir, resolved)
}

fn wait_for(path: &str, timeout: Duration) -> bool {
    let deadline = Instant::now() + timeout;
    while Instant::now() < deadline {
        if fs::metadata(path).is_ok() {
            return true;
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    false
}

fn run_with(
    ctx: &Context,
    dir: &str,
    command: Command,
    streams: Streams,
    owned: &mut dyn FnMut(&Scope) -> anyhow::Result<()>,
    drained: &mut dyn FnMut(&Scope) -> anyhow::Result<()>,
) -> (process::RunResult, anyhow::Result<()>) {
    process::run(ctx, dir, command, streams, Hooks { owned, drained })
}

#[cfg(unix)]
fn run_plain(
    ctx: &Context,
    dir: &str,
    command: Command,
) -> (process::RunResult, anyhow::Result<()>) {
    run_with(
        ctx,
        dir,
        command,
        Streams::default(),
        &mut |_| Ok(()),
        &mut |_| Ok(()),
    )
}

/// Cancels `ctx` once `path` exists.
#[cfg(unix)]
fn cancel_when(path: String, handle: cachecow::context::CancelHandle, ctx: Context) {
    std::thread::spawn(move || {
        while !ctx.is_done() {
            if fs::metadata(&path).is_ok() {
                handle.cancel();
                return;
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    });
}

#[cfg(unix)]
mod unix_tests {
    use std::{
        fs::File,
        io::{Seek, Write},
        os::unix::process::CommandExt,
    };

    use super::*;

    fn shell(dir: &str, script: &str) -> Command {
        Command {
            noop: false,
            path: "/bin/sh".into(),
            args: vec!["sh".into(), "-c".into(), script.into()],
            dir: dir.into(),
            env: vec!["PATH=/usr/bin:/bin".into(), "TEST_VALUE=separate".into()],
        }
    }

    fn alive(pid: i32) -> bool {
        // SAFETY: signal 0 only checks existence.
        unsafe { libc::kill(pid, 0) == 0 }
    }

    pub fn native_streams_and_exit() {
        let (_guard, dir) = state_root();
        let mut input = File::options()
            .read(true)
            .write(true)
            .create_new(true)
            .open(paths::join2(&dir, "input"))
            .unwrap();
        input.write_all(b"from stdin\n").unwrap();
        input.rewind().unwrap();
        let out_path = paths::join2(&dir, "stdout");
        let err_path = paths::join2(&dir, "stderr");
        let streams = Streams {
            stdin: Some(input),
            stdout: Some(File::create(&out_path).unwrap()),
            stderr: Some(File::create(&err_path).unwrap()),
        };
        let (mut owned, mut drained) = (false, false);
        let (r, e) = run_with(
            &Context::background(),
            &dir,
            shell(
                &dir,
                r#"read value; printf '%s|%s|%s' "$value" "$TEST_VALUE" "$PWD"; printf 'error only' >&2; exit 37"#,
            ),
            streams,
            &mut |s| {
                owned = true;
                assert!(!s.started, "child started before ownership barrier");
                Ok(())
            },
            &mut |s| {
                drained = true;
                assert!(s.complete && s.exit_code == 37, "incorrect completion");
                Ok(())
            },
        );
        e.unwrap();
        assert!(
            r.exit_code == 37 && owned && drained,
            "result={r:?} owned={owned} drained={drained}"
        );
        assert_eq!(
            fs::read_to_string(&out_path).unwrap(),
            format!("from stdin|separate|{dir}")
        );
        assert_eq!(fs::read_to_string(&err_path).unwrap(), "error only");
        let journal = fs::read_to_string(paths::join2(&r.scope.directory, "scope.json")).unwrap();
        assert!(
            !journal.contains("TEST_VALUE") && !journal.contains("printf"),
            "sensitive command persisted"
        );
        assert!(
            journal.contains("\"version\":2"),
            "new journals use version 2"
        );
        assert!(!r.scope.boot_id.is_empty(), "native boot identity recorded");
    }

    pub fn ownership_failure_never_starts_child() {
        let (_guard, dir) = state_root();
        let sentinel = paths::join2(&dir, "started");
        let (r, e) = run_with(
            &Context::background(),
            &dir,
            shell(&dir, &format!("touch '{sentinel}'")),
            Streams::default(),
            &mut |_| Err(anyhow!("durable claim failed")),
            &mut |_| panic!("unexpected completion callback"),
        );
        assert_eq!(e.unwrap_err().to_string(), "durable claim failed");
        assert!(
            wait_for(
                &paths::join2(&r.scope.directory, "test-drained"),
                Duration::from_secs(5)
            ),
            "no-child supervisor did not finalize"
        );
        assert!(
            fs::metadata(&sentinel).is_err(),
            "child crossed failed barrier"
        );
    }

    pub fn cancellation_waits_five_seconds() {
        let (_guard, dir) = state_root();
        let ready = paths::join2(&dir, "ready");
        let (ctx, cancel) = Context::background().with_cancel();
        cancel_when(ready.clone(), cancel, ctx.clone());
        let started = Instant::now();
        let (r, e) = run_plain(
            &ctx,
            &dir,
            shell(
                &dir,
                &format!("trap '' TERM; touch '{ready}'; while :; do sleep 1; done"),
            ),
        );
        e.unwrap();
        assert!(
            started.elapsed() >= process::GRACE_PERIOD,
            "killed before five-second grace"
        );
        assert_eq!(r.exit_code, 137);
    }

    pub fn graceful_cancellation_preserves_exit() {
        let (_guard, dir) = state_root();
        let (ctx, cancel) = Context::background().with_cancel();
        cancel_when(paths::join2(&dir, "ready"), cancel, ctx.clone());
        let started = Instant::now();
        let (r, e) = run_plain(
            &ctx,
            &dir,
            shell(
                &dir,
                "trap 'exit 42' TERM; touch ready; while :; do sleep 1; done",
            ),
        );
        e.unwrap();
        assert_eq!(r.exit_code, 42);
        assert!(
            started.elapsed() < process::GRACE_PERIOD,
            "graceful exit unnecessarily delayed"
        );
    }

    pub fn root_exit_drains_detached_descendant() {
        let (_guard, dir) = state_root();
        let exe = paths::path_string(&std::env::current_exe().unwrap()).unwrap();
        let mut env: Vec<String> = std::env::vars().map(|(k, v)| format!("{k}={v}")).collect();
        env.push("CCOW_TREE_HELPER=root".into());
        env.push(format!("CCOW_TEST_DIR={dir}"));
        let command = Command {
            noop: false,
            path: exe.clone(),
            args: vec![exe],
            dir: dir.clone(),
            env,
        };
        let started = Instant::now();
        let (r, e) = run_plain(&Context::background(), &dir, command);
        e.unwrap();
        assert_eq!(r.exit_code, 23, "root exit");
        assert!(
            started.elapsed() >= process::GRACE_PERIOD,
            "descendant skipped grace"
        );
        let pid: i32 = fs::read_to_string(paths::join2(&dir, "descendant"))
            .unwrap()
            .trim()
            .parse()
            .unwrap();
        assert!(pid > 1);
        assert!(!alive(pid), "descendant {pid} survived completed scope");
    }

    /// Shell snippet that sets `zombies` to the number of zombie children of
    /// the root's parent (the supervisor).
    #[cfg(target_os = "linux")]
    const ZOMBIE_COUNT: &str = r#"
        zombies=0
        for stat in /proc/[0-9]*/stat; do
            { read -r _ _ state ppid _ < "$stat"; } 2>/dev/null || continue
            if [ "$state" = Z ] && [ "$ppid" = "$PPID" ]; then
                zombies=$((zombies + 1))
            fi
        done
    "#;

    /// Orphans that exit while the root is still running must be reaped
    /// without waiting for the root, and without disturbing its status.
    #[cfg(target_os = "linux")]
    pub fn reaps_orphans_while_root_runs() {
        let (_guard, dir) = state_root();
        let out = paths::join2(&dir, "zombies");
        let script = format!(
            "i=0; while [ $i -lt 20 ]; do (sleep 0.01 &); i=$((i + 1)); done; sleep 1; \
             {ZOMBIE_COUNT} printf '%s' \"$zombies\" > '{out}'; exit 7"
        );
        let (r, e) = run_plain(&Context::background(), &dir, shell(&dir, &script));
        e.unwrap();
        assert_eq!(r.exit_code, 7, "root exit code");
        assert_eq!(
            fs::read_to_string(&out).unwrap(),
            "0",
            "exited orphans stayed zombies while the root ran"
        );
    }

    /// Orphans that exit at the same moment as the root must never take the
    /// root's exit status.
    #[cfg(target_os = "linux")]
    pub fn orphan_reaping_preserves_root_status() {
        for round in 0..20 {
            let (_guard, dir) = state_root();
            let script = "i=0; while [ $i -lt 20 ]; do (sleep 0.05 &); i=$((i + 1)); done; sleep \
                          0.05; exit 3";
            let (r, e) = run_plain(&Context::background(), &dir, shell(&dir, script));
            e.unwrap();
            assert_eq!(r.exit_code, 3, "root exit code in round {round}");
        }
    }

    pub fn tree_helper(mode: &str) -> ! {
        let dir = std::env::var("CCOW_TEST_DIR").unwrap_or_default();
        match mode {
            "root" => {
                let exe = std::env::current_exe().unwrap();
                let mut c = std::process::Command::new(exe);
                c.env("CCOW_TREE_HELPER", "child");
                // SAFETY: setsid is async-signal-safe.
                unsafe {
                    c.pre_exec(|| {
                        libc::setsid();
                        Ok(())
                    });
                }
                if c.spawn().is_err() {
                    std::process::exit(90);
                }
                if wait_for(&paths::join2(&dir, "descendant"), Duration::from_secs(5)) {
                    std::process::exit(23);
                }
                std::process::exit(91);
            }
            _ => {
                // exec a shell so SIGTERM is ignored before readiness is
                // visible.
                let script = format!(
                    "trap '' TERM; printf '%s' $$ > '{}'; while :; do sleep 1; done",
                    paths::join2(&dir, "descendant")
                );
                let e = std::process::Command::new("/bin/sh")
                    .arg("-c")
                    .arg(script)
                    .exec();
                eprintln!("exec: {e}");
                std::process::exit(92);
            }
        }
    }

    pub fn manager_death_still_drains() {
        let (_guard, dir) = state_root();
        let exe = std::env::current_exe().unwrap();
        let mut manager = std::process::Command::new(exe)
            .env("CCOW_MANAGER_HELPER", "1")
            .env("CCOW_TEST_DIR", &dir)
            .spawn()
            .unwrap();
        let owned = paths::join2(&dir, "owned");
        assert!(
            wait_for(&owned, Duration::from_secs(20)),
            "manager did not own a scope"
        );
        let scope_dir = fs::read_to_string(&owned).unwrap();
        // Wait until user code runs, not merely until ownership persisted.
        assert!(
            wait_for(&paths::join2(&dir, "ready"), Duration::from_secs(20)),
            "child not ready"
        );
        manager.kill().unwrap();
        let _ = manager.wait();
        let scope = Scope {
            id: paths::base(&scope_dir),
            directory: scope_dir.clone(),
            ..Scope::default()
        };
        let deadline = Instant::now() + Duration::from_secs(12);
        while Instant::now() < deadline {
            if process::reconcile(&scope).is_ok()
                && fs::metadata(paths::join2(&scope_dir, "test-drained")).is_ok()
            {
                return;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
        panic!("supervisor did not drain after manager death");
    }

    pub fn manager_helper() -> ! {
        let dir = std::env::var("CCOW_TEST_DIR").unwrap();
        let (_r, e) = run_with(
            &Context::background(),
            &dir,
            shell(&dir, "trap '' TERM; touch ready; while :; do sleep 1; done"),
            Streams::default(),
            &mut |s| {
                // Publish atomically: the test reads the path once it exists.
                let staged = paths::join2(&dir, "owned.tmp");
                fs::write(&staged, &s.directory)?;
                fs::rename(&staged, paths::join2(&dir, "owned"))?;
                Ok(())
            },
            &mut |_| Ok(()),
        );
        std::process::exit(i32::from(e.is_err()) * 95);
    }

    pub fn supervisor_death_blocks_recovery() {
        let (_guard, dir) = state_root();
        let (tx, rx) = std::sync::mpsc::channel::<Scope>();
        let run_dir = dir.clone();
        let runner = std::thread::spawn(move || {
            let (_r, e) = run_with(
                &Context::background(),
                &run_dir,
                shell(
                    &run_dir,
                    "trap '' TERM; printf '%s' $$ > child; exec /bin/sleep 60",
                ),
                Streams::default(),
                &mut |s| {
                    let _ = tx.send(s.clone());
                    Ok(())
                },
                &mut |_| panic!("dead supervisor reported drained"),
            );
            e
        });
        let scope = rx
            .recv_timeout(Duration::from_secs(20))
            .expect("no supervisor");
        let child = paths::join2(&dir, "child");
        let deadline = Instant::now() + Duration::from_secs(5);
        let mut pid = 0;
        while Instant::now() < deadline && pid <= 1 {
            pid = fs::read_to_string(&child)
                .ok()
                .and_then(|s| s.parse().ok())
                .unwrap_or(0);
            std::thread::sleep(Duration::from_millis(10));
        }
        assert!(pid > 1, "no child");
        // SAFETY: kill has no memory-safety preconditions.
        unsafe { libc::kill(scope.owner_pid as i32, libc::SIGKILL) };
        let deadline = Instant::now() + Duration::from_secs(5);
        while !runner.is_finished() && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(20));
        }
        assert!(runner.is_finished(), "run did not report lost supervisor");
        let err = runner.join().unwrap().unwrap_err();
        let still = process::reconcile(&scope);
        let retention = process::retention(&scope);
        // SAFETY: cleanup of the surviving fixture descendant.
        unsafe { libc::kill(pid, libc::SIGKILL) };
        assert!(
            cachecow::error::is(&err, &process::ProcessError::Reconciliation),
            "error={err:#}"
        );
        assert!(still.is_err(), "dead supervisor unlocked unfinished scope");
        if cfg!(target_os = "linux") {
            assert_eq!(retention, process::Retention::SameBoot);
        } else {
            assert_ne!(retention, process::Retention::Unreadable);
        }
    }

    pub fn start_failure_exit_codes() {
        let (_guard, dir) = state_root();
        for (name, mode, want) in [("missing", None, 127), ("denied", Some(0o600), 126)] {
            let path = paths::join2(&dir, name);
            if let Some(mode) = mode {
                use std::os::unix::fs::PermissionsExt;
                fs::write(&path, "#!/bin/sh\n").unwrap();
                fs::set_permissions(&path, fs::Permissions::from_mode(mode)).unwrap();
            }
            let command = Command {
                noop: false,
                path: path.clone(),
                args: vec![path],
                dir: dir.clone(),
                env: vec![],
            };
            let (r, e) = run_plain(&Context::background(), &dir, command);
            e.unwrap();
            assert_eq!(r.exit_code, want, "{name}");
        }
    }

    pub fn supervisor_session_hooks() {
        let (_guard, dir) = state_root();
        let (r, e) = run_plain(
            &Context::background(),
            &dir,
            shell(&dir, r#"printf '%s' "$CCOW_TEST_SUPERVISOR" > hook-token"#),
        );
        e.unwrap();
        assert_eq!(
            fs::read_to_string(paths::join2(&dir, "hook-token")).unwrap(),
            r.scope.id,
            "transient env"
        );
        for name in ["test-ready", "test-quiesced", "test-drained"] {
            assert!(
                fs::metadata(paths::join2(&r.scope.directory, name)).is_ok(),
                "{name}"
            );
        }
    }

    pub fn cancellation_after_ownership_finalizes_without_child() {
        let (_guard, dir) = state_root();
        let (ctx, cancel) = Context::background().with_cancel();
        let mut drained = false;
        let (r, e) = run_with(
            &ctx,
            &dir,
            shell(&dir, "touch should-not-start"),
            Streams::default(),
            &mut |_| {
                cancel.cancel();
                Ok(())
            },
            &mut |s| {
                drained = true;
                assert!(!s.started && s.complete, "unexpected no-child scope: {s:?}");
                process::reconcile(s)
            },
        );
        let e = e.unwrap_err();
        assert!(
            cachecow::error::find::<cachecow::context::CancelError>(&e).is_some() && drained,
            "error={e} drained={drained}"
        );
        assert!(
            fs::metadata(paths::join2(&dir, "should-not-start")).is_err(),
            "child ran"
        );
        assert!(wait_for(
            &paths::join2(&r.scope.directory, "test-drained"),
            Duration::from_secs(5)
        ));
    }

    pub fn before_start_failure_finalizes() {
        let (_guard, dir) = state_root();
        let path = paths::join2(&dir, "before-start-failure");
        let mut drained = false;
        let command = Command {
            noop: false,
            path: path.clone(),
            args: vec![path],
            dir: dir.clone(),
            env: vec![],
        };
        let (r, e) = run_with(
            &Context::background(),
            &dir,
            command,
            Streams::default(),
            &mut |_| Ok(()),
            &mut |s| {
                drained = true;
                process::reconcile(s)
            },
        );
        assert!(
            e.is_ok() && r.exit_code == 125 && drained,
            "result={r:?} error={e:?} drained={drained}"
        );
        for name in ["test-quiesced", "test-drained"] {
            assert!(
                fs::metadata(paths::join2(&r.scope.directory, name)).is_ok(),
                "{name}"
            );
        }
    }

    pub fn noop_command_finalizes() {
        let (_guard, dir) = state_root();
        let (r, e) = run_plain(
            &Context::background(),
            &dir,
            Command {
                noop: true,
                ..Command::default()
            },
        );
        e.unwrap();
        assert_eq!(r.exit_code, 0);
        assert!(r.scope.complete && r.scope.started);
    }

    pub fn termination_request_drains_session() {
        let (_guard, dir) = state_root();
        let (tx, rx) = std::sync::mpsc::channel::<Scope>();
        let run_dir = dir.clone();
        let runner = std::thread::spawn(move || {
            run_with(
                &Context::background(),
                &run_dir,
                shell(
                    &run_dir,
                    "trap 'exit 7' TERM; touch ready; while :; do sleep 1; done",
                ),
                Streams::default(),
                &mut |s| {
                    let _ = tx.send(s.clone());
                    Ok(())
                },
                &mut |_| Ok(()),
            )
        });
        let scope = rx.recv_timeout(Duration::from_secs(20)).unwrap();
        assert!(wait_for(
            &paths::join2(&dir, "ready"),
            Duration::from_secs(10)
        ));
        process::request_termination(scope.owner_pid).unwrap();
        // Cancellation does not close the control channel after the command
        // started: the caller (whose own context is live) observes completion
        // only through the final frame, after the supervisor drained the tree.
        let (r, e) = runner.join().unwrap();
        e.unwrap();
        assert_eq!(r.exit_code, 7);
        assert!(wait_for(
            &paths::join2(&scope.directory, "test-drained"),
            Duration::from_secs(10)
        ));
        let done = process::load_scope(&scope.directory).unwrap();
        assert!(done.complete);
        assert_eq!(done.exit_code, 7);
    }
}

#[cfg(windows)]
mod windows_tests {
    use std::{fs::File, io::Read as _};

    use windows_sys::Win32::{
        Foundation::{CloseHandle, FILETIME, WAIT_TIMEOUT},
        Security::{
            Authorization::{
                ConvertStringSecurityDescriptorToSecurityDescriptorW, SDDL_REVISION_1,
                SE_FILE_OBJECT, SetNamedSecurityInfoW,
            },
            DACL_SECURITY_INFORMATION, GetSecurityDescriptorDacl,
            PROTECTED_DACL_SECURITY_INFORMATION, PSECURITY_DESCRIPTOR,
        },
        System::Threading::{
            GetCurrentProcess, GetProcessTimes, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION,
            PROCESS_SYNCHRONIZE, TerminateProcess, WaitForSingleObject,
        },
    };

    use super::*;

    fn wide(s: &str) -> Vec<u16> {
        use std::os::windows::ffi::OsStrExt;
        std::ffi::OsStr::new(s)
            .encode_wide()
            .chain(Some(0))
            .collect()
    }

    pub fn make_private(path: &str) {
        let sid = cachecow::winsec::current_user()
            .unwrap()
            .to_string_sid()
            .unwrap();
        let sddl = wide(&format!("D:P(A;OICI;FA;;;{sid})"));
        let mut sd: PSECURITY_DESCRIPTOR = std::ptr::null_mut();
        // SAFETY: valid NUL-terminated SDDL; the descriptor is leaked (test
        // only).
        assert!(
            unsafe {
                ConvertStringSecurityDescriptorToSecurityDescriptorW(
                    sddl.as_ptr(),
                    SDDL_REVISION_1,
                    &mut sd,
                    std::ptr::null_mut(),
                )
            } != 0
        );
        let (mut present, mut defaulted) = (0, 0);
        let mut acl = std::ptr::null_mut();
        // SAFETY: `sd` is a valid descriptor.
        assert!(
            unsafe { GetSecurityDescriptorDacl(sd, &mut present, &mut acl, &mut defaulted) } != 0
        );
        let name = wide(path);
        // SAFETY: applies the protected DACL to the fixture directory.
        let r = unsafe {
            SetNamedSecurityInfoW(
                name.as_ptr(),
                SE_FILE_OBJECT,
                DACL_SECURITY_INFORMATION | PROTECTED_DACL_SECURITY_INFORMATION,
                std::ptr::null_mut(),
                std::ptr::null_mut(),
                acl,
                std::ptr::null(),
            )
        };
        assert_eq!(r, 0);
    }

    fn filetime(t: FILETIME) -> i64 {
        (i64::from(t.dwHighDateTime) << 32) | i64::from(t.dwLowDateTime)
    }

    fn created(handle: windows_sys::Win32::Foundation::HANDLE) -> i64 {
        let z = FILETIME {
            dwLowDateTime: 0,
            dwHighDateTime: 0,
        };
        let (mut c, mut e, mut k, mut u) = (z, z, z, z);
        // SAFETY: valid process handle and outputs.
        assert!(unsafe { GetProcessTimes(handle, &mut c, &mut e, &mut k, &mut u) } != 0);
        filetime(c)
    }

    fn helper_command(dir: &str, mode: (&str, &str)) -> Command {
        let exe = paths::path_string(&std::env::current_exe().unwrap()).unwrap();
        let mut env: Vec<String> = std::env::vars().map(|(k, v)| format!("{k}={v}")).collect();
        env.push(format!("{}={}", mode.0, mode.1));
        env.push(format!("CCOW_TREE_DIR={dir}"));
        Command {
            noop: false,
            path: exe.clone(),
            args: vec![exe],
            dir: dir.into(),
            env,
        }
    }

    fn journal(dir: &str, pid: u32, owner_created: i64) -> Scope {
        let directory = paths::join2(dir, &uuid::Uuid::now_v7().to_string());
        fs::create_dir(&directory).unwrap();
        let s = Scope {
            version: 2,
            id: paths::base(&directory),
            directory,
            owner_pid: i64::from(pid),
            owner_created,
            started: true,
            exit_code: 125,
            ..Scope::default()
        };
        process::testing::save_scope(&s).unwrap();
        s
    }

    pub fn native_completion_identity() {
        let (_guard, dir) = state_root();
        let mut child = std::process::Command::new("cmd")
            .args(["/c", "exit 0"])
            .spawn()
            .unwrap();
        use std::os::windows::io::AsRawHandle;
        let child_created = created(child.as_raw_handle() as _);
        let pid = child.id();
        child.wait().unwrap();
        drop(child);
        let exited = journal(&dir, pid, child_created);
        process::reconcile(&exited).unwrap();
        let recovered = process::load_scope(&exited.directory).unwrap();
        assert!(recovered.complete && recovered.exit_code == 125);
        // SAFETY: pseudo-handle of this process.
        let me = journal(
            &dir,
            std::process::id(),
            created(unsafe { GetCurrentProcess() }),
        );
        assert!(process::reconcile(&me).is_err());
        let zero = journal(&dir, std::process::id(), 0);
        assert!(process::reconcile(&zero).is_err());
        assert_eq!(
            process::retention(&zero),
            process::Retention::NoNativeIdentity
        );
    }

    pub fn native_streams_and_exit() {
        let (_guard, dir) = state_root();
        let input = paths::join2(&dir, "input");
        fs::write(&input, "stdin bytes").unwrap();
        let (out, err) = (paths::join2(&dir, "stdout"), paths::join2(&dir, "stderr"));
        let streams = Streams {
            stdin: Some(File::open(&input).unwrap()),
            stdout: Some(File::create(&out).unwrap()),
            stderr: Some(File::create(&err).unwrap()),
        };
        let (r, e) = run_with(
            &Context::background(),
            &dir,
            helper_command(&dir, ("CCOW_WINDOWS_CHILD", "1")),
            streams,
            &mut |s| {
                assert!(!s.started);
                Ok(())
            },
            &mut |s| {
                assert!(s.complete);
                Ok(())
            },
        );
        e.unwrap();
        assert_eq!(r.exit_code, 37);
        assert_eq!(
            fs::read_to_string(&out).unwrap(),
            format!("stdin bytes|{dir}")
        );
        assert_eq!(fs::read_to_string(&err).unwrap(), "stderr bytes");
        assert!(r.scope.owner_created != 0);
    }

    pub fn child_helper() -> ! {
        let mut data = String::new();
        if std::io::stdin().read_to_string(&mut data).is_err() {
            std::process::exit(90);
        }
        let cwd = paths::current_dir().unwrap_or_default();
        print!("{data}|{cwd}");
        eprint!("stderr bytes");
        std::process::exit(37);
    }

    pub fn root_exit_drains_job_descendant() {
        let (_guard, dir) = state_root();
        let started = Instant::now();
        let (r, e) = run_with(
            &Context::background(),
            &dir,
            helper_command(&dir, ("CCOW_WINDOWS_TREE", "root")),
            Streams::default(),
            &mut |_| Ok(()),
            &mut |s| process::reconcile(s),
        );
        e.unwrap();
        assert_eq!(r.exit_code, 23);
        assert!(
            started.elapsed() >= process::GRACE_PERIOD,
            "job descendant skipped grace"
        );
        let pid: u32 = fs::read_to_string(paths::join2(&dir, "child"))
            .unwrap()
            .parse()
            .unwrap();
        // SAFETY: OpenProcess returns an owned handle or null.
        let h = unsafe { OpenProcess(PROCESS_SYNCHRONIZE, 0, pid) };
        if !h.is_null() {
            // SAFETY: live handle.
            let status = unsafe { WaitForSingleObject(h, 0) };
            // SAFETY: owned handle.
            unsafe { CloseHandle(h) };
            assert_ne!(
                status, WAIT_TIMEOUT,
                "completed job still has a live descendant"
            );
        }
    }

    pub fn tree_helper(mode: &str) -> ! {
        let dir = std::env::var("CCOW_TREE_DIR").unwrap_or_default();
        if mode == "root" {
            let exe = std::env::current_exe().unwrap();
            if std::process::Command::new(exe)
                .env("CCOW_WINDOWS_TREE", "child")
                .spawn()
                .is_err()
            {
                std::process::exit(90);
            }
            if wait_for(&paths::join2(&dir, "child"), Duration::from_secs(5)) {
                std::process::exit(23);
            }
            std::process::exit(91);
        }
        // Survive the graceful CTRL_BREAK so the supervisor waits for the grace
        // interval: a registered handler consumes the event.
        let (ctx, cancel) = Context::background().with_cancel();
        let _registration = cachecow::signals::notify(cachecow::signals::SignalSet::Cli, cancel);
        let _ = ctx;
        if fs::write(paths::join2(&dir, "child"), std::process::id().to_string()).is_err() {
            std::process::exit(92);
        }
        loop {
            std::thread::sleep(Duration::from_secs(1));
        }
    }

    pub fn supervisor_kill_recovers_job() {
        let (_guard, dir) = state_root();
        let (tx, rx) = std::sync::mpsc::channel::<Scope>();
        let run_dir = dir.clone();
        let runner = std::thread::spawn(move || {
            run_with(
                &Context::background(),
                &run_dir,
                helper_command(&run_dir, ("CCOW_WINDOWS_TREE", "child")),
                Streams::default(),
                &mut |s| {
                    let _ = tx.send(s.clone());
                    Ok(())
                },
                &mut |_| Ok(()),
            )
        });
        let scope = rx.recv_timeout(Duration::from_secs(20)).unwrap();
        assert!(wait_for(
            &paths::join2(&dir, "child"),
            Duration::from_secs(10)
        ));
        let child: u32 = fs::read_to_string(paths::join2(&dir, "child"))
            .unwrap()
            .parse()
            .unwrap();
        // SAFETY: OpenProcess returns an owned handle or null.
        let owner = unsafe {
            OpenProcess(
                windows_sys::Win32::System::Threading::PROCESS_TERMINATE,
                0,
                scope.owner_pid as u32,
            )
        };
        assert!(!owner.is_null());
        // SAFETY: owned handle with terminate access.
        unsafe {
            TerminateProcess(owner, 1);
            CloseHandle(owner);
        }
        let (_r, e) = runner.join().unwrap();
        assert!(e.is_err());
        let deadline = Instant::now() + Duration::from_secs(10);
        while process::reconcile(&scope).is_err() && Instant::now() < deadline {
            std::thread::sleep(Duration::from_millis(50));
        }
        process::reconcile(&scope).unwrap();
        // SAFETY: OpenProcess returns an owned handle or null.
        let h = unsafe {
            OpenProcess(
                PROCESS_QUERY_LIMITED_INFORMATION | PROCESS_SYNCHRONIZE,
                0,
                child,
            )
        };
        if !h.is_null() {
            // SAFETY: live handle.
            let status = unsafe { WaitForSingleObject(h, 5000) };
            // SAFETY: owned handle.
            unsafe { CloseHandle(h) };
            assert_ne!(
                status, WAIT_TIMEOUT,
                "job member survived supervisor termination"
            );
        }
    }

    pub fn batch_arguments() {
        let (_guard, dir) = state_root();
        let exe = paths::path_string(&std::env::current_exe().unwrap()).unwrap();
        for ext in [".cmd", ".bat"] {
            let wrapper = paths::join2(&dir, &format!("npm wrapper 한글{ext}"));
            fs::write(&wrapper, format!("@echo off\r\n\"{exe}\" -- %*\r\n")).unwrap();
            let args: Vec<String> = [
                "",
                "space value",
                "한글",
                "a&b",
                "a|b",
                "a<b",
                "a>b",
                "(value)",
                "caret^",
                "!VALUE!",
                "trailing\\",
            ]
            .iter()
            .map(|s| (*s).to_string())
            .collect();
            let mut env: Vec<String> = std::env::vars().map(|(k, v)| format!("{k}={v}")).collect();
            env.push("CCOW_BATCH_CHILD=1".into());
            let mut c = Command {
                noop: false,
                path: wrapper.clone(),
                args: std::iter::once(wrapper.clone())
                    .chain(args.clone())
                    .collect(),
                dir: dir.clone(),
                env,
            };
            let out = process::new_exec_command(&c).unwrap().output().unwrap();
            let got: Vec<String> = serde_json::from_slice(&out.stdout).unwrap();
            assert_eq!(got, args);
            for arg in ["%PATH%", "\"&echo injected", "line\nnext"] {
                c.args = vec![wrapper.clone(), arg.into()];
                assert!(
                    process::new_exec_command(&c).is_err(),
                    "unsafe argument accepted: {arg}"
                );
            }
        }
    }

    pub fn batch_helper() -> ! {
        let args: Vec<String> = std::env::args().collect();
        if let Some(i) = args.iter().position(|a| a == "--") {
            println!("{}", serde_json::to_string(&args[i + 1..]).unwrap());
            std::process::exit(0);
        }
        std::process::exit(90);
    }
}

type Case = (&'static str, fn());

fn cases() -> Vec<Case> {
    #[allow(unused_mut, reason = "platform-specific case lists")]
    let mut cases: Vec<Case> = Vec::new();
    #[cfg(unix)]
    cases.extend([
        (
            "native_streams_and_exit",
            unix_tests::native_streams_and_exit as fn(),
        ),
        (
            "ownership_failure_never_starts_child",
            unix_tests::ownership_failure_never_starts_child,
        ),
        (
            "cancellation_waits_five_seconds",
            unix_tests::cancellation_waits_five_seconds,
        ),
        (
            "graceful_cancellation_preserves_exit",
            unix_tests::graceful_cancellation_preserves_exit,
        ),
        (
            "root_exit_drains_detached_descendant",
            unix_tests::root_exit_drains_detached_descendant,
        ),
        (
            "manager_death_still_drains",
            unix_tests::manager_death_still_drains,
        ),
        (
            "supervisor_death_blocks_recovery",
            unix_tests::supervisor_death_blocks_recovery,
        ),
        (
            "start_failure_exit_codes",
            unix_tests::start_failure_exit_codes,
        ),
        (
            "supervisor_session_hooks",
            unix_tests::supervisor_session_hooks,
        ),
        (
            "cancellation_after_ownership_finalizes_without_child",
            unix_tests::cancellation_after_ownership_finalizes_without_child,
        ),
        (
            "before_start_failure_finalizes",
            unix_tests::before_start_failure_finalizes,
        ),
        ("noop_command_finalizes", unix_tests::noop_command_finalizes),
        (
            "termination_request_drains_session",
            unix_tests::termination_request_drains_session,
        ),
    ]);
    #[cfg(target_os = "linux")]
    cases.extend([
        (
            "reaps_orphans_while_root_runs",
            unix_tests::reaps_orphans_while_root_runs as fn(),
        ),
        (
            "orphan_reaping_preserves_root_status",
            unix_tests::orphan_reaping_preserves_root_status,
        ),
    ]);
    #[cfg(windows)]
    cases.extend([
        (
            "windows_native_completion_identity",
            windows_tests::native_completion_identity as fn(),
        ),
        (
            "windows_native_streams_and_exit",
            windows_tests::native_streams_and_exit,
        ),
        (
            "windows_root_exit_drains_job_descendant",
            windows_tests::root_exit_drains_job_descendant,
        ),
        (
            "windows_supervisor_kill_recovers_job",
            windows_tests::supervisor_kill_recovers_job,
        ),
        ("windows_batch_arguments", windows_tests::batch_arguments),
    ]);
    cases
}

fn dispatch_helpers() {
    #[cfg(unix)]
    {
        if let Ok(mode) = std::env::var("CCOW_TREE_HELPER") {
            unix_tests::tree_helper(&mode);
        }
        if std::env::var("CCOW_MANAGER_HELPER").as_deref() == Ok("1") {
            unix_tests::manager_helper();
        }
    }
    #[cfg(windows)]
    {
        if std::env::var("CCOW_WINDOWS_CHILD").as_deref() == Ok("1") {
            windows_tests::child_helper();
        }
        if let Ok(mode) = std::env::var("CCOW_WINDOWS_TREE") {
            windows_tests::tree_helper(&mode);
        }
        if std::env::var("CCOW_BATCH_CHILD").as_deref() == Ok("1") {
            windows_tests::batch_helper();
        }
    }
}

fn main() {
    if let Some(code) = process::supervisor_main(test_hooks()) {
        std::process::exit(code);
    }
    dispatch_helpers();
    let filters: Vec<String> = std::env::args()
        .skip(1)
        .filter(|a| !a.starts_with('-'))
        .collect();
    if std::env::args().any(|a| a == "--list") {
        for (name, _) in cases() {
            println!("{name}: test");
        }
        return;
    }
    let selected: Vec<Case> = cases()
        .into_iter()
        .filter(|(name, _)| filters.is_empty() || filters.iter().any(|f| name.contains(f.as_str())))
        .collect();
    println!("running {} process tests", selected.len());
    let handles: Vec<_> = selected
        .into_iter()
        .map(|(name, f)| {
            let started = Instant::now();
            (
                name,
                started,
                std::thread::spawn(move || std::panic::catch_unwind(AssertUnwindSafe(f)).is_ok()),
            )
        })
        .collect();
    let mut failed = Vec::new();
    for (name, started, handle) in handles {
        let ok = handle.join().unwrap_or(false);
        println!(
            "test {name} ... {} ({:.1?})",
            if ok { "ok" } else { "FAILED" },
            started.elapsed()
        );
        if !ok {
            failed.push(name);
        }
    }
    if !failed.is_empty() {
        println!("failed: {failed:?}");
        std::process::exit(101);
    }
    println!("test result: ok");
}

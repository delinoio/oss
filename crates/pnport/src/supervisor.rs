use std::{
    ffi::OsString,
    fs,
    path::{Path, PathBuf},
    process::{Command, ExitStatus},
    sync::atomic::{AtomicI32, Ordering},
    time::{Duration, Instant},
};

use pnport::{
    diagnostic::{Code, Error, Result},
    graph::Input,
    view::View,
};

pub fn platform() -> Result<()> {
    if !cfg!(all(
        target_os = "macos",
        any(target_arch = "x86_64", target_arch = "aarch64")
    )) {
        return Err(Error::new(
            Code::PnportUnsupportedOperation,
            "This development build has no verified native interception backend for this target.",
        ));
    }
    Ok(())
}
pub fn artifact() -> Result<PathBuf> {
    platform()?;
    let directory = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(Path::to_owned))
        .ok_or_else(injection_error)?;
    let packaged = directory.join(if cfg!(target_os = "macos") {
        "libpnport_preload.dylib"
    } else {
        "libpnport_preload.so"
    });
    // Development uses the fork's crate name; release packaging keeps the
    // stable pnport companion filename checked by the existing installer.
    let development = directory.join("libfspy_preload_unix.dylib");
    let artifact = if cfg!(target_os = "macos") && development.is_file() {
        development
    } else {
        packaged
    };
    let bytes = fs::read(&artifact).map_err(|_| injection_error())?;
    if !bytes
        .windows(35)
        .any(|window| window == b"PNPORT_PRELOAD_0.1.0_FORMAT_1_READY")
    {
        return Err(injection_error());
    }
    Ok(artifact)
}
fn injection_error() -> Error {
    Error::new(
        Code::PnportInjectionFailed,
        "The matching native injection artifact is unavailable; reinstall the complete native \
         distribution.",
    )
}

fn runtime_failure(session: &Path) -> Result<Option<Error>> {
    let recorded = match fs::read(session.join("failure")) {
        Ok(recorded) => recorded,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(injection_error()),
    };
    let code = [
        Code::PnportManifestMissing,
        Code::PnportManifestInvalid,
        Code::PnportFilesystemConflict,
        Code::PnportUnsupportedOperation,
        Code::PnportInjectionFailed,
        Code::PnportArchiveCorrupt,
        Code::PnportCacheFailed,
        Code::PnportGraphChanged,
        Code::PnportCleanupFailed,
        Code::PnportCommandNotExecutable,
    ]
    .into_iter()
    .find(|code| code.as_str().as_bytes() == recorded)
    .unwrap_or(Code::PnportInjectionFailed);
    Ok(Some(Error::new(
        code,
        "Native filesystem interception reported a runtime failure; the process tree has been \
         stopped.",
    )))
}

fn pending_launches(session: &Path) -> Result<bool> {
    match fs::read_dir(session.join("pending")) {
        Ok(mut entries) => entries
            .next()
            .transpose()
            .map(|entry| entry.is_some())
            .map_err(|_| injection_error()),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(false),
        Err(_) => Err(injection_error()),
    }
}

static SIGNAL: AtomicI32 = AtomicI32::new(0);
#[cfg(unix)]
extern "C" fn signal_handler(signal: i32) {
    SIGNAL.store(signal, Ordering::SeqCst);
}

pub fn run(view: &mut View, artifact: &Path, executable: &Path, args: &[OsString]) -> Result<i32> {
    let prepared =
        pnport::executable::prepare(view, executable, args, std::env::var_os("PATH").as_deref())?;
    #[cfg(target_os = "macos")]
    let admission =
        fspy_shared_unix::spawn::admit_pnport_program(&prepared.program).map_err(|_| {
            Error::new(
                Code::PnportUnsupportedOperation,
                "The executable cannot accept macOS filesystem injection.",
            )
        })?;
    #[cfg(target_os = "macos")]
    let admitted_program = &admission.path;
    #[cfg(not(target_os = "macos"))]
    let admitted_program = prepared.program;
    let mut command = Command::new(admitted_program);
    command
        .args(&prepared.args)
        .env("PNPORT_SESSION", &view.session)
        .env("PNPORT_CACHE", &view.cache.root)
        .env_remove("PNPORT_LAUNCH_TOKEN");
    let variable = if cfg!(target_os = "macos") {
        "DYLD_INSERT_LIBRARIES"
    } else {
        "LD_PRELOAD"
    };
    if std::env::var_os(variable).is_some() {
        return Err(Error::new(
            Code::PnportUnsupportedOperation,
            "An existing native preload may conflict with pnport; start from an environment \
             without another interception library.",
        ));
    }
    #[cfg(target_os = "macos")]
    command.env(variable, artifact);
    #[cfg(not(target_os = "macos"))]
    command.env(variable, artifact);
    #[cfg(unix)]
    {
        use std::os::unix::process::CommandExt;
        command.process_group(0);
        unsafe {
            for signal in [libc::SIGINT, libc::SIGTERM, libc::SIGHUP] {
                libc::signal(signal, signal_handler as *const () as usize);
            }
        }
    }
    tracing::debug!(action = "spawn", "Starting the owned process tree");
    #[cfg(target_os = "macos")]
    admission.verify_at_launch()?;
    let mut child = command.spawn().map_err(|e| {
        Error::new(
            if e.kind() == std::io::ErrorKind::NotFound {
                Code::PnportCommandNotFound
            } else {
                Code::PnportCommandNotExecutable
            },
            "The operating system could not start the executable.",
        )
    })?;
    let pid = child.id() as i32;
    let start = Instant::now();
    let mut status = None;
    let mut root_exited_at = None;
    let mut watch = crate::input_watch::InputWatch::default();
    let mut active_markers = std::collections::HashSet::new();
    let starting = view.session.join("starting").join(pid.to_string());
    let ready = view.session.join("ready").join(pid.to_string());
    let mut initialization_started = false;
    let result = (|| loop {
        if SIGNAL.load(Ordering::SeqCst) != 0 {
            return Ok(128 + SIGNAL.load(Ordering::SeqCst));
        }
        for input in &view.graph.snapshot.inputs {
            watch.register(input)?;
        }
        view.graph.check_conflicts()?;
        if let Some(error) = runtime_failure(&view.session)? {
            return Err(error);
        }
        if let Ok(entries) = fs::read_dir(view.session.join("active")) {
            for entry in entries {
                let entry = entry.map_err(|_| injection_error())?;
                if entry.file_name().to_string_lossy().starts_with('.')
                    || !active_markers.insert(entry.path())
                {
                    continue;
                }
                let input: Input =
                    serde_json::from_slice(&fs::read(entry.path()).map_err(|_| injection_error())?)
                        .map_err(|_| injection_error())?;
                watch.register(&input)?;
            }
        }
        watch.poll()?;
        if !initialization_started && starting.is_file() {
            initialization_started = true;
            tracing::debug!(
                action = "initialization_started",
                "Native preload entered initialization"
            );
        }
        if let Some(exit) = child.try_wait().map_err(|_| injection_error())? {
            if status.is_none() {
                status = Some(exit);
                root_exited_at = Some(Instant::now());
                tracing::debug!(action="child_exit", status=?exit, "Child exited");
            }
            if !ready.is_file() {
                return Err(Error::new(
                    Code::PnportInjectionFailed,
                    "The executable did not initialize native interception; its result is not a \
                     virtualized run.",
                ));
            }
            if pending_launches(&view.session)? {
                if root_exited_at.is_some_and(|at| at.elapsed() > Duration::from_secs(5)) {
                    return Err(Error::new(
                        Code::PnportInjectionFailed,
                        "A child executable did not acknowledge native injection; its trace is \
                         incomplete.",
                    ));
                }
                std::thread::sleep(Duration::from_millis(100));
                continue;
            }
            return Ok(exit_code(exit));
        }
        if start.elapsed() > Duration::from_secs(5) && !initialization_started && !ready.is_file() {
            return Err(Error::new(
                Code::PnportInjectionFailed,
                "The executable did not acknowledge native injection; execution was stopped.",
            ));
        }
        std::thread::sleep(Duration::from_millis(100));
    })();
    #[cfg(unix)]
    unsafe {
        libc::kill(-pid, libc::SIGTERM);
        let deadline = Instant::now() + Duration::from_secs(5);
        while libc::kill(-pid, 0) == 0 && Instant::now() < deadline {
            if status.is_none() {
                status = child.try_wait().ok().flatten();
            }
            std::thread::sleep(Duration::from_millis(25));
        }
        libc::kill(-pid, libc::SIGKILL);
    }
    if status.is_none() {
        child.wait().map_err(|_| {
            Error::new(
                Code::PnportCleanupFailed,
                "Could not reap the owned executable.",
            )
        })?;
    }
    tracing::debug!(action = "cleanup", "Owned process group stopped");
    result
}
fn exit_code(status: ExitStatus) -> i32 {
    #[cfg(unix)]
    {
        use std::os::unix::process::ExitStatusExt;
        status
            .code()
            .unwrap_or_else(|| 128 + status.signal().unwrap_or(0))
    }
    #[cfg(not(unix))]
    {
        status.code().unwrap_or(125)
    }
}

#[cfg(test)]
mod failure_tests {
    use super::*;

    #[test]
    fn recorded_runtime_code_is_reported_and_invalid_marker_fails_closed() {
        let session = tempfile::tempdir().unwrap();
        assert!(runtime_failure(session.path()).unwrap().is_none());
        fs::write(
            session.path().join("failure"),
            Code::PnportArchiveCorrupt.as_str(),
        )
        .unwrap();
        assert_eq!(
            runtime_failure(session.path()).unwrap().unwrap().code,
            Code::PnportArchiveCorrupt
        );
        fs::write(session.path().join("failure"), b"unexpected").unwrap();
        assert_eq!(
            runtime_failure(session.path()).unwrap().unwrap().code,
            Code::PnportInjectionFailed
        );
    }
}

#[cfg(all(test, target_os = "macos"))]
mod tests {
    use fs2::FileExt;
    use pnport::{
        cache::{private_dir, Cache},
        graph::{Graph, Snapshot},
    };
    use serde_json::json;

    use super::*;

    fn fixture() -> (tempfile::TempDir, View, PathBuf) {
        let root = tempfile::tempdir().unwrap();
        let path = fs::canonicalize(root.path()).unwrap();
        let session = path.join("session");
        private_dir(&session).unwrap();
        let graph = Graph::from_snapshot(Snapshot {
            schema_version: 1,
            manifest_path: path.join(".pnp.cjs"),
            data: json!({
                "enableTopLevelFallback": false, "ignorePatternData": null,
                "dependencyTreeRoots": [], "fallbackPool": [], "fallbackExclusionList": [],
                "packageRegistryData": [[null, [[null, {
                    "packageLocation": "./", "packageDependencies": [], "linkType": "SOFT"
                }]]]]
            }),
            inputs: vec![],
        })
        .unwrap();
        fs::write(
            session.join("graph.json"),
            serde_json::to_vec(&graph.snapshot).unwrap(),
        )
        .unwrap();
        let cache = Cache::open(path.join("cache")).unwrap();
        let source = path.join("probe.c");
        let executable = path.join("probe");
        fs::write(&source, "int main(void) { return 0; }\n").unwrap();
        assert!(Command::new("cc")
            .arg(&source)
            .arg("-o")
            .arg(&executable)
            .status()
            .unwrap()
            .success());
        (root, View::new(graph, cache, session), executable)
    }

    #[test]
    fn cache_contention_after_initializer_entry_can_exceed_five_seconds() {
        let (_root, mut view, executable) = fixture();
        let lock = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(view.cache.root.join(".lock"))
            .unwrap();
        lock.lock_exclusive().unwrap();
        let session = view.session.clone();
        let release = std::thread::spawn(move || {
            let deadline = Instant::now() + Duration::from_secs(15);
            loop {
                if fs::read_dir(session.join("starting"))
                    .ok()
                    .is_some_and(|mut entries| entries.next().is_some())
                {
                    break;
                }
                assert!(Instant::now() < deadline, "The preload did not start.");
                std::thread::sleep(Duration::from_millis(10));
            }
            // Begin the long wait only after actual constructor entry, so a
            // slow compiler/signature check cannot shorten cache contention.
            std::thread::sleep(Duration::from_secs(6));
            assert!(
                !session.join("ready").exists(),
                "Readiness must wait for cache coordination."
            );
            drop(lock);
        });
        let result = run(&mut view, &artifact().unwrap(), &executable, &[]);
        release.join().unwrap();
        assert_eq!(result.unwrap(), 0);
        assert_eq!(fs::read_dir(view.session.join("ready")).unwrap().count(), 1);
    }

    #[test]
    fn initializer_entry_without_readiness_cannot_accept_a_child_result() {
        let (root, mut view, executable) = fixture();
        let source = root.path().join("incomplete.c");
        let library = root.path().join("incomplete.dylib");
        fs::write(
            &source,
            r#"
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <unistd.h>
__attribute__((constructor)) static void start(void) {
    char path[4096];
    snprintf(path, sizeof(path), "%s/starting", getenv("PNPORT_SESSION"));
    mkdir(path, 0700);
    snprintf(path, sizeof(path), "%s/starting/%d", getenv("PNPORT_SESSION"), getpid());
    int fd = open(path, O_WRONLY | O_CREAT, 0600);
    if (fd >= 0) close(fd);
}
"#,
        )
        .unwrap();
        assert!(Command::new("cc")
            .arg("-dynamiclib")
            .arg(&source)
            .arg("-o")
            .arg(&library)
            .status()
            .unwrap()
            .success());
        let result = run(&mut view, &library, &executable, &[]);
        assert_eq!(result.unwrap_err().code, Code::PnportInjectionFailed);
        assert_eq!(
            fs::read_dir(view.session.join("starting")).unwrap().count(),
            1
        );
    }

    #[test]
    fn unacknowledged_descendant_prevents_a_complete_result() {
        let (_root, mut view, executable) = fixture();
        fs::create_dir(view.session.join("pending")).unwrap();
        fs::write(view.session.join("pending/pnport-unacknowledged"), b"").unwrap();
        let result = run(&mut view, &artifact().unwrap(), &executable, &[]);
        assert_eq!(result.unwrap_err().code, Code::PnportInjectionFailed);
    }
}

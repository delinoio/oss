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
    let artifact = directory.join(if cfg!(target_os = "macos") {
        "libpnport_preload.dylib"
    } else {
        "libpnport_preload.so"
    });
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

static SIGNAL: AtomicI32 = AtomicI32::new(0);
#[cfg(unix)]
extern "C" fn signal_handler(signal: i32) {
    SIGNAL.store(signal, Ordering::SeqCst);
}

pub fn run(view: &mut View, artifact: &Path, executable: &Path, args: &[OsString]) -> Result<i32> {
    let prepared =
        pnport::executable::prepare(view, executable, args, std::env::var_os("PATH").as_deref())?;
    let mut command = Command::new(&prepared.program);
    command
        .args(&prepared.args)
        .env("PNPORT_SESSION", &view.session)
        .env("PNPORT_CACHE", &view.cache.root);
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
    let mut watch = crate::input_watch::InputWatch::default();
    let mut active_markers = std::collections::HashSet::new();
    let result = (|| loop {
        if SIGNAL.load(Ordering::SeqCst) != 0 {
            return Ok(128 + SIGNAL.load(Ordering::SeqCst));
        }
        for input in &view.graph.snapshot.inputs {
            watch.register(input)?;
        }
        view.graph.check_conflicts()?;
        if view.session.join("failure").exists() {
            return Err(Error::new(
                Code::PnportInjectionFailed,
                "Native filesystem interception failed; the process tree has been stopped.",
            ));
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
        if let Some(exit) = child.try_wait().map_err(|_| injection_error())? {
            status = Some(exit);
            tracing::debug!(action="child_exit", status=?exit, "Child exited");
            if !view.session.join("ready").join(pid.to_string()).is_file() {
                return Err(Error::new(
                    Code::PnportInjectionFailed,
                    "The executable did not initialize native interception; its result is not a \
                     virtualized run.",
                ));
            }
            return Ok(exit_code(exit));
        }
        if start.elapsed() > Duration::from_secs(5)
            && !view.session.join("ready").join(pid.to_string()).is_file()
        {
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

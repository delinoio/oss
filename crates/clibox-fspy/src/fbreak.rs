#[cfg(target_os = "linux")]
use std::{
    collections::BTreeSet,
    fs::{File, OpenOptions},
    io::{self, Read as _, Write as _},
    os::fd::AsRawFd as _,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
        mpsc,
    },
    thread,
};
use std::{ffi::OsString, path::PathBuf, time::Duration};

use clap::Args;

#[cfg(target_os = "linux")]
use crate::cli::{exit_code, render_path};
use crate::{
    cli::{fail, parse_duration, parse_positive},
    selector::Selector,
    trace::{self, Operation},
};

#[derive(Args)]
pub struct Fbreak {
    /// Existing root for selection; child cwd is unchanged.
    #[arg(long, value_name = "DIR")]
    root: Option<PathBuf>,
    /// Required project-relative globs; repeat to union selections.
    #[arg(long, required = true, value_name = "GLOB")]
    include: Vec<String>,
    /// Project-relative globs to exclude.
    #[arg(long, value_name = "GLOB")]
    exclude: Vec<String>,
    /// Operation kinds to break on; default is read.
    #[arg(long = "op", value_enum, value_name = "OP")]
    operations: Vec<Operation>,
    /// Optional execution budget.
    #[arg(long, value_parser = parse_duration, value_name = "DURATION")]
    timeout: Option<Duration>,
    /// Grace period before forceful descendant termination.
    #[arg(long, default_value = "5s", value_parser = parse_duration, value_name = "DURATION")]
    kill_after: Duration,
    /// Maximum traced operations.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_EVENTS, value_parser = parse_positive)]
    max_events: usize,
    /// Maximum encoded trace bytes.
    #[arg(long, default_value_t = trace::DEFAULT_MAX_BYTES, value_parser = parse_positive)]
    max_trace_bytes: usize,
    /// Command and literal arguments; use -- before COMMAND.
    #[arg(last = true, required = true, num_args = 1.., value_name = "COMMAND [ARG...]")]
    command: Vec<OsString>,
}

#[cfg(target_os = "linux")]
struct ControlTty {
    file: File,
    original: libc::termios,
    flags: i32,
}

#[cfg(target_os = "linux")]
impl ControlTty {
    fn open() -> io::Result<Self> {
        let file = OpenOptions::new().read(true).write(true).open("/dev/tty")?;
        let fd = file.as_raw_fd();
        // SAFETY: the descriptor is held by `file` for this entire operation.
        if unsafe { libc::isatty(fd) } != 1 {
            return Err(io::Error::other("control input is not a terminal"));
        }
        // SAFETY: F_GETFL reads flags from a valid descriptor.
        let flags = unsafe { libc::fcntl(fd, libc::F_GETFL) };
        if flags == -1 {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: tcgetattr writes the termios value for a valid TTY.
        let mut original = unsafe { std::mem::zeroed::<libc::termios>() };
        // SAFETY: the descriptor and output pointer are valid.
        if unsafe { libc::tcgetattr(fd, &raw mut original) } == -1 {
            return Err(io::Error::last_os_error());
        }
        let mut modified = original;
        modified.c_lflag &= !(libc::ICANON | libc::ECHO);
        modified.c_cc[libc::VMIN] = 1;
        modified.c_cc[libc::VTIME] = 0;
        // SAFETY: the new flags preserve all unrelated terminal settings.
        if unsafe { libc::tcsetattr(fd, libc::TCSANOW, &raw const modified) } == -1 {
            return Err(io::Error::last_os_error());
        }
        // SAFETY: F_SETFL retains the original flags and adds nonblocking reads.
        if unsafe { libc::fcntl(fd, libc::F_SETFL, flags | libc::O_NONBLOCK) } == -1 {
            // SAFETY: `original` was read from this TTY above.
            unsafe { libc::tcsetattr(fd, libc::TCSANOW, &raw const original) };
            return Err(io::Error::last_os_error());
        }
        Ok(Self {
            file,
            original,
            flags,
        })
    }
}

#[cfg(target_os = "linux")]
impl Drop for ControlTty {
    fn drop(&mut self) {
        let fd = self.file.as_raw_fd();
        // SAFETY: the guard still owns this TTY. Restoration is best effort
        // during unwinding and after a closed control channel.
        unsafe {
            libc::tcsetattr(fd, libc::TCSANOW, &raw const self.original);
            libc::fcntl(fd, libc::F_SETFL, self.flags);
        }
    }
}

#[expect(
    clippy::too_many_lines,
    reason = "Keep terminal ownership and traced execution together"
)]
pub fn execute(options: Fbreak) -> i32 {
    let root = match options.root {
        Some(root) => root,
        None => match std::env::current_dir() {
            Ok(root) => root,
            Err(_) => {
                return fail(
                    1,
                    "working_directory",
                    "Cannot resolve the current directory.",
                );
            }
        },
    };
    let selector = match Selector::new(&root, &options.include, &options.exclude) {
        Ok(selector) => selector,
        Err(message) => return fail(2, "invalid_selector", message),
    };
    let operations = if options.operations.is_empty() {
        vec![Operation::Read]
    } else {
        options.operations
    };
    let Some((program, arguments)) = options.command.split_first() else {
        return fail(2, "missing_command", "Provide a command after --.");
    };
    #[cfg(not(target_os = "linux"))]
    {
        let _ = (selector, operations, program, arguments);
        fail(
            1,
            "tracing_unavailable",
            "This platform has no complete file-operation backend yet.",
        )
    }
    #[cfg(target_os = "linux")]
    {
        let Ok(tty) = ControlTty::open() else {
            return fail(
                1,
                "control_tty_unavailable",
                "fbreak needs a real control terminal before launching the command.",
            );
        };
        let selected_physical: BTreeSet<PathBuf> = match selector.selected_files() {
            Ok(files) => files.into_iter().map(|file| file.physical).collect(),
            Err(_) => return fail(1, "selection_failed", "Cannot enumerate selected files."),
        };
        let cancellation = Arc::new(AtomicBool::new(false));
        let termination = Arc::new(AtomicBool::new(false));
        if signal_hook::flag::register(signal_hook::consts::SIGINT, Arc::clone(&cancellation))
            .is_err()
            || signal_hook::flag::register(signal_hook::consts::SIGTERM, Arc::clone(&cancellation))
                .is_err()
            || signal_hook::flag::register(signal_hook::consts::SIGTERM, Arc::clone(&termination))
                .is_err()
        {
            return fail(
                1,
                "signal_handler",
                "Cannot supervise cancellation signals.",
            );
        }
        let (sender, receiver) = mpsc::channel();
        let stopped = Arc::new(AtomicBool::new(false));
        let Ok(mut reader) = tty.file.try_clone() else {
            return fail(
                1,
                "control_tty_unavailable",
                "Cannot open the terminal control channel.",
            );
        };
        let reader_stopped = Arc::clone(&stopped);
        let control_thread = thread::spawn(move || {
            let mut byte = [0u8; 1];
            while !reader_stopped.load(Ordering::Relaxed) {
                match reader.read(&mut byte) {
                    Ok(1) => {
                        let command = match byte[0] {
                            b'n' => Some(crate::linux::BreakCommand::Next),
                            b'c' => Some(crate::linux::BreakCommand::Continue),
                            b'q' => Some(crate::linux::BreakCommand::Quit),
                            _ => None,
                        };
                        if let Some(command) = command
                            && (sender.send(command).is_err()
                                || matches!(
                                    command,
                                    crate::linux::BreakCommand::Continue
                                        | crate::linux::BreakCommand::Quit
                                ))
                        {
                            break;
                        }
                    }
                    Ok(0) => break,
                    Ok(_) => {}
                    Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                        thread::sleep(Duration::from_millis(10));
                    }
                    Err(error) if error.kind() == io::ErrorKind::Interrupted => {}
                    Err(_) => break,
                }
            }
        });
        let rule = |start: &trace::Start| {
            operations.contains(&start.operation)
                && start
                    .paths
                    .iter()
                    .any(|path| selector.matches_trace_path(path, &selected_physical))
        };
        let display = |start: &trace::Start| {
            let mut stderr = io::stderr().lock();
            let path = start
                .paths
                .first()
                .map_or_else(|| "<unknown>".to_owned(), render_path);
            let _ = writeln!(
                stderr,
                "break: {} {:?} pid={} tid={} (n=next, c=continue, q=quit)",
                path, start.operation, start.pid, start.tid
            );
            let _ = stderr.flush();
        };
        let control = crate::linux::BreakControl {
            rule: &rule,
            commands: &receiver,
            display: &display,
        };
        tracing::info!(command = "fbreak", stage = "start", "fspy_execution");
        let captured = crate::linux::capture(
            crate::linux::CaptureRequest {
                root: selector.root(),
                program: program.as_os_str(),
                arguments,
                child_io: crate::linux::ChildIo::Interactive,
                timeout: options.timeout,
                kill_after: options.kill_after,
                max_events: options.max_events,
                max_bytes: options.max_trace_bytes,
                delay_rule: None,
                break_control: Some(&control),
            },
            &cancellation,
        );
        stopped.store(true, Ordering::Relaxed);
        let _ = control_thread.join();
        drop(tty);
        let Ok(captured) = captured else {
            return fail(
                1,
                "tracing_unavailable",
                "Cannot launch a complete traced execution.",
            );
        };
        tracing::info!(
            command = "fbreak",
            stage = "finish",
            complete = captured.complete(),
            events = captured.events.len(),
            "fspy_execution"
        );
        if termination.load(Ordering::Relaxed) {
            return 143;
        }
        match captured.failure {
            Some(crate::linux::LinuxTraceError::Timeout) => 124,
            Some(crate::linux::LinuxTraceError::Cancelled) => 130,
            Some(crate::linux::LinuxTraceError::ControlLost) => fail(
                1,
                "control_loss",
                "The terminal control channel closed before execution finished.",
            ),
            Some(_) => fail(
                1,
                "tracing_incomplete",
                "The traced execution did not complete.",
            ),
            None => captured.root_status.map_or(1, exit_code),
        }
    }
}

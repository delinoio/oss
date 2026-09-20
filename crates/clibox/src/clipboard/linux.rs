use std::{
    io::{Read, Write},
    process::{Command, Stdio},
    sync::mpsc,
};

use x11rb::protocol::xproto::ConnectionExt;

use super::*;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Session {
    Wayland,
    X11,
}
#[derive(Default)]
pub struct Native {}
struct Output {
    success: bool,
    bytes: Vec<u8>,
    error: Vec<u8>,
}
trait Tools {
    fn invoke(&mut self, tool: &'static str, args: &[&str], input: Option<&[u8]>)
        -> Result<Output>;
    fn x11_empty(&mut self) -> Result<bool>;
}
struct System;

fn memory_directory() -> Result<tempfile::TempDir> {
    use std::{ffi::CString, os::unix::ffi::OsStrExt, path::PathBuf};
    let candidates = std::iter::once(PathBuf::from("/dev/shm"))
        .chain(std::env::var_os("XDG_RUNTIME_DIR").map(PathBuf::from));
    for path in candidates {
        let Ok(directory) = tempfile::Builder::new()
            .prefix("clibox-clipboard-")
            .tempdir_in(path)
        else {
            continue;
        };
        let Ok(path) = CString::new(directory.path().as_os_str().as_bytes()) else {
            continue;
        };
        let mut stat: libc::statfs = unsafe { std::mem::zeroed() };
        if unsafe { libc::statfs(path.as_ptr(), &mut stat) } == 0
            && i128::from(stat.f_type) == i128::from(libc::TMPFS_MAGIC)
        {
            return Ok(directory);
        }
    }
    Err(Failure::new(
        Code::UnsupportedCapability,
        "Wayland copy needs writable tmpfs-backed shared memory or a tmpfs-backed XDG runtime \
         directory; no clipboard data was written.",
    ))
}
fn session(wayland: bool, x11: bool) -> Result<Session> {
    if wayland {
        Ok(Session::Wayland)
    } else if x11 {
        Ok(Session::X11)
    } else {
        Err(Failure::new(
            Code::SessionUnavailable,
            "No desktop clipboard session; set up a Wayland or X11 session with its normal \
             display credentials.",
        ))
    }
}
fn selected() -> Result<Session> {
    session(
        std::env::var_os("WAYLAND_DISPLAY").is_some_and(|s| !s.is_empty()),
        std::env::var_os("DISPLAY").is_some_and(|s| !s.is_empty()),
    )
}
fn missing(tool: &str) -> Failure {
    Failure::new(
        Code::BackendUnavailable,
        match tool {
            "wl-copy" => {
                "Wayland clipboard copy requires wl-copy; install wl-clipboard using your system \
                 package manager."
            }
            "wl-paste" => {
                "Wayland clipboard paste requires wl-paste; install wl-clipboard using your system \
                 package manager."
            }
            _ => {
                "X11 clipboard access requires xclip; install it using your system package manager."
            }
        },
    )
}

impl Tools for System {
    fn invoke(
        &mut self,
        tool: &'static str,
        args: &[&str],
        input: Option<&[u8]>,
    ) -> Result<Output> {
        let mut command = Command::new(tool);
        runtime::detached(&mut command);
        command
            .args(args)
            .env("LC_ALL", "C")
            .env_remove("WAYLAND_DEBUG");
        // wl-copy buffers stdin in a temporary file, even with an explicit MIME
        // type. Confine it to a private verified tmpfs directory, so clipboard
        // bytes never reach persistent storage. Keep this until upstream accepts
        // an anonymous in-memory fd without copying into its own temp directory.
        // Drop removes incomplete setup files on failure or cancellation; a
        // successful tool unlinks its file before retaining the in-memory owner.
        let memory = if tool == "wl-copy" {
            Some(memory_directory()?)
        } else {
            None
        };
        if let Some(directory) = &memory {
            command.env("TMPDIR", directory.path());
        }
        if input.is_some() {
            command.stdin(Stdio::piped());
        } else {
            command.stdout(Stdio::piped()).stderr(Stdio::piped());
        }
        let mut child = command.spawn().map_err(|e| {
            if e.kind() == io::ErrorKind::NotFound {
                missing(tool)
            } else {
                Failure::io(&e)
            }
        })?;
        let pid = child.id();
        let (tx, rx) = mpsc::channel();
        if let Some(bytes) = input {
            let bytes = bytes.to_vec();
            let mut pipe = child.stdin.take().unwrap();
            std::thread::spawn(move || {
                let _ = tx.send(
                    pipe.write_all(&bytes)
                        .map(|()| (Vec::new(), Vec::new()))
                        .map_err(|e| Failure::io(&e)),
                );
            });
        } else {
            let stdout = child.stdout.take().unwrap();
            let stderr = child.stderr.take().unwrap();
            std::thread::spawn(move || {
                let error = std::thread::spawn(move || {
                    let mut bytes = Vec::new();
                    let _ = stderr.take(16384).read_to_end(&mut bytes);
                    bytes
                });
                let bytes = runtime::read_bounded(stdout, LIMIT);
                let error = error.join().unwrap_or_default();
                let _ = tx.send(bytes.map(|bytes| (bytes, error)));
            });
        }
        let mut status = None;
        let mut output = None;
        loop {
            if runtime::cancelled() {
                // Tools have their own session; cancel only unfinished tool work. A
                // successful background clipboard owner is deliberately not retained here.
                unsafe {
                    libc::kill(-(pid as i32), libc::SIGKILL);
                }
                let _ = child.wait();
                return runtime::check_cancelled().and_then(|()| unreachable!());
            }
            if status.is_none() {
                status = child.try_wait().map_err(|e| Failure::io(&e))?;
            }
            if output.is_none() {
                match rx.recv_timeout(runtime::POLL) {
                    Ok(value) => output = Some(value),
                    Err(mpsc::RecvTimeoutError::Timeout) => (),
                    Err(_) => {
                        return Err(Failure::new(
                            Code::IoFailed,
                            "Clipboard tool I/O stopped unexpectedly.",
                        ))
                    }
                }
            } else {
                std::thread::sleep(runtime::POLL);
            }
            if let Some(status) = status {
                if let Some(output) = output.take() {
                    let (bytes, error) = output?;
                    return Ok(Output {
                        success: status.success(),
                        bytes,
                        error,
                    });
                }
            }
        }
    }

    fn x11_empty(&mut self) -> Result<bool> {
        // Older xclip versions use the same failed conversion error for an empty
        // selection and non-text data. Query ownership explicitly instead of treating
        // every failed paste as empty. x11rb uses the wire protocol, with no libX11
        // link.
        let check = || {
            let (conn, _) = x11rb::connect(None).map_err(|_| ())?;
            let atom = conn
                .intern_atom(false, b"CLIPBOARD")
                .map_err(|_| ())?
                .reply()
                .map_err(|_| ())?
                .atom;
            let owner = conn
                .get_selection_owner(atom)
                .map_err(|_| ())?
                .reply()
                .map_err(|_| ())?
                .owner;
            Ok::<_, ()>(owner == x11rb::NONE)
        };
        runtime::interruptible(move || {
            check().map_err(|_| {
                Failure::new(
                    Code::SessionUnavailable,
                    "Cannot access the X11 clipboard; check DISPLAY and X authority for the \
                     current session.",
                )
            })
        })
    }
}

fn copy_tools(session: Session, tools: &mut impl Tools, text: &str) -> Result<()> {
    let output = match session {
        Session::Wayland => tools.invoke(
            "wl-copy",
            &["--type", "text/plain;charset=utf-8"],
            Some(text.as_bytes()),
        )?,
        Session::X11 => tools.invoke(
            "xclip",
            &[
                "-selection",
                "clipboard",
                "-in",
                "-target",
                "UTF8_STRING",
                "-silent",
            ],
            Some(text.as_bytes()),
        )?,
    };
    if output.success {
        Ok(())
    } else {
        Err(Failure::new(
            Code::SessionUnavailable,
            "Clipboard setup failed; check desktop access and compositor clipboard support. No \
             fallback or automatic retry was attempted.",
        ))
    }
}
fn paste_tools(session: Session, tools: &mut impl Tools) -> Result<Content> {
    if session == Session::X11 && tools.x11_empty()? {
        return Ok(Content::Empty);
    }
    let types = match session {
        Session::Wayland => tools.invoke("wl-paste", &["--list-types"], None)?,
        Session::X11 => tools.invoke(
            "xclip",
            &["-selection", "clipboard", "-out", "-target", "TARGETS"],
            None,
        )?,
    };
    if !types.success {
        if session == Session::Wayland && types.error == b"Nothing is copied\n" {
            return Ok(Content::Empty);
        }
        return Err(Failure::new(
            Code::SessionUnavailable,
            "Cannot inspect clipboard types; check session access and clipboard protocol support.",
        ));
    }
    let types = std::str::from_utf8(&types.bytes).map_err(|_| {
        Failure::new(
            Code::UnsupportedCapability,
            "Clipboard tool returned an unsupported type listing.",
        )
    })?;
    let offered: Vec<_> = types.lines().collect();
    if offered.is_empty() {
        return Ok(Content::Empty);
    }
    let target = match session {
        Session::Wayland => [
            "text/plain;charset=utf-8",
            "text/plain;charset=UTF-8",
            "UTF8_STRING",
            "text/plain",
        ]
        .into_iter()
        .find(|t| offered.contains(t)),
        Session::X11 => [
            "UTF8_STRING",
            "text/plain;charset=utf-8",
            "text/plain",
            "STRING",
        ]
        .into_iter()
        .find(|t| offered.contains(t)),
    };
    let Some(target) = target else {
        return Ok(Content::NonText);
    };
    let output = match session {
        Session::Wayland => tools.invoke("wl-paste", &["--no-newline", "--type", target], None)?,
        Session::X11 => tools.invoke(
            "xclip",
            &["-selection", "clipboard", "-out", "-target", target],
            None,
        )?,
    };
    if !output.success {
        return Err(Failure::new(
            Code::ClipboardChanged,
            "Clipboard text could not be read; it may have changed or become inaccessible.",
        ));
    }
    // ICCCM STRING is Latin-1, unlike UTF8_STRING. Convert its defined encoding
    // explicitly, then let the common layer enforce the UTF-8 byte limit.
    let bytes = if session == Session::X11 && target == "STRING" {
        output
            .bytes
            .into_iter()
            .map(char::from)
            .collect::<String>()
            .into_bytes()
    } else {
        output.bytes
    };
    Ok(Content::Text(bytes))
}
impl Backend for Native {
    fn copy(&mut self, text: &str) -> Result<()> {
        copy_tools(selected()?, &mut System, text)
    }

    fn paste(&mut self) -> Result<Content> {
        paste_tools(selected()?, &mut System)
    }
}

#[cfg(test)]
mod tests {
    use std::collections::VecDeque;

    use super::*;
    #[derive(Default)]
    struct Fake {
        outputs: VecDeque<Output>,
        calls: Vec<(String, Vec<String>, Option<Vec<u8>>)>,
        empty: bool,
    }
    impl Tools for Fake {
        fn invoke(
            &mut self,
            tool: &'static str,
            args: &[&str],
            input: Option<&[u8]>,
        ) -> Result<Output> {
            self.calls.push((
                tool.into(),
                args.iter().map(|s| s.to_string()).collect(),
                input.map(<[u8]>::to_vec),
            ));
            self.outputs.pop_front().ok_or_else(|| missing(tool))
        }

        fn x11_empty(&mut self) -> Result<bool> {
            Ok(self.empty)
        }
    }
    fn output(bytes: &[u8]) -> Output {
        Output {
            success: true,
            bytes: bytes.to_vec(),
            error: Vec::new(),
        }
    }
    #[test]
    fn session_priority_and_headless() {
        assert_eq!(session(true, true).unwrap(), Session::Wayland);
        assert_eq!(session(false, true).unwrap(), Session::X11);
        assert!(session(false, false).is_err());
    }
    #[test]
    fn copy_content_is_stdin_and_owner_runs_in_background() {
        for session in [Session::X11, Session::Wayland] {
            let mut fake = Fake {
                outputs: [output(b"")].into(),
                ..Default::default()
            };
            copy_tools(session, &mut fake, "PRIVATE\r\n").unwrap();
            assert_eq!(fake.calls.len(), 1);
            assert_eq!(fake.calls[0].2.as_deref(), Some(&b"PRIVATE\r\n"[..]));
            assert!(!fake.calls[0]
                .1
                .iter()
                .any(|a| a.contains("PRIVATE") || a == "--foreground" || a == "--paste-once"));
        }
    }
    #[test]
    fn empty_nontext_and_no_added_newline() {
        let mut fake = Fake {
            empty: true,
            ..Default::default()
        };
        assert!(matches!(
            paste_tools(Session::X11, &mut fake).unwrap(),
            Content::Empty
        ));
        assert!(fake.calls.is_empty());
        let mut fake = Fake {
            outputs: [Output {
                success: false,
                bytes: vec![],
                error: b"Nothing is copied\n".to_vec(),
            }]
            .into(),
            ..Default::default()
        };
        assert!(matches!(
            paste_tools(Session::Wayland, &mut fake).unwrap(),
            Content::Empty
        ));
        let mut fake = Fake {
            outputs: [output(b"image/png\n")].into(),
            ..Default::default()
        };
        assert!(matches!(
            paste_tools(Session::Wayland, &mut fake).unwrap(),
            Content::NonText
        ));
        let mut fake = Fake {
            outputs: [output(b"text/plain;charset=utf-8\n"), output(b"text\r\n")].into(),
            ..Default::default()
        };
        assert!(
            matches!(paste_tools(Session::Wayland, &mut fake).unwrap(), Content::Text(t) if t == b"text\r\n")
        );
        assert!(fake.calls[1].1.contains(&"--no-newline".into()));
    }
    #[test]
    fn tool_failure_does_not_fallback_or_echo_stderr() {
        let mut fake = Fake {
            outputs: [Output {
                success: false,
                bytes: vec![],
                error: b"PRIVATE /secret/file".to_vec(),
            }]
            .into(),
            ..Default::default()
        };
        let error = paste_tools(Session::Wayland, &mut fake).err().unwrap();
        assert!(!error.message.contains("PRIVATE"));
        assert_eq!(fake.calls.len(), 1);
        assert_eq!(
            copy_tools(Session::Wayland, &mut Fake::default(), "text")
                .unwrap_err()
                .code,
            Code::BackendUnavailable
        );
    }
}

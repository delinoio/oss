use std::{
    ffi::{OsStr, OsString},
    path::{Path, PathBuf},
    process::{Child, Command},
};

use crate::{
    error::{Code, Failure, Result},
    runtime,
};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Platform {
    Mac,
    Windows,
    Linux,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum Mode {
    Dispatch,
    Process,
    MacApplication,
}
struct Request {
    program: Option<OsString>,
    args: Vec<OsString>,
    mode: Mode,
}

fn is_uri(target: &OsStr) -> bool {
    let Some(text) = target.to_str() else {
        return false;
    };
    let Some((scheme, _)) = text.split_once(':') else {
        return false;
    };
    if cfg!(windows) && scheme.len() == 1 {
        return false;
    }
    let mut chars = scheme.bytes();
    chars.next().is_some_and(|b| b.is_ascii_alphabetic())
        && chars.all(|b| b.is_ascii_alphanumeric() || matches!(b, b'+' | b'-' | b'.'))
}
fn target(target: OsString) -> Result<OsString> {
    if !target.is_empty() && is_uri(&target) && !Path::new(&target).exists() {
        return Ok(target);
    }
    if target.is_empty() {
        return Err(Failure::new(
            Code::InvalidInput,
            "An open target is required.",
        ));
    }
    let path = PathBuf::from(target);
    let path = if path.is_absolute() {
        path
    } else {
        std::env::current_dir()
            .map_err(|e| Failure::io(&e))?
            .join(path)
    };
    std::fs::metadata(&path).map_err(|_| {
        Failure::new(
            Code::LaunchFailed,
            "The local open target is missing or inaccessible; check the file or directory.",
        )
    })?;
    Ok(path.into_os_string())
}

fn request(
    platform: Platform,
    target: OsString,
    app: Option<OsString>,
    wait: bool,
) -> Result<Request> {
    if wait && app.is_none() {
        return Err(Failure::new(
            Code::InvalidInput,
            "--wait requires --app on every platform.",
        ));
    }
    if app.as_ref().is_some_and(|s| s.is_empty()) {
        return Err(Failure::new(
            Code::InvalidInput,
            "--app requires an application name or executable path.",
        ));
    }
    if wait && platform != Platform::Mac {
        let app = app.as_ref().unwrap().to_string_lossy().to_ascii_lowercase();
        let base = app.rsplit(['/', '\\']).next().unwrap_or("");
        if matches!(
            base,
            "xdg-open" | "gio" | "explorer" | "explorer.exe" | "rundll32" | "rundll32.exe"
        ) || (platform == Platform::Windows
            && (base.ends_with(".cmd") || base.ends_with(".bat")))
        {
            return Err(Failure::new(
                Code::UnsupportedCapability,
                "This application delegates opening and cannot provide a trackable application \
                 wait; choose the actual application executable.",
            ));
        }
    }
    Ok(match platform {
        Platform::Mac => {
            let mut args = Vec::new();
            if let Some(app) = app {
                args.extend([OsString::from("-a"), app]);
            }
            if wait {
                args.push("-W".into());
            }
            args.extend([OsString::from("--"), target]);
            Request {
                program: Some("/usr/bin/open".into()),
                args,
                mode: if wait {
                    Mode::MacApplication
                } else {
                    Mode::Dispatch
                },
            }
        }
        Platform::Linux | Platform::Windows => {
            let explicit = app.is_some();
            Request {
                program: app.or_else(|| (platform == Platform::Linux).then(|| "xdg-open".into())),
                args: vec![target],
                mode: if explicit {
                    Mode::Process
                } else {
                    Mode::Dispatch
                },
            }
        }
    })
}
trait Backend {
    type Handle;
    fn launch(&mut self, request: &Request) -> Result<Self::Handle>;
    fn wait(&mut self, handle: &mut Self::Handle, mode: Mode) -> Result<()>;
}
fn launch(backend: &mut impl Backend, request: Request, wait: bool) -> Result<()> {
    runtime::check_cancelled()?;
    let mut handle = backend.launch(&request)?;
    if wait || request.mode == Mode::Dispatch {
        backend.wait(&mut handle, request.mode)?;
    }
    Ok(())
}
struct Native;
impl Backend for Native {
    type Handle = Option<Child>;

    fn launch(&mut self, request: &Request) -> Result<Self::Handle> {
        if let Some(program) = &request.program {
            let mut command = Command::new(program);
            runtime::detached(&mut command);
            command.args(&request.args);
            command.spawn().map(Some).map_err(|e| {
                if e.kind() == std::io::ErrorKind::NotFound {
                    Failure::new(
                        Code::BackendUnavailable,
                        "Open launcher or application was not found; install the selected \
                         application or Linux xdg-utils and check PATH.",
                    )
                } else {
                    Failure::new(
                        Code::LaunchFailed,
                        "Could not start the application; check installation, executable \
                         permissions and desktop-session access.",
                    )
                }
            })
        } else {
            #[cfg(windows)]
            {
                shell_open(&request.args[0])?;
                Ok(None)
            }
            #[cfg(not(windows))]
            {
                Err(Failure::new(
                    Code::UnsupportedCapability,
                    "No opening backend is available on this platform.",
                ))
            }
        }
    }

    fn wait(&mut self, handle: &mut Self::Handle, mode: Mode) -> Result<()> {
        let Some(child) = handle else {
            return if mode == Mode::Dispatch {
                Ok(())
            } else {
                Err(Failure::new(
                    Code::WaitUnavailable,
                    "Application tracking is unavailable; the application may already have \
                     opened. Do not retry automatically.",
                ))
            };
        };
        // macOS open -W is a waiter, not the application. Cancelling may terminate
        // that helper only; direct application processes are always left running.
        let status = runtime::wait_child(child, mode == Mode::MacApplication)?;
        if status.success() {
            Ok(())
        } else {
            Err(Failure::new(
                Code::LaunchFailed,
                "The application or open launcher exited unsuccessfully; the resource may already \
                 have opened.",
            ))
        }
    }
}

#[cfg(windows)]
fn shell_open(target: &OsStr) -> Result<()> {
    use std::{os::windows::ffi::OsStrExt, ptr};

    use windows_sys::Win32::{
        Foundation::CloseHandle,
        System::Com::*,
        UI::{Shell::*, WindowsAndMessaging::SW_SHOWNORMAL},
    };
    let target: Vec<_> = target.encode_wide().chain(Some(0)).collect();
    unsafe {
        let initialized = CoInitializeEx(
            ptr::null(),
            COINIT_APARTMENTTHREADED as u32 | COINIT_DISABLE_OLE1DDE as u32,
        );
        if initialized < 0 {
            return Err(Failure::new(
                Code::LaunchFailed,
                "Windows application dispatch could not initialize.",
            ));
        }
        let mut info: SHELLEXECUTEINFOW = std::mem::zeroed();
        info.cbSize = std::mem::size_of_val(&info) as u32;
        info.fMask = SEE_MASK_NOCLOSEPROCESS | SEE_MASK_NOASYNC | SEE_MASK_FLAG_NO_UI;
        info.lpFile = target.as_ptr();
        info.nShow = SW_SHOWNORMAL;
        let success = ShellExecuteExW(&mut info) != 0;
        if !info.hProcess.is_null() {
            CloseHandle(info.hProcess);
        }
        CoUninitialize();
        if success {
            Ok(())
        } else {
            Err(Failure::new(
                Code::LaunchFailed,
                "Windows could not dispatch the resource; check the registered application and \
                 desktop-session permissions.",
            ))
        }
    }
}

pub fn execute(resource: OsString, app: Option<OsString>, wait: bool) -> Result<()> {
    let platform = if cfg!(target_os = "macos") {
        Platform::Mac
    } else if cfg!(windows) {
        Platform::Windows
    } else {
        Platform::Linux
    };
    let request = request(platform, target(resource)?, app, wait)?;
    tracing::debug!(
        operation = "open",
        backend = std::env::consts::OS,
        wait,
        "Resource dispatch started"
    );
    launch(&mut Native, request, wait)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[derive(Default)]
    struct Fake {
        launched: usize,
        waited: usize,
        fail_wait: bool,
        fail_launch: bool,
    }
    impl Backend for Fake {
        type Handle = ();

        fn launch(&mut self, _: &Request) -> Result<()> {
            self.launched += 1;
            if self.fail_launch {
                Err(Failure::new(Code::LaunchFailed, "Launch failed."))
            } else {
                Ok(())
            }
        }

        fn wait(&mut self, _: &mut (), _: Mode) -> Result<()> {
            self.waited += 1;
            if self.fail_wait {
                Err(Failure::new(
                    Code::WaitUnavailable,
                    "Application may already have opened.",
                ))
            } else {
                Ok(())
            }
        }
    }
    #[test]
    fn preserves_one_target_and_requires_explicit_app() {
        for platform in [Platform::Mac, Platform::Windows, Platform::Linux] {
            assert!(request(platform, "https://private/path?q=a&b=c".into(), None, true).is_err());
            let r = request(
                platform,
                "custom:opaque value;echo PRIVATE".into(),
                Some("app with spaces".into()),
                true,
            )
            .unwrap();
            assert_eq!(r.args.last().unwrap(), "custom:opaque value;echo PRIVATE");
            let mut fake = Fake::default();
            launch(&mut fake, r, true).unwrap();
            assert_eq!((fake.launched, fake.waited), (1, 1));
        }
    }
    #[test]
    fn unsupported_wait_rejected_before_dispatch() {
        for (platform, app) in [
            (Platform::Linux, "xdg-open"),
            (Platform::Windows, "explorer.exe"),
            (Platform::Windows, "app.cmd"),
        ] {
            assert!(request(platform, "target".into(), Some(app.into()), true).is_err());
        }
    }
    #[test]
    fn dispatch_and_tracking_failure_are_not_retried() {
        let mut fake = Fake {
            fail_wait: true,
            ..Default::default()
        };
        let r = request(
            Platform::Windows,
            "uri:test".into(),
            Some("app".into()),
            true,
        )
        .unwrap();
        assert_eq!(
            launch(&mut fake, r, true).unwrap_err().code,
            Code::WaitUnavailable
        );
        assert_eq!(fake.launched, 1);
        let r = request(
            Platform::Linux,
            "uri:test".into(),
            Some("app".into()),
            false,
        )
        .unwrap();
        launch(&mut fake, r, false).unwrap();
        assert_eq!(fake.waited, 1);
        let r = request(Platform::Mac, "uri:test".into(), None, false).unwrap();
        let mut fake = Fake {
            fail_launch: true,
            ..Default::default()
        };
        assert!(launch(&mut fake, r, false).is_err());
        assert_eq!(fake.waited, 0);
    }
    #[test]
    fn relative_paths_starting_with_options_become_absolute() {
        let path = tempfile::Builder::new()
            .prefix("-clibox-")
            .tempfile()
            .unwrap();
        assert!(Path::new(&target(path.path().as_os_str().into()).unwrap()).is_absolute());
        assert!(is_uri(OsStr::new("some+scheme:a b")));
        assert!(!is_uri(OsStr::new("./file")));
    }
}

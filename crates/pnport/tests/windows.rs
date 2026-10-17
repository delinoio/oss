// SPDX-License-Identifier: Apache-2.0
#![cfg(windows)]

use std::{
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    time::{Duration, Instant},
};

use serde_json::json;

const MODE: &str = "PNPORT_NATIVE_WINDOWS_FIXTURE";
const PROJECT: &str = "PNPORT_NATIVE_WINDOWS_PROJECT";
const WORK: &str = "PNPORT_NATIVE_WINDOWS_WORK";
const REPORT: &str = "PNPORT_NATIVE_WINDOWS_REPORT";

#[derive(Clone, Copy, Debug)]
enum Api {
    Ansi,
    Wide,
}

/// Calls the native `CreateProcess` entry point directly so the application
/// string, command line and `lpCurrentDirectory` are exactly the requested
/// values. Returns the worker exit code, or the Win32 error when creation
/// fails.
fn create_process(
    api: Api,
    application: Option<&str>,
    command: &str,
    directory: Option<&Path>,
) -> Result<u32, u32> {
    use std::{ffi::CString, os::windows::ffi::OsStrExt, ptr};

    use windows_sys::Win32::{
        Foundation::{CloseHandle, GetLastError},
        System::Threading::{
            CreateProcessA, CreateProcessW, GetExitCodeProcess, WaitForSingleObject,
            PROCESS_INFORMATION, STARTUPINFOA, STARTUPINFOW,
        },
    };
    let mut info = PROCESS_INFORMATION::default();
    let created = match api {
        Api::Wide => {
            let text = |value: &str| value.encode_utf16().chain([0]).collect::<Vec<u16>>();
            let application = application.map(text);
            let mut command = text(command);
            let directory = directory.map(|value| {
                value
                    .as_os_str()
                    .encode_wide()
                    .chain([0])
                    .collect::<Vec<u16>>()
            });
            let startup = STARTUPINFOW {
                cb: std::mem::size_of::<STARTUPINFOW>() as u32,
                ..Default::default()
            };
            unsafe {
                CreateProcessW(
                    application
                        .as_ref()
                        .map_or(ptr::null(), |value| value.as_ptr()),
                    command.as_mut_ptr(),
                    ptr::null(),
                    ptr::null(),
                    0,
                    0,
                    ptr::null(),
                    directory
                        .as_ref()
                        .map_or(ptr::null(), |value| value.as_ptr()),
                    &startup,
                    &mut info,
                )
            }
        }
        Api::Ansi => {
            let application = application.map(|value| CString::new(value).unwrap());
            let mut command = CString::new(command).unwrap().into_bytes_with_nul();
            let directory = directory.map(|value| CString::new(value.to_str().unwrap()).unwrap());
            let startup = STARTUPINFOA {
                cb: std::mem::size_of::<STARTUPINFOA>() as u32,
                ..Default::default()
            };
            unsafe {
                CreateProcessA(
                    application
                        .as_ref()
                        .map_or(ptr::null(), |value| value.as_ptr().cast()),
                    command.as_mut_ptr(),
                    ptr::null(),
                    ptr::null(),
                    0,
                    0,
                    ptr::null(),
                    directory
                        .as_ref()
                        .map_or(ptr::null(), |value| value.as_ptr().cast()),
                    &startup,
                    &mut info,
                )
            }
        }
    };
    if created == 0 {
        return Err(unsafe { GetLastError() });
    }
    let waited = unsafe { WaitForSingleObject(info.hProcess, 30_000) };
    let mut code = u32::MAX;
    let queried = unsafe { GetExitCodeProcess(info.hProcess, &mut code) };
    unsafe {
        CloseHandle(info.hProcess);
        CloseHandle(info.hThread);
    }
    assert_eq!(waited, 0, "{api:?} worker did not exit");
    assert_ne!(queried, 0, "{api:?} worker exit code unavailable");
    Ok(code)
}

/// Launches the report worker and returns the working directory and image
/// path that the worker observed about itself.
fn launch_worker(
    api: Api,
    work: &Path,
    application: Option<&str>,
    command: &str,
    directory: Option<&Path>,
) -> Result<(PathBuf, PathBuf), u32> {
    let report = work.join("report.txt");
    let _ = fs::remove_file(&report);
    std::env::set_var(MODE, "report");
    std::env::set_var(REPORT, &report);
    let code = create_process(api, application, command, directory)?;
    assert_eq!(code, 0, "{api:?} report worker failed with {code}");
    let text = fs::read_to_string(&report).unwrap();
    let mut lines = text.lines();
    let cwd = PathBuf::from(lines.next().unwrap());
    let image = PathBuf::from(lines.next().unwrap());
    Ok((cwd, image))
}

fn binary() -> PathBuf {
    std::env::var_os("PNPORT_TEST_BINARY")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(env!("CARGO_BIN_EXE_pnport")))
}
fn library(directory: &Path) -> Vec<u8> {
    if let Some(path) = std::env::var_os("PNPORT_TEST_DLL") {
        return fs::read(path).unwrap();
    }
    let target = if cfg!(target_arch = "aarch64") {
        "aarch64-pc-windows-msvc"
    } else {
        "x86_64-pc-windows-msvc"
    };
    let compiler = cc::Build::new()
        .target(target)
        .host(target)
        .opt_level(0)
        .get_compiler();
    let output = directory.join("loader.dll");
    let status = compiler
        .to_command()
        .args(["/nologo", "/LD", "/O2"])
        .arg(Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/windows-loader.c"))
        .arg(format!("/Fo{}", directory.join("loader.obj").display()))
        .arg(format!("/Fe{}", output.display()))
        .args(["/link", "/NOENTRY", "/NODEFAULTLIB"])
        .current_dir(directory)
        .status()
        .unwrap();
    assert!(status.success(), "native DLL fixture compilation failed");
    fs::read(output).unwrap()
}
fn fixture(split: bool) -> tempfile::TempDir {
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().join("project 한글 space");
    fs::create_dir(&root).unwrap();
    fs::write(root.join("source.txt"), b"source").unwrap();
    let library = library(temp.path());
    let executable = fs::read(std::env::current_exe().unwrap()).unwrap();
    let archive = fs::File::create(root.join("dependency.zip")).unwrap();
    let mut archive = zip::ZipWriter::new(archive);
    for (name, bytes) in [
        (
            "node_modules/example/package.json",
            br#"{"name":"example","bin":{"native-fixture":"native.exe"}}"#.as_slice(),
        ),
        ("node_modules/example/data.txt", b"dependency".as_slice()),
        ("node_modules/example/loader.dll", library.as_slice()),
        ("node_modules/example/native.exe", executable.as_slice()),
    ] {
        archive
            .start_file(
                name,
                zip::write::SimpleFileOptions::default()
                    .compression_method(zip::CompressionMethod::Deflated),
            )
            .unwrap();
        archive.write_all(bytes).unwrap();
    }
    archive.finish().unwrap();
    let data = json!({"__info": [], "dependencyTreeRoots": [{"name":"root","reference":"workspace:."}], "enableTopLevelFallback":false, "ignorePatternData":null,"fallbackExclusionList":[],"fallbackPool":[],"packageRegistryData":[[null,[[null,{"packageLocation":"./","packageDependencies":[["example","npm:1.0.0"]],"linkType":"SOFT"}]]],["root",[["workspace:.",{"packageLocation":"./","packageDependencies":[["example","npm:1.0.0"]],"linkType":"SOFT"}]]],["example",[["npm:1.0.0",{"packageLocation":"./dependency.zip/node_modules/example/","packageDependencies":[],"linkType":"HARD"}]]]]});
    if split {
        fs::write(
            root.join(".pnp.cjs"),
            "const pnpDataFilepath = path.resolve(__dirname, \".pnp.data.json\");\n",
        )
        .unwrap();
        fs::write(
            root.join(".pnp.data.json"),
            serde_json::to_vec(&data).unwrap(),
        )
        .unwrap();
    } else {
        let escaped = serde_json::to_string(&data)
            .unwrap()
            .replace('\\', "\\\\")
            .replace('\'', "\\'");
        fs::write(
            root.join(".pnp.cjs"),
            format!("const RAW_RUNTIME_STATE = '{escaped}';\n"),
        )
        .unwrap();
    }
    temp
}
fn run(root: &Path, cache: &Path, mode: &str) -> Command {
    let mut command = Command::new(binary());
    command
        .arg("--project")
        .arg(root)
        .arg("--cache-dir")
        .arg(cache)
        .args(["--color=never", "--log-level=debug", "run", "--"])
        .arg(std::env::current_exe().unwrap())
        .args(["--exact", "native_child", "--nocapture"])
        .env(MODE, mode)
        .env(PROJECT, root)
        .current_dir(root);
    command
}

#[test]
fn native_child() {
    let Some(mode) = std::env::var_os(MODE) else {
        return;
    };
    let root = PathBuf::from(std::env::var_os(PROJECT).unwrap());
    if mode == "read" {
        let dependency = root.join("node_modules/example/data.txt");
        assert_eq!(fs::read(&dependency).unwrap(), b"dependency");
        assert_eq!(fs::metadata(&dependency).unwrap().len(), 10);
        assert_eq!(
            fs::read(root.join("NODE_MODULES/EXAMPLE/DATA.TXT")).unwrap(),
            b"dependency"
        );
        let mut original = fs::File::open(&dependency).unwrap();
        let mut duplicate = original.try_clone().unwrap();
        let mut first = [0; 4];
        duplicate.read_exact(&mut first).unwrap();
        let mut remaining = Vec::new();
        original.read_to_end(&mut remaining).unwrap();
        assert_eq!(
            [first.as_slice(), remaining.as_slice()].concat(),
            b"dependency"
        );
        assert!(fs::write(&dependency, b"forbidden").is_err());
        assert!(fs::remove_file(&dependency).is_err());
        assert!(fs::rename(root.join("source.txt"), &dependency).is_err());
        fs::write(root.join("output.txt"), b"output").unwrap();
        let root_entries: Vec<_> = fs::read_dir(&root)
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert_eq!(
            root_entries
                .iter()
                .filter(|name| *name == "node_modules")
                .count(),
            1
        );
        let deps: Vec<_> = fs::read_dir(root.join("node_modules"))
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert!(deps.iter().any(|name| name == "example"));
        let canonical = fs::canonicalize(&dependency).unwrap();
        assert!(!canonical.to_string_lossy().contains("/v1/"));
        assert!(canonical.to_string_lossy().contains("dependency.zip"));
    } else if mode == "spawn" {
        // Retain the OS/runtime DLL search prerequisites of this host while
        // replacing every pnport/Node option and all other inherited variables.
        let output = Command::new(std::env::current_exe().unwrap())
            .args(["--exact", "native_child", "--nocapture"])
            .env_clear()
            .env("SystemRoot", std::env::var_os("SystemRoot").unwrap())
            .env("PATH", std::env::var_os("PATH").unwrap())
            .env(MODE, "read")
            .env(PROJECT, &root)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "descendant failed {:?}: {} {}",
            output.status.code(),
            String::from_utf8_lossy(&output.stderr),
            String::from_utf8_lossy(&output.stdout)
        );
    } else if mode == "loader" {
        use windows_sys::Win32::{
            Foundation::FreeLibrary,
            System::LibraryLoader::{GetProcAddress, LoadLibraryW},
        };
        let path =
            pnport_core::windows::wide(&root.join("node_modules/example/loader.dll")).unwrap();
        let module = unsafe { LoadLibraryW(path.as_ptr()) };
        assert!(
            !module.is_null(),
            "ZIP library load failed: {}",
            std::io::Error::last_os_error()
        );
        let value =
            unsafe { GetProcAddress(module, c"pnport_fixture_value".as_ptr().cast()) }.unwrap();
        let value: unsafe extern "C" fn() -> i32 = unsafe { std::mem::transmute(value) };
        assert_eq!(unsafe { value() }, 42);
        assert_ne!(unsafe { FreeLibrary(module) }, 0);
    } else if mode == "mmap" {
        use std::os::windows::io::AsRawHandle;

        use windows_sys::Win32::{
            Foundation::CloseHandle,
            System::Memory::{
                CreateFileMappingW, MapViewOfFile, UnmapViewOfFile, FILE_MAP_READ, PAGE_READONLY,
            },
        };
        let file = fs::File::open(root.join("node_modules/example/data.txt")).unwrap();
        let mapping = unsafe {
            CreateFileMappingW(
                file.as_raw_handle(),
                std::ptr::null(),
                PAGE_READONLY,
                0,
                0,
                std::ptr::null(),
            )
        };
        assert!(!mapping.is_null());
        let view = unsafe { MapViewOfFile(mapping, FILE_MAP_READ, 0, 0, 0) };
        assert!(!view.Value.is_null());
        assert_eq!(
            unsafe { std::slice::from_raw_parts(view.Value.cast::<u8>(), 10) },
            b"dependency"
        );
        unsafe {
            UnmapViewOfFile(view);
            CloseHandle(mapping);
        }
    } else if mode == "watch" {
        use windows_sys::Win32::{
            Foundation::{CloseHandle, INVALID_HANDLE_VALUE},
            Storage::FileSystem::{
                CreateFileW, ReadDirectoryChangesW, FILE_FLAG_BACKUP_SEMANTICS,
                FILE_FLAG_OVERLAPPED, FILE_LIST_DIRECTORY, FILE_NOTIFY_CHANGE_LAST_WRITE,
                FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE, OPEN_EXISTING,
            },
            System::{
                Threading::{CreateEventW, WaitForSingleObject},
                IO::{GetOverlappedResult, OVERLAPPED},
            },
        };
        let path = pnport_core::windows::wide(&root).unwrap();
        let directory = unsafe {
            CreateFileW(
                path.as_ptr(),
                FILE_LIST_DIRECTORY,
                FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                std::ptr::null(),
                OPEN_EXISTING,
                FILE_FLAG_BACKUP_SEMANTICS | FILE_FLAG_OVERLAPPED,
                std::ptr::null_mut(),
            )
        };
        assert_ne!(directory, INVALID_HANDLE_VALUE);
        let event = unsafe { CreateEventW(std::ptr::null(), 1, 0, std::ptr::null()) };
        assert!(!event.is_null());
        let mut overlapped = OVERLAPPED::default();
        overlapped.hEvent = event;
        let mut buffer = vec![0u32; 1024];
        let mut bytes = 0;
        let started = unsafe {
            ReadDirectoryChangesW(
                directory,
                buffer.as_mut_ptr().cast(),
                (buffer.len() * 4) as u32,
                0,
                FILE_NOTIFY_CHANGE_LAST_WRITE,
                &mut bytes,
                &mut overlapped,
                None,
            )
        };
        assert_ne!(started, 0, "native watch registration failed");
        fs::write(root.join("source.txt"), b"source").unwrap();
        assert_eq!(
            unsafe { WaitForSingleObject(event, 5000) },
            0,
            "source watch did not complete"
        );
        assert_ne!(
            unsafe { GetOverlappedResult(directory, &overlapped, &mut bytes, 0) },
            0
        );
        assert!(bytes > 0);
        unsafe {
            CloseHandle(directory);
            CloseHandle(event);
        }
    } else if mode == "cache" {
        fs::create_dir_all(root.join("node_modules/.cache/tool")).unwrap();
        fs::write(root.join("node_modules/.cache/tool/value"), b"cache").unwrap();
        assert_eq!(
            fs::read(root.join("node_modules/example/data.txt")).unwrap(),
            b"dependency"
        );
        let names: Vec<_> = fs::read_dir(root.join("node_modules"))
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert!(names.iter().any(|name| name == ".cache"));
        assert!(names.iter().any(|name| name == "example"));
        assert!(fs::remove_dir(root.join("node_modules")).is_err());
    } else if mode == "report" {
        let report = PathBuf::from(std::env::var_os(REPORT).unwrap());
        fs::write(
            report,
            format!(
                "{}\n{}\n",
                std::env::current_dir().unwrap().display(),
                std::env::current_exe().unwrap().display()
            ),
        )
        .unwrap();
    } else if mode == "application" {
        let work = PathBuf::from(std::env::var_os(WORK).unwrap());
        let caller = work.join("caller");
        let child = work.join("child");
        std::env::set_current_dir(&caller).unwrap();
        let same = |left: &Path, right: &Path| {
            fs::canonicalize(left).unwrap() == fs::canonicalize(right).unwrap()
        };
        let image = caller.join("bin").join("worker.exe");
        let decoy = child.join("bin").join("worker.exe");
        let image_text = image.to_str().unwrap();
        assert_eq!(image_text.as_bytes()[1], b':', "fixture needs a drive path");
        let worker = "worker.exe --exact native_child --nocapture";
        let arguments = " --exact native_child --nocapture";
        for api in [Api::Wide, Api::Ansi] {
            // A partial application resolves against the caller directory
            // while the worker still starts in the requested directory.
            let requested = [
                (r"bin\worker.exe".to_owned(), "relative"),
                ("bin/worker.exe".to_owned(), "forward-slash relative"),
                (image_text[2..].to_owned(), "root-relative"),
                (
                    format!("{}:bin\\worker.exe", &image_text[..1]),
                    "drive-relative",
                ),
            ];
            for (application, label) in requested {
                let (cwd, launched) =
                    launch_worker(api, &work, Some(&application), worker, Some(&child))
                        .unwrap_or_else(|error| {
                            panic!("{api:?} {label} application failed with {error}")
                        });
                assert!(same(&launched, &image), "{api:?} {label}: {launched:?}");
                assert!(!same(&launched, &decoy), "{api:?} {label} chose the child");
                assert!(same(&cwd, &child), "{api:?} {label} cwd {cwd:?}");
            }
            // Without a requested directory the worker inherits the caller's.
            let (cwd, launched) =
                launch_worker(api, &work, Some(r"bin\worker.exe"), worker, None).unwrap();
            assert!(same(&launched, &image), "{api:?} null cwd: {launched:?}");
            assert!(same(&cwd, &caller), "{api:?} null cwd observed {cwd:?}");
            // A relative image that exists only below the child directory is
            // not found from the caller directory.
            let missing = launch_worker(
                api,
                &work,
                Some(r"bin\only-child.exe"),
                worker,
                Some(&child),
            );
            assert!(missing.is_err(), "{api:?} selected an image in the child");
            assert!(
                !work.join("report.txt").exists(),
                "{api:?} started a worker for a missing caller image"
            );
            // Controls: absolute applications are never rebased, and a null
            // application keeps native command-line resolution.
            let absolute = decoy.to_str().unwrap();
            let (cwd, launched) =
                launch_worker(api, &work, Some(absolute), worker, Some(&child)).unwrap();
            assert!(same(&launched, &decoy), "{api:?} absolute: {launched:?}");
            assert!(same(&cwd, &child), "{api:?} absolute cwd {cwd:?}");
            let (cwd, launched) = launch_worker(
                api,
                &work,
                None,
                &format!(r"bin\worker.exe{arguments}"),
                Some(&child),
            )
            .unwrap();
            assert!(same(&launched, &image), "{api:?} null app: {launched:?}");
            assert!(same(&cwd, &child), "{api:?} null app cwd {cwd:?}");
        }
    } else if mode == "completion" {
        completion_port_directory_queries(&root);
    } else if mode == "wait" {
        fs::write(root.join("ready.txt"), std::process::id().to_string()).unwrap();
        loop {
            std::thread::sleep(Duration::from_millis(50));
        }
    }
}

/// Native directory queries issued without an APC routine or context. A merged
/// directory cannot post a completion packet, so a handle associated with an
/// I/O completion port must fail explicitly instead of returning a result that
/// a port waiter never observes.
///
/// The preload publishes `PNPORT_UNSUPPORTED_OPERATION` for the first rejected
/// query and the supervisor then stops the run with exit 125, so this child
/// ignores the supervisor's CTRL_BREAK to finish its assertions inside the
/// shutdown grace interval and writes `completion-done.txt` when all of them
/// passed.
fn completion_port_directory_queries(root: &Path) {
    use std::ffi::c_void;

    use windows_sys::Win32::{
        Foundation::{
            CloseHandle, DuplicateHandle, GetLastError, DUPLICATE_SAME_ACCESS, HANDLE,
            INVALID_HANDLE_VALUE, STATUS_PENDING, WAIT_OBJECT_0, WAIT_TIMEOUT,
        },
        Storage::FileSystem::{
            CreateFileW, FILE_FLAG_BACKUP_SEMANTICS, FILE_FLAG_OVERLAPPED, FILE_LIST_DIRECTORY,
            FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE, OPEN_EXISTING,
        },
        System::{
            Console::SetConsoleCtrlHandler,
            Threading::{CreateEventW, GetCurrentProcess, WaitForSingleObject},
            IO::{CreateIoCompletionPort, GetQueuedCompletionStatus, IO_STATUS_BLOCK, OVERLAPPED},
        },
    };

    #[link(name = "ntdll")]
    extern "system" {
        fn NtQueryDirectoryFile(
            file: HANDLE,
            event: HANDLE,
            apc: *mut c_void,
            context: *mut c_void,
            ios: *mut IO_STATUS_BLOCK,
            buffer: *mut c_void,
            length: u32,
            class: i32,
            single: u8,
            pattern: *mut c_void,
            restart: u8,
        ) -> i32;
        fn NtQueryDirectoryFileEx(
            file: HANDLE,
            event: HANDLE,
            apc: *mut c_void,
            context: *mut c_void,
            ios: *mut IO_STATUS_BLOCK,
            buffer: *mut c_void,
            length: u32,
            class: i32,
            flags: u32,
            pattern: *mut c_void,
        ) -> i32;
        fn NtSetInformationFile(
            file: HANDLE,
            ios: *mut IO_STATUS_BLOCK,
            information: *mut c_void,
            length: u32,
            class: i32,
        ) -> i32;
    }
    const FILE_NAMES_INFORMATION: i32 = 12;
    const SL_RESTART_SCAN: u32 = 1;
    const FILE_REPLACE_COMPLETION_INFORMATION: i32 = 61;

    unsafe extern "system" fn ignore_control_event(_event: u32) -> i32 {
        1
    }
    assert_ne!(
        unsafe { SetConsoleCtrlHandler(Some(ignore_control_event), 1) },
        0,
        "install control handler"
    );

    #[derive(Clone, Copy, Debug)]
    enum Query {
        Classic,
        Extended,
    }
    /// Returns the NTSTATUS of a restarting null-callback query. The request
    /// storage must outlive the kernel's use of it, so a pending request is
    /// awaited before the buffers drop. `port` is the completion port the
    /// handle is associated with, if any; the packet of a pending native
    /// request is consumed so it cannot be mistaken for a rejected query's.
    fn query(directory: HANDLE, api: Query, port: Option<HANDLE>) -> i32 {
        // Heap storage so a request that never completes can be leaked instead
        // of freed under the kernel.
        let mut ios: Box<IO_STATUS_BLOCK> = Box::new(unsafe { std::mem::zeroed() });
        let mut buffer = vec![0u64; 2048];
        let length = (buffer.len() * 8) as u32;
        let event = unsafe { CreateEventW(std::ptr::null(), 1, 0, std::ptr::null()) };
        assert!(!event.is_null(), "create query event");
        let mut status = unsafe {
            match api {
                Query::Classic => NtQueryDirectoryFile(
                    directory,
                    event,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    &mut *ios,
                    buffer.as_mut_ptr().cast(),
                    length,
                    FILE_NAMES_INFORMATION,
                    0,
                    std::ptr::null_mut(),
                    1,
                ),
                Query::Extended => NtQueryDirectoryFileEx(
                    directory,
                    event,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    &mut *ios,
                    buffer.as_mut_ptr().cast(),
                    length,
                    FILE_NAMES_INFORMATION,
                    SL_RESTART_SCAN,
                    std::ptr::null_mut(),
                ),
            }
        };
        if status == STATUS_PENDING {
            if unsafe { WaitForSingleObject(event, 10_000) } != WAIT_OBJECT_0 {
                std::mem::forget(ios);
                std::mem::forget(buffer);
                panic!("{api:?} pending directory query did not complete");
            }
            status = unsafe { ios.Anonymous.Status };
            if let Some(port) = port {
                let (mut bytes, mut key) = (0, 0);
                let mut overlapped: *mut OVERLAPPED = std::ptr::null_mut();
                while unsafe {
                    GetQueuedCompletionStatus(port, &mut bytes, &mut key, &mut overlapped, 50)
                } != 0
                {}
            }
        }
        unsafe { CloseHandle(event) };
        status
    }
    /// Replaces (or, with a null port, removes) the completion port of a
    /// handle through `FileReplaceCompletionInformation` (Windows 8.1+).
    fn replace_completion_port(directory: HANDLE, port: HANDLE) -> i32 {
        #[repr(C)]
        struct CompletionInformation {
            port: HANDLE,
            key: *mut c_void,
        }
        let mut information = CompletionInformation {
            port,
            key: std::ptr::null_mut(),
        };
        let mut ios: IO_STATUS_BLOCK = unsafe { std::mem::zeroed() };
        unsafe {
            NtSetInformationFile(
                directory,
                &mut ios,
                (&raw mut information).cast(),
                std::mem::size_of::<CompletionInformation>() as u32,
                FILE_REPLACE_COMPLETION_INFORMATION,
            )
        }
    }
    fn open(path: &Path, overlapped: bool) -> HANDLE {
        let path = pnport_core::windows::wide(path).unwrap();
        let handle = unsafe {
            CreateFileW(
                path.as_ptr(),
                FILE_LIST_DIRECTORY,
                FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                std::ptr::null(),
                OPEN_EXISTING,
                FILE_FLAG_BACKUP_SEMANTICS | if overlapped { FILE_FLAG_OVERLAPPED } else { 0 },
                std::ptr::null_mut(),
            )
        };
        assert_ne!(handle, INVALID_HANDLE_VALUE, "open {path:?}");
        handle
    }
    /// Associates a new completion port with the handle. Returns the port, or
    /// zero when the native association failed.
    fn associate(directory: HANDLE) -> HANDLE {
        unsafe { CreateIoCompletionPort(directory, std::ptr::null_mut(), 1, 0) }
    }
    /// A rejected query must not post a completion packet either.
    fn assert_no_packet(port: HANDLE) {
        let (mut bytes, mut key) = (0, 0);
        let mut overlapped: *mut OVERLAPPED = std::ptr::null_mut();
        let received =
            unsafe { GetQueuedCompletionStatus(port, &mut bytes, &mut key, &mut overlapped, 100) };
        assert_eq!(received, 0, "unexpected completion packet");
        assert_eq!(unsafe { GetLastError() }, WAIT_TIMEOUT);
    }
    let apis = [Query::Classic, Query::Extended];

    // Controls: an unassociated synchronous handle keeps the merged listing.
    let synchronous = open(root, false);
    for api in apis {
        let status = query(synchronous, api, None);
        assert!(status >= 0, "{api:?} unassociated status {status:#x}");
    }
    // A failed association (the handle is not overlapped) records nothing.
    assert_eq!(associate(synchronous), std::ptr::null_mut());
    for api in apis {
        let status = query(synchronous, api, None);
        assert!(status >= 0, "{api:?} failed association status {status:#x}");
    }
    unsafe { CloseHandle(synchronous) };

    // Repeat so a reopened handle that reuses the numeric value starts clean.
    for round in 0..2 {
        let directory = open(root, true);
        let port = associate(directory);
        assert!(!port.is_null(), "round {round} association failed");
        for api in apis {
            let status = query(directory, api, Some(port));
            assert!(status < 0, "{api:?} round {round} associated {status:#x}");
        }
        assert_no_packet(port);
        // A same-process duplicate shares the native file object.
        let mut duplicate: HANDLE = std::ptr::null_mut();
        let duplicated = unsafe {
            DuplicateHandle(
                GetCurrentProcess(),
                directory,
                GetCurrentProcess(),
                &mut duplicate,
                0,
                0,
                DUPLICATE_SAME_ACCESS,
            )
        };
        assert_ne!(duplicated, 0, "duplicate directory handle");
        for api in apis {
            let status = query(duplicate, api, Some(port));
            assert!(status < 0, "{api:?} round {round} duplicate {status:#x}");
        }
        assert_no_packet(port);
        unsafe {
            CloseHandle(duplicate);
            CloseHandle(directory);
            CloseHandle(port);
        }
        let reopened = open(root, true);
        for api in apis {
            let status = query(reopened, api, None);
            assert!(status >= 0, "{api:?} round {round} reopened {status:#x}");
        }
        unsafe { CloseHandle(reopened) };
    }

    // `FileReplaceCompletionInformation` changes or removes the association
    // without `FileCompletionInformation`, and the recorded state must follow.
    let directory = open(root, true);
    let port = unsafe { CreateIoCompletionPort(INVALID_HANDLE_VALUE, std::ptr::null_mut(), 1, 0) };
    assert!(!port.is_null(), "create replacement port");
    // `FileReplaceCompletionInformation` is available on every supported
    // Windows baseline, so a failed install is a hook, ABI, or access
    // regression and must fail the test instead of skipping the assertions.
    let installed = replace_completion_port(directory, port);
    assert!(installed >= 0, "replacement install status {installed:#x}");
    for api in apis {
        let status = query(directory, api, Some(port));
        assert!(status < 0, "{api:?} replaced association {status:#x}");
    }
    assert_no_packet(port);
    let removed = replace_completion_port(directory, std::ptr::null_mut());
    assert!(removed >= 0, "remove association status {removed:#x}");
    for api in apis {
        let status = query(directory, api, None);
        assert!(status >= 0, "{api:?} removed association {status:#x}");
    }
    // An unreadable replacement payload must surface the native failure
    // instead of faulting the detour, and must not change the recorded state.
    let unreadable = unsafe {
        let mut ios: IO_STATUS_BLOCK = std::mem::zeroed();
        NtSetInformationFile(
            directory,
            &mut ios,
            // Page zero is never mapped, so the native probe rejects the
            // pointer while the detour must not dereference it.
            0x10 as *mut c_void,
            (2 * std::mem::size_of::<usize>()) as u32,
            FILE_REPLACE_COMPLETION_INFORMATION,
        )
    };
    assert!(unreadable < 0, "unreadable payload status {unreadable:#x}");
    for api in apis {
        let status = query(directory, api, None);
        assert!(status >= 0, "{api:?} after unreadable payload {status:#x}");
    }
    unsafe {
        CloseHandle(directory);
        CloseHandle(port);
    }

    // A read-only managed directory (a dependency container with merged
    // entries) still accepts completion association changes: the request only
    // changes the completion association and never mutates package content.
    fs::create_dir_all(root.join("node_modules/.cache")).unwrap();
    let container = open(&root.join("node_modules"), true);
    let container_port =
        unsafe { CreateIoCompletionPort(INVALID_HANDLE_VALUE, std::ptr::null_mut(), 1, 0) };
    assert!(!container_port.is_null(), "create container port");
    for api in apis {
        let status = query(container, api, None);
        assert!(status >= 0, "{api:?} read-only unassociated {status:#x}");
    }
    let installed = replace_completion_port(container, container_port);
    assert!(
        installed >= 0,
        "read-only replacement install status {installed:#x}"
    );
    for api in apis {
        let status = query(container, api, Some(container_port));
        assert!(status < 0, "{api:?} read-only replaced {status:#x}");
    }
    assert_no_packet(container_port);
    let removed = replace_completion_port(container, std::ptr::null_mut());
    assert!(
        removed >= 0,
        "read-only replacement removal status {removed:#x}"
    );
    for api in apis {
        let status = query(container, api, None);
        assert!(status >= 0, "{api:?} read-only removed {status:#x}");
    }
    unsafe {
        CloseHandle(container);
        CloseHandle(container_port);
    }

    // A native-backed directory (no merged entries) keeps the native syscall.
    let plain = root.join("plain");
    fs::create_dir_all(&plain).unwrap();
    fs::write(plain.join("file.txt"), b"plain").unwrap();
    let native = open(&plain, true);
    let port = associate(native);
    assert!(!port.is_null(), "native association failed");
    for api in apis {
        let status = query(native, api, Some(port));
        assert!(status >= 0, "{api:?} native status {status:#x}");
    }
    unsafe {
        CloseHandle(native);
        CloseHandle(port);
    }
    fs::write(root.join("completion-done.txt"), b"done").unwrap();
}

#[test]
fn zip_library_mapping_cache_and_direct_dependency_binary() {
    for split in [false, true] {
        let fixture = fixture(split);
        let root = fixture.path().join("project 한글 space");
        let cache = fixture.path().join("cache");
        for mode in ["loader", "mmap", "watch", "cache"] {
            let output = run(&root, &cache, mode).output().unwrap();
            assert!(
                output.status.success(),
                "{mode} failed {:?}: {} {}",
                output.status.code(),
                String::from_utf8_lossy(&output.stderr),
                String::from_utf8_lossy(&output.stdout)
            );
        }
        let output = Command::new(binary())
            .arg("--project")
            .arg(&root)
            .arg("--cache-dir")
            .arg(&cache)
            .args([
                "run",
                "--",
                "native-fixture",
                "--exact",
                "native_child",
                "--nocapture",
            ])
            .env(MODE, "read")
            .env(PROJECT, &root)
            .current_dir(&root)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "ZIP executable failed: {} {}",
            String::from_utf8_lossy(&output.stderr),
            String::from_utf8_lossy(&output.stdout)
        );
    }
}

#[test]
fn installed_inline_split_filesystem_and_replacement_environment() {
    for split in [false, true] {
        let fixture = fixture(split);
        let root = fixture.path().join("project 한글 space");
        let cache = fixture.path().join("private-cache");
        for mode in ["read", "spawn"] {
            let output = run(&root, &cache, mode).output().unwrap();
            assert!(
                output.status.success(),
                "mode {mode}, status {:?}: {} {}",
                output.status.code(),
                String::from_utf8_lossy(&output.stderr),
                String::from_utf8_lossy(&output.stdout)
            );
        }
        assert!(!root.join("node_modules").exists());
        assert_eq!(fs::read(root.join("source.txt")).unwrap(), b"source");
        assert_eq!(fs::read(root.join("output.txt")).unwrap(), b"output");
    }
}

#[test]
fn relative_applications_resolve_against_the_caller_directory() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let work = fixture.path().join("application-work");
    assert!(
        work.to_str().is_some_and(str::is_ascii),
        "ANSI CreateProcess fixtures need an ASCII temporary directory: {work:?}"
    );
    let executable = std::env::current_exe().unwrap();
    for (directory, name) in [
        ("caller", "worker.exe"),
        ("child", "worker.exe"),
        ("child", "only-child.exe"),
    ] {
        let directory = work.join(directory).join("bin");
        fs::create_dir_all(&directory).unwrap();
        fs::copy(&executable, directory.join(name)).unwrap();
    }
    let output = run(&root, &fixture.path().join("cache"), "application")
        .env(WORK, &work)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "application resolution failed {:?}: {} {}",
        output.status.code(),
        String::from_utf8_lossy(&output.stderr),
        String::from_utf8_lossy(&output.stdout)
    );
}

#[test]
fn completion_port_handles_reject_merged_directory_queries() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let cache = fixture.path().join("private-cache");
    let output = run(&root, &cache, "completion").output().unwrap();
    let stderr = String::from_utf8_lossy(&output.stderr);
    // The rejected query publishes the unsupported-operation failure, so the
    // supervisor ends the run with the typed diagnostic. The child writes its
    // marker only after every in-process assertion passed.
    assert_eq!(
        output.status.code(),
        Some(125),
        "status {:?}: {stderr} {}",
        output.status.code(),
        String::from_utf8_lossy(&output.stdout)
    );
    assert!(
        stderr.contains("PNPORT_UNSUPPORTED_OPERATION"),
        "missing typed diagnostic: {stderr}"
    );
    assert!(
        root.join("completion-done.txt").is_file(),
        "the child assertions did not complete: {stderr}"
    );
}

#[test]
fn native_cache_materialization_keeps_active_content() {
    let fixture = fixture(false);
    let cache = pnport::cache::Cache::open(fixture.path().join("cache")).unwrap();
    let lease = cache
        .materialize(&fixture.path().join("project 한글 space/dependency.zip"))
        .unwrap();
    assert_eq!(
        fs::read(lease.content.join("node_modules/example/data.txt")).unwrap(),
        b"dependency"
    );
    assert!(cache
        .entries(pnport::cache::Operation::Clean)
        .unwrap()
        .iter()
        .any(|entry| matches!(entry.state, pnport::cache::State::Active)));
    drop(lease);
    assert!(cache
        .entries(pnport::cache::Operation::Clean)
        .unwrap()
        .iter()
        .any(|entry| matches!(entry.state, pnport::cache::State::Removed)));
}

#[test]
fn native_lookup_preserves_the_drive_root() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let graph = pnport::graph::Graph::load(&root.join(".pnp.cjs")).unwrap();
    let path = root.join("node_modules/example/data.txt");
    let resolved = pnport_core::windows::lookup_spelling(&path, &graph).unwrap();
    assert!(resolved.is_absolute(), "{resolved:?}");
}

#[test]
fn cross_volume_packages_and_unmanaged_system_paths_do_not_panic() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let mut snapshot = pnport::graph::Graph::load(&root.join(".pnp.cjs"))
        .unwrap()
        .snapshot;
    snapshot.manifest_path = PathBuf::from(r"Z:\project\.pnp.cjs");
    snapshot.inputs.clear();
    snapshot.data["packageRegistryData"][2][1][0][1]["packageLocation"] =
        json!("C:/cache/example/");
    snapshot.data["packageRegistryData"][2][1][0][1]["packageDependencies"] =
        json!([["nested", "npm:1.0.0"], ["alias", ["nested", "npm:1.0.0"]]]);
    snapshot.data["packageRegistryData"].as_array_mut().unwrap().push(json!(["nested",[["npm:1.0.0",{"packageLocation":"C:/cache/nested/","packageDependencies":[],"linkType":"HARD"}]]]));
    let graph = pnport::graph::Graph::from_snapshot(snapshot.clone()).unwrap();
    let issuer = Path::new(r"C:\cache\example\source.rs");
    assert!(graph.package(Path::new(r"C:\Windows\System32")).is_none());
    assert!(!graph.managed(Path::new(r"C:\Windows\System32")));
    assert!(graph.managed(issuer));
    for name in ["nested", "alias"] {
        assert_eq!(
            pnport::graph::normalize(&graph.resolve(name, issuer).unwrap()),
            PathBuf::from(r"C:\cache\nested")
        );
    }
    snapshot.data["ignorePatternData"] = json!("^C:/cache/example/");
    let ignored = pnport::graph::Graph::from_snapshot(snapshot).unwrap();
    assert!(ignored.package(issuer).is_none());
    assert!(!ignored.managed(issuer));
}

#[test]
fn node_filesystem_crypto_loopback_and_replacement_environment() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let node = std::env::var_os("PNPORT_TEST_NODE").unwrap_or_else(|| "node".into());
    let script = r#"
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = process.argv[1];
const dependency = path.join(root, 'node_modules/example/data.txt');
assert.equal(fs.readFileSync(dependency, 'utf8'), 'dependency');
assert(fs.readdirSync(root).includes('node_modules'));
assert.equal(require('node:crypto').randomBytes(32).length, 32);
const child = require('node:child_process').execFileSync(process.execPath,
  ['-e', 'process.stdout.write(require("node:fs").readFileSync(process.argv[1]))', dependency],
  { env: { SystemRoot: process.env.SystemRoot, PATH: process.env.PATH } });
assert.equal(child.toString(), 'dependency');
const net = require('node:net');
const server = net.createServer(socket => socket.end('loopback'));
server.listen(0, '127.0.0.1', () => {
  const client = net.connect(server.address().port, '127.0.0.1');
  let received = '';
  client.on('data', chunk => received += chunk);
  client.on('end', () => { assert.equal(received, 'loopback'); server.close(); });
});
"#;
    let output = Command::new(binary())
        .arg("--project")
        .arg(&root)
        .arg("--cache-dir")
        .arg(fixture.path().join("cache"))
        .args(["--log-level=debug", "--color=never", "run", "--"])
        .arg(node)
        .arg("-e")
        .arg(script)
        .arg(&root)
        .current_dir(&root)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "Node native fixture failed {:?}: {} {}",
        output.status.code(),
        String::from_utf8_lossy(&output.stderr),
        String::from_utf8_lossy(&output.stdout)
    );
}

#[test]
fn forced_supervisor_exit_kills_the_owned_job() {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::CloseHandle,
        System::Threading::{OpenProcess, WaitForSingleObject, PROCESS_SYNCHRONIZE},
    };
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let mut supervisor = run(&root, &fixture.path().join("cache"), "wait")
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(20);
    while !root.join("ready.txt").exists() {
        if let Some(status) = supervisor.try_wait().unwrap() {
            let mut stderr = String::new();
            supervisor
                .stderr
                .take()
                .unwrap()
                .read_to_string(&mut stderr)
                .unwrap();
            panic!("early exit {status}: {stderr}");
        }
        assert!(
            Instant::now() < deadline,
            "native child initialization deadline"
        );
        std::thread::sleep(Duration::from_millis(20));
    }
    let pid = fs::read_to_string(root.join("ready.txt"))
        .unwrap()
        .parse()
        .unwrap();
    let child = unsafe { OpenProcess(PROCESS_SYNCHRONIZE, 0, pid) };
    assert!(!child.is_null());
    supervisor.kill().unwrap();
    supervisor.wait().unwrap();
    let status = unsafe { WaitForSingleObject(child, 5000) };
    unsafe {
        CloseHandle(child);
    }
    assert_eq!(
        status, 0,
        "owned child survived abrupt supervisor termination"
    );
    let _ = supervisor.as_raw_handle();
}

#[test]
fn changed_graph_stops_the_job_and_preserves_an_unrelated_process() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let unrelated_root = tempfile::tempdir().unwrap();
    let mut unrelated = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "native_child", "--nocapture"])
        .env(MODE, "wait")
        .env(PROJECT, unrelated_root.path())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let outcome = std::panic::catch_unwind(|| {
        let mut supervisor = run(&root, &fixture.path().join("cache"), "wait")
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let result = std::panic::catch_unwind(std::panic::AssertUnwindSafe(|| {
            let deadline = Instant::now() + Duration::from_secs(20);
            while !root.join("ready.txt").exists() {
                assert!(
                    supervisor.try_wait().unwrap().is_none(),
                    "supervisor exited before readiness"
                );
                assert!(Instant::now() < deadline, "initialization deadline");
                std::thread::sleep(Duration::from_millis(20));
            }
            let path = root.join(".pnp.cjs");
            let modified = fs::metadata(&path).unwrap().modified().unwrap();
            let bytes = fs::read_to_string(&path)
                .unwrap()
                .replace("example", "EXAMPLE");
            fs::write(&path, bytes).unwrap();
            fs::OpenOptions::new()
                .write(true)
                .open(&path)
                .unwrap()
                .set_modified(modified)
                .unwrap();
            let deadline = Instant::now() + Duration::from_secs(12);
            loop {
                if let Some(status) = supervisor.try_wait().unwrap() {
                    assert_eq!(status.code(), Some(125));
                    let mut stderr = String::new();
                    supervisor
                        .stderr
                        .take()
                        .unwrap()
                        .read_to_string(&mut stderr)
                        .unwrap();
                    assert!(
                        stderr.contains("PNPORT_GRAPH_CHANGED"),
                        "missing typed restart diagnostic: {stderr}"
                    );
                    break;
                }
                assert!(
                    Instant::now() < deadline,
                    "owned graph-change cleanup deadline"
                );
                std::thread::sleep(Duration::from_millis(20));
            }
        }));
        let _ = supervisor.kill();
        let _ = supervisor.wait();
        if let Err(error) = result {
            std::panic::resume_unwind(error);
        }
    });
    let unrelated_alive = unrelated.try_wait().unwrap().is_none();
    let _ = unrelated.kill();
    let _ = unrelated.wait();
    assert!(
        unrelated_alive,
        "graph cleanup signalled an unrelated process"
    );
    if let Err(error) = outcome {
        std::panic::resume_unwind(error);
    }
}

#[test]
fn doctor_json_and_rejected_companions_preserve_the_launch_boundary() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let cache = fixture.path().join("cache");
    let report = Command::new(binary())
        .arg("--project")
        .arg(&root)
        .arg("--cache-dir")
        .arg(&cache)
        .args(["doctor", "--json"])
        .output()
        .unwrap();
    assert!(report.status.success());
    let report: serde_json::Value = serde_json::from_slice(&report.stdout).unwrap();
    assert_eq!(report["schemaVersion"], 1);
    assert_eq!(report["ready"], true);
    let pair = fixture.path().join("pair");
    fs::create_dir(&pair).unwrap();
    fs::copy(binary(), pair.join("pnport.exe")).unwrap();
    let original = fs::read(binary().parent().unwrap().join("pnport_preload.dll")).unwrap();
    for wrong_architecture in [false, true] {
        let mut bytes = original.clone();
        if wrong_architecture {
            let pe = u32::from_le_bytes(bytes[0x3c..0x40].try_into().unwrap()) as usize;
            bytes[pe + 4..pe + 6].copy_from_slice(
                &(if cfg!(target_arch = "aarch64") {
                    0x8664u16
                } else {
                    0xaa64u16
                })
                .to_le_bytes(),
            );
        } else {
            let offset = bytes
                .windows(35)
                .position(|bytes| bytes == b"PNPORT_PRELOAD_0.1.0_FORMAT_1_READY")
                .unwrap();
            bytes[offset] = b'X';
        }
        fs::write(pair.join("pnport_preload.dll"), bytes).unwrap();
        let output = Command::new(pair.join("pnport.exe"))
            .arg("--project")
            .arg(&root)
            .arg("--cache-dir")
            .arg(&cache)
            .args(["run", "--"])
            .arg(std::env::current_exe().unwrap())
            .args(["--exact", "native_child", "--nocapture"])
            .env(MODE, "wait")
            .env(PROJECT, &root)
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(125));
        assert!(String::from_utf8_lossy(&output.stderr).contains("PNPORT_INJECTION_FAILED"));
        assert!(
            !root.join("ready.txt").exists(),
            "a rejected image executed user code"
        );
    }
}

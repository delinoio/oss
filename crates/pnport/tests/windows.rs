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

/// Diagnostic replay uses an external debugger, so a corrupted target stack
/// or loader lock cannot prevent reporting its faulting code location. Normal
/// validation never sets this opt-in; replay cannot turn its first failure
/// green.
fn node_output(command: &mut Command) -> std::process::Output {
    if std::env::var_os("PNPORT_WINDOWS_DEBUGGER").is_none() {
        return command.output().unwrap();
    }
    use std::{collections::BTreeMap, os::windows::process::CommandExt};

    use windows_sys::Win32::{
        Foundation::{CloseHandle, DBG_CONTINUE, DBG_EXCEPTION_NOT_HANDLED, EXCEPTION_BREAKPOINT},
        Storage::FileSystem::GetFinalPathNameByHandleW,
        System::{Diagnostics::Debug::*, Threading::DEBUG_PROCESS},
    };
    let mut child = command
        .creation_flags(DEBUG_PROCESS)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdout = child.stdout.take().unwrap();
    let mut stderr = child.stderr.take().unwrap();
    let out = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        stdout.read_to_end(&mut bytes).unwrap();
        bytes
    });
    let err = std::thread::spawn(move || {
        let mut bytes = Vec::new();
        stderr.read_to_end(&mut bytes).unwrap();
        bytes
    });
    let mut modules = std::collections::HashMap::<u32, BTreeMap<usize, String>>::new();
    let mut threads = std::collections::HashMap::new();
    let mut active = 0usize;
    let deadline = Instant::now() + Duration::from_secs(60);
    loop {
        if Instant::now() >= deadline {
            let _ = child.kill();
            panic!("diagnostic debugger timed out");
        }
        let mut event = DEBUG_EVENT::default();
        if unsafe { WaitForDebugEvent(&mut event, 1000) } == 0 {
            continue;
        }
        let mut disposition = DBG_CONTINUE;
        unsafe {
            let image = match event.dwDebugEventCode {
                CREATE_PROCESS_DEBUG_EVENT => {
                    active += 1;
                    threads.insert(event.dwThreadId, event.u.CreateProcessInfo.hThread);
                    Some((
                        event.u.CreateProcessInfo.hFile,
                        event.u.CreateProcessInfo.lpBaseOfImage as usize,
                    ))
                }
                LOAD_DLL_DEBUG_EVENT => {
                    Some((event.u.LoadDll.hFile, event.u.LoadDll.lpBaseOfDll as usize))
                }
                _ => None,
            };
            if let Some((file, base)) = image {
                if !file.is_null() {
                    let mut name = vec![0u16; 32768];
                    let length =
                        GetFinalPathNameByHandleW(file, name.as_mut_ptr(), name.len() as u32, 0)
                            as usize;
                    if length < name.len() {
                        let path = String::from_utf16_lossy(&name[..length]);
                        let name = path.rsplit('\\').next().unwrap_or("unknown").to_owned();
                        modules
                            .entry(event.dwProcessId)
                            .or_default()
                            .insert(base, name);
                    }
                    CloseHandle(file);
                }
            }
            if event.dwDebugEventCode == CREATE_THREAD_DEBUG_EVENT {
                threads.insert(event.dwThreadId, event.u.CreateThread.hThread);
            }
            if event.dwDebugEventCode == EXIT_THREAD_DEBUG_EVENT {
                threads.remove(&event.dwThreadId);
            }
            if event.dwDebugEventCode == EXCEPTION_DEBUG_EVENT {
                let exception = event.u.Exception;
                let code = exception.ExceptionRecord.ExceptionCode;
                if code != EXCEPTION_BREAKPOINT {
                    disposition = DBG_EXCEPTION_NOT_HANDLED;
                }
                if code == 0xc0000005u32 as i32 {
                    #[allow(unused_mut, reason = "ARM64 replay also inspects the link register")]
                    let mut addresses = vec![exception.ExceptionRecord.ExceptionAddress as usize];
                    #[cfg(target_arch = "aarch64")]
                    if let Some(thread) = threads.get(&event.dwThreadId) {
                        let mut context = CONTEXT {
                            ContextFlags: CONTEXT_CONTROL_ARM64,
                            ..CONTEXT::default()
                        };
                        if GetThreadContext(*thread, &mut context) != 0 {
                            addresses.push(context.Anonymous.Anonymous.Lr as usize);
                        }
                    }
                    for address in addresses {
                        if let Some((base, name)) = modules
                            .get(&event.dwProcessId)
                            .and_then(|images| images.range(..=address).next_back())
                        {
                            let offset = address - base;
                            eprintln!(
                                "action=windows_exception_frame module={name} offset=0x{offset:x} \
                                 first_chance={}",
                                exception.dwFirstChance
                            );
                            if name.eq_ignore_ascii_case("pnport_preload.dll") {
                                let image = PathBuf::from(env!("CARGO_BIN_EXE_pnport"))
                                    .with_file_name(name);
                                if let Ok(symbols) = Command::new("llvm-symbolizer.exe")
                                    .arg("--relative-address")
                                    .arg(format!("--obj={}", image.display()))
                                    .arg(format!("0x{offset:x}"))
                                    .output()
                                {
                                    eprintln!("{}", String::from_utf8_lossy(&symbols.stdout));
                                }
                            }
                        }
                    }
                }
            }
            if event.dwDebugEventCode == EXIT_PROCESS_DEBUG_EVENT {
                modules.remove(&event.dwProcessId);
                active -= 1;
            }
            assert_ne!(
                ContinueDebugEvent(event.dwProcessId, event.dwThreadId, disposition),
                0
            );
        }
        if active == 0 {
            break;
        }
    }
    std::process::Output {
        status: child.wait().unwrap(),
        stdout: out.join().unwrap(),
        stderr: err.join().unwrap(),
    }
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
        // NT status is returned directly; hook bookkeeping must not clear the
        // caller's independent Win32 last-error slot through TlsGetValue.
        {
            use std::os::windows::io::AsRawHandle;

            use ntapi::ntioapi::{
                FileStandardInformation, NtQueryInformationFile, FILE_STANDARD_INFORMATION,
                IO_STATUS_BLOCK,
            };
            use windows_sys::Win32::Foundation::{GetLastError, SetLastError};
            let mut info: FILE_STANDARD_INFORMATION = unsafe { std::mem::zeroed() };
            let mut ios: IO_STATUS_BLOCK = unsafe { std::mem::zeroed() };
            let (status, error) = unsafe {
                SetLastError(0x521);
                let status = NtQueryInformationFile(
                    original.as_raw_handle().cast(),
                    &mut ios,
                    (&raw mut info).cast(),
                    std::mem::size_of_val(&info) as u32,
                    FileStandardInformation,
                );
                (status, GetLastError())
            };
            assert!(status >= 0, "native information query failed {status:#x}");
            assert_eq!(error, 0x521, "hook TLS changed the caller's last error");
        }
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
    } else if mode == "file-ids" {
        directory_file_ids(&root);
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
            CloseHandle, DuplicateHandle, DUPLICATE_SAME_ACCESS, HANDLE, INVALID_HANDLE_VALUE,
            STATUS_PENDING, STATUS_SUCCESS, STATUS_TIMEOUT, WAIT_OBJECT_0,
        },
        Storage::FileSystem::{
            CreateFileW, FILE_FLAG_BACKUP_SEMANTICS, FILE_FLAG_OVERLAPPED, FILE_LIST_DIRECTORY,
            FILE_SHARE_DELETE, FILE_SHARE_READ, FILE_SHARE_WRITE, OPEN_EXISTING,
        },
        System::{
            Console::SetConsoleCtrlHandler,
            Threading::{CreateEventW, GetCurrentProcess, WaitForSingleObject},
            IO::{CreateIoCompletionPort, IO_STATUS_BLOCK},
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
        /// Removes one packet. The call's own NTSTATUS reports the dequeue
        /// outcome (`STATUS_SUCCESS`, or `STATUS_TIMEOUT` when no packet
        /// arrived), while the returned `IO_STATUS_BLOCK` carries the status
        /// of the completed request, so a failed completion cannot be mistaken
        /// for a timeout. `timeout` is a negative relative interval in 100 ns
        /// units.
        fn NtRemoveIoCompletion(
            port: HANDLE,
            key: *mut *mut c_void,
            context: *mut *mut c_void,
            ios: *mut IO_STATUS_BLOCK,
            timeout: *mut i64,
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
    /// One dequeued completion packet.
    struct Completion {
        key: usize,
        context: usize,
        /// The final status of the completed request, as stored in the
        /// packet's status block.
        status: i32,
    }
    /// Removes exactly one packet from `port`. Returns `None` only when the
    /// wait timed out without a packet; a packet for a failed request is
    /// returned with its negative `status`. Any other dequeue failure is a
    /// harness error and panics instead of being read as a timeout.
    fn remove_completion(port: HANDLE, timeout_ms: u32) -> Option<Completion> {
        let mut key: *mut c_void = std::ptr::null_mut();
        let mut context: *mut c_void = std::ptr::null_mut();
        let mut ios: IO_STATUS_BLOCK = unsafe { std::mem::zeroed() };
        let mut timeout = -(i64::from(timeout_ms) * 10_000);
        let removed =
            unsafe { NtRemoveIoCompletion(port, &mut key, &mut context, &mut ios, &mut timeout) };
        if removed == STATUS_TIMEOUT {
            return None;
        }
        assert_eq!(
            removed, STATUS_SUCCESS,
            "dequeue completion packet status {removed:#x}"
        );
        Some(Completion {
            key: key as usize,
            context: context as usize,
            status: unsafe { ios.Anonymous.Status },
        })
    }
    /// Returns the NTSTATUS of a restarting null-callback query. The request
    /// storage must outlive the kernel's use of it, so a pending request is
    /// awaited before the buffers drop. `port` is the completion port the
    /// handle is associated with, if any.
    ///
    /// For an associated handle the status block of a pending request is not
    /// updated until its completion packet is removed from the port, so the
    /// final status is taken from exactly one dequeued packet and the block is
    /// never read before that. A timeout and a failed completion are reported
    /// as different failures.
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
            if let Some(port) = port {
                // Only the packet's own status block is valid before the
                // packet is removed, and it is the only source of the final
                // status.
                let Some(completion) = remove_completion(port, 10_000) else {
                    std::mem::forget(ios);
                    std::mem::forget(buffer);
                    panic!("{api:?} pending directory query posted no completion packet");
                };
                assert_eq!(completion.key, 1, "{api:?} completion key");
                assert_eq!(completion.context, 0, "{api:?} completion context");
                status = completion.status;
            } else {
                if unsafe { WaitForSingleObject(event, 10_000) } != WAIT_OBJECT_0 {
                    std::mem::forget(ios);
                    std::mem::forget(buffer);
                    panic!("{api:?} pending directory query did not complete");
                }
                // No completion port is involved, so the signaled event
                // follows the status block update.
                status = unsafe { ios.Anonymous.Status };
            }
        } else if let (Some(port), true) = (port, status >= 0) {
            // A native request that completed inline on an associated handle
            // still posts its packet unless skip-on-success is set. Consume it
            // so a later pending request on the same port cannot dequeue this
            // one's packet instead of its own. A rejected query (negative
            // status) never reaches the native call, so its port is left
            // untouched for `assert_no_packet`.
            while remove_completion(port, 0).is_some() {}
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
    /// A rejected query must not post a completion packet either. A packet,
    /// including one for a failed request, fails the assertion; only a
    /// timeout with an empty port passes.
    fn assert_no_packet(port: HANDLE) {
        if let Some(completion) = remove_completion(port, 100) {
            panic!(
                "unexpected completion packet: key {} context {} status {:#x}",
                completion.key, completion.context, completion.status
            );
        }
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

    // Run the native control before the first intentional rejection starts
    // the supervisor's shutdown grace interval. Its asynchronous completion
    // wait must report its own timeout instead of being killed mid-control.
    {
        // A native-backed directory (no merged entries) keeps the native
        // syscall.
        let plain = root.join("plain");
        fs::create_dir_all(&plain).unwrap();
        fs::write(plain.join("file.txt"), b"plain").unwrap();
        let native = open(&plain, true);
        let port = associate(native);
        assert!(!port.is_null(), "native association failed");
        // Null APC context uses the explicit event on this native query;
        // Windows does not enqueue a completion packet for it. Keep the
        // associated-port rejection checks below separate from this control.
        for api in apis {
            let status = query(native, api, None);
            assert!(status >= 0, "{api:?} native status {status:#x}");
        }
        assert_no_packet(port);
        unsafe {
            CloseHandle(native);
            CloseHandle(port);
        }
    }

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
    // Replacement changes an existing native association. Installing it on
    // an unassociated object returns STATUS_INVALID_PARAMETER on Windows 2022.
    // Keep both replacement and removal assertions after a real association.
    let original_port = associate(directory);
    assert!(!original_port.is_null(), "create original port");
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
        CloseHandle(original_port);
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
    let original_container_port = associate(container);
    assert!(
        !original_container_port.is_null(),
        "associate read-only container"
    );
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
        CloseHandle(original_container_port);
    }

    fs::write(root.join("completion-done.txt"), b"done").unwrap();
}

/// Reads ID-bearing merged directory records through both native query APIs
/// and compares every `FileId` with the identity of a native open of the same
/// entry. `root` lists a physical `.pnp.cjs`, a hard-link pair, a
/// `restricted.txt` whose attribute access the caller may be denied, and the
/// virtual `node_modules` entry, plus `.` and `..`.
fn directory_file_ids(root: &Path) {
    use std::{collections::BTreeMap, ffi::c_void, os::windows::ffi::OsStringExt};

    use windows_sys::Win32::{
        Foundation::{
            CloseHandle, DuplicateHandle, DUPLICATE_SAME_ACCESS, HANDLE, INVALID_HANDLE_VALUE,
        },
        Storage::FileSystem::{
            CreateFileW, GetFileInformationByHandle, BY_HANDLE_FILE_INFORMATION,
            FILE_FLAG_BACKUP_SEMANTICS, FILE_LIST_DIRECTORY, FILE_SHARE_DELETE, FILE_SHARE_READ,
            FILE_SHARE_WRITE, OPEN_EXISTING,
        },
        System::{Threading::GetCurrentProcess, IO::IO_STATUS_BLOCK},
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
    }
    const FILE_DIRECTORY_INFORMATION: i32 = 1;
    const FILE_BOTH_DIRECTORY_INFORMATION: i32 = 3;
    const FILE_NAMES_INFORMATION: i32 = 12;
    const FILE_ID_BOTH_DIRECTORY_INFORMATION: i32 = 37;
    const FILE_ID_FULL_DIRECTORY_INFORMATION: i32 = 38;
    const STATUS_NO_MORE_FILES: i32 = 0x8000_0006u32 as i32;
    const SL_RESTART_SCAN: u32 = 1;
    const SL_RETURN_SINGLE_ENTRY: u32 = 2;

    #[derive(Clone, Copy, Debug)]
    enum Query {
        Classic,
        Extended,
    }
    /// Fixed header size, the `FileId` offset (if the class has one) and the
    /// offset of the file name for each class under test.
    fn layout(class: i32) -> (usize, Option<usize>) {
        match class {
            FILE_DIRECTORY_INFORMATION => (64, None),
            FILE_BOTH_DIRECTORY_INFORMATION => (94, None),
            FILE_NAMES_INFORMATION => (12, None),
            FILE_ID_BOTH_DIRECTORY_INFORMATION => (104, Some(96)),
            FILE_ID_FULL_DIRECTORY_INFORMATION => (80, Some(72)),
            _ => unreachable!("unexpected class {class}"),
        }
    }
    fn open(path: &Path, access: u32) -> HANDLE {
        let wide = pnport_core::windows::wide(path).unwrap();
        let handle = unsafe {
            CreateFileW(
                wide.as_ptr(),
                access,
                FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                std::ptr::null(),
                OPEN_EXISTING,
                FILE_FLAG_BACKUP_SEMANTICS,
                std::ptr::null_mut(),
            )
        };
        assert_ne!(handle, INVALID_HANDLE_VALUE, "open {path:?}");
        handle
    }
    /// The file reference number reported by a native open of `path`, or
    /// `None` when the caller cannot open the entry for its attributes.
    ///
    /// The reference handle mirrors the production open exactly: zero desired
    /// access (the implicit attribute read), backup semantics and the full
    /// share mode. Requesting any explicit access such as
    /// `FILE_READ_ATTRIBUTES` could fail where the production open succeeds
    /// (or the reverse) once an ACL denies that right, which would make the
    /// test expect a zero id that production never returns.
    fn native_id(path: &Path) -> Option<u64> {
        let wide = pnport_core::windows::wide(path).unwrap();
        let handle = unsafe {
            CreateFileW(
                wide.as_ptr(),
                0,
                FILE_SHARE_READ | FILE_SHARE_WRITE | FILE_SHARE_DELETE,
                std::ptr::null(),
                OPEN_EXISTING,
                FILE_FLAG_BACKUP_SEMANTICS,
                std::ptr::null_mut(),
            )
        };
        if handle == INVALID_HANDLE_VALUE {
            return None;
        }
        let mut information: BY_HANDLE_FILE_INFORMATION = unsafe { std::mem::zeroed() };
        let read = unsafe { GetFileInformationByHandle(handle, &mut information) };
        unsafe { CloseHandle(handle) };
        (read != 0).then(|| {
            (u64::from(information.nFileIndexHigh) << 32) | u64::from(information.nFileIndexLow)
        })
    }
    /// One query. Returns the status and the bytes written.
    fn query(
        directory: HANDLE,
        api: Query,
        class: i32,
        capacity: usize,
        single: bool,
        restart: bool,
    ) -> (i32, Vec<u8>) {
        let mut ios: IO_STATUS_BLOCK = unsafe { std::mem::zeroed() };
        // 8-byte aligned storage, as native callers provide.
        let mut storage = vec![0u64; capacity.div_ceil(8)];
        let status = unsafe {
            match api {
                Query::Classic => NtQueryDirectoryFile(
                    directory,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    &mut ios,
                    storage.as_mut_ptr().cast(),
                    capacity as u32,
                    class,
                    u8::from(single),
                    std::ptr::null_mut(),
                    u8::from(restart),
                ),
                Query::Extended => NtQueryDirectoryFileEx(
                    directory,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    &mut ios,
                    storage.as_mut_ptr().cast(),
                    capacity as u32,
                    class,
                    if restart { SL_RESTART_SCAN } else { 0 }
                        | if single { SL_RETURN_SINGLE_ENTRY } else { 0 },
                    std::ptr::null_mut(),
                ),
            }
        };
        let used = if status >= 0 { ios.Information } else { 0 };
        let bytes: Vec<u8> = storage
            .iter()
            .flat_map(|word| word.to_le_bytes())
            .take(used)
            .collect();
        (status, bytes)
    }
    /// Parses a chain of records into `(name, file id)` pairs, checking the
    /// chaining, alignment and name offset of every record.
    fn parse(bytes: &[u8], class: i32) -> Vec<(String, Option<u64>)> {
        let (header, id_offset) = layout(class);
        let u32_at =
            |offset: usize| u32::from_le_bytes(bytes[offset..offset + 4].try_into().unwrap());
        let mut entries = Vec::new();
        let mut offset = 0;
        loop {
            let name_length_at = if class == FILE_NAMES_INFORMATION {
                8
            } else {
                60
            };
            let name_length = u32_at(offset + name_length_at) as usize;
            let name_start = offset + header;
            let name_bytes = &bytes[name_start..name_start + name_length];
            let units: Vec<u16> = (0..name_bytes.len() / 2)
                .map(|i| u16::from_le_bytes([name_bytes[2 * i], name_bytes[2 * i + 1]]))
                .collect();
            let id = id_offset.map(|at| {
                u64::from_le_bytes(bytes[offset + at..offset + at + 8].try_into().unwrap())
            });
            entries.push((
                std::ffi::OsString::from_wide(&units).into_string().unwrap(),
                id,
            ));
            let next = u32_at(offset) as usize;
            if next == 0 {
                break;
            }
            assert_eq!(next % 8, 0, "record chain stays 8-byte aligned");
            offset += next;
        }
        entries
    }
    /// Lists the whole directory with a record capacity of `capacity` bytes
    /// per call.
    fn collect(
        directory: HANDLE,
        api: Query,
        class: i32,
        capacity: usize,
        single: bool,
    ) -> Vec<(String, Option<u64>)> {
        let mut all = Vec::new();
        let mut restart = true;
        loop {
            let (status, bytes) = query(directory, api, class, capacity, single, restart);
            restart = false;
            if status == STATUS_NO_MORE_FILES {
                return all;
            }
            assert!(status >= 0, "{api:?} class {class} status {status:#x}");
            assert!(!bytes.is_empty(), "{api:?} class {class} returned no bytes");
            all.extend(parse(&bytes, class));
        }
    }

    // A hard link is a second name for one file reference number.
    fs::hard_link(root.join("source.txt"), root.join("linked.txt")).unwrap();
    let parent = root.parent().unwrap();
    let expected_path = |name: &str| match name {
        "." => root.to_path_buf(),
        ".." => parent.to_path_buf(),
        other => root.join(other),
    };
    let directory = open(root, FILE_LIST_DIRECTORY);
    for api in [Query::Classic, Query::Extended] {
        for class in [
            FILE_ID_BOTH_DIRECTORY_INFORMATION,
            FILE_ID_FULL_DIRECTORY_INFORMATION,
        ] {
            let listed = collect(directory, api, class, 64 * 1024, false);
            let ids: BTreeMap<String, u64> = listed
                .iter()
                .map(|(name, id)| (name.clone(), id.unwrap()))
                .collect();
            assert_eq!(ids.len(), listed.len(), "{api:?} class {class} names");
            for name in [
                ".",
                "..",
                "source.txt",
                "linked.txt",
                "restricted.txt",
                "dependency.zip",
                ".pnp.cjs",
                "node_modules",
            ] {
                assert!(ids.contains_key(name), "{api:?} class {class} lacks {name}");
            }
            // Every record carries the identity of a native open of its entry,
            // including `.`, `..` and the virtual `node_modules` addition. The
            // only entry the caller may be unable to open is the restricted
            // one, which keeps its record with a zero id instead of failing
            // the listing.
            for (name, id) in &ids {
                match native_id(&expected_path(name)) {
                    Some(expected) => {
                        assert_ne!(*id, 0, "{api:?} class {class} {name} has a zero id");
                        assert_eq!(*id, expected, "{api:?} class {class} {name}");
                    }
                    None => {
                        assert_eq!(name, "restricted.txt", "{api:?} class {class} {name}");
                        assert_eq!(*id, 0, "{api:?} class {class} unopenable {name}");
                    }
                }
            }
            assert_eq!(
                ids["source.txt"], ids["linked.txt"],
                "hard links share an id"
            );
            let mut unique: Vec<_> = ids
                .iter()
                .filter(|(name, _)| *name != "linked.txt")
                .map(|(_, id)| *id)
                .collect();
            unique.sort_unstable();
            let distinct = unique.len();
            unique.dedup();
            assert_eq!(
                unique.len(),
                distinct,
                "{api:?} class {class} distinct files"
            );

            // One record per call, and a buffer that holds a single record at
            // a time, chain to the same names and identities.
            let single = collect(directory, api, class, 64 * 1024, true);
            assert_eq!(single, listed, "{api:?} class {class} single entry");
            let (header, _) = layout(class);
            let longest = ids
                .keys()
                .map(|name| name.encode_utf16().count() * 2)
                .max()
                .unwrap();
            let narrow = collect(directory, api, class, header + longest + 8, false);
            assert_eq!(narrow, listed, "{api:?} class {class} narrow buffer");

            // A duplicated handle shares the enumeration cursor.
            let mut duplicate: HANDLE = std::ptr::null_mut();
            assert_ne!(
                unsafe {
                    DuplicateHandle(
                        GetCurrentProcess(),
                        directory,
                        GetCurrentProcess(),
                        &mut duplicate,
                        0,
                        0,
                        DUPLICATE_SAME_ACCESS,
                    )
                },
                0,
                "duplicate directory handle"
            );
            let (status, first) = query(directory, api, class, 64 * 1024, true, true);
            assert!(
                status >= 0,
                "{api:?} class {class} first status {status:#x}"
            );
            let mut resumed = parse(&first, class);
            loop {
                let (status, bytes) = query(duplicate, api, class, 64 * 1024, false, false);
                if status == STATUS_NO_MORE_FILES {
                    break;
                }
                assert!(
                    status >= 0,
                    "{api:?} class {class} resume status {status:#x}"
                );
                resumed.extend(parse(&bytes, class));
            }
            assert_eq!(resumed, listed, "{api:?} class {class} duplicated handle");
            unsafe { CloseHandle(duplicate) };
        }
        // Classes without a file id keep their layout and names.
        let names: Vec<_> = collect(directory, api, FILE_NAMES_INFORMATION, 64 * 1024, false)
            .into_iter()
            .map(|(name, _)| name)
            .collect();
        for class in [FILE_DIRECTORY_INFORMATION, FILE_BOTH_DIRECTORY_INFORMATION] {
            let plain: Vec<_> = collect(directory, api, class, 64 * 1024, false)
                .into_iter()
                .map(|(name, _)| name)
                .collect();
            assert_eq!(plain, names, "{api:?} class {class} names");
        }
    }
    unsafe { CloseHandle(directory) };

    // A native-backed directory (no merged entries) uses the native syscall,
    // whose ids are the native identities.
    let plain = root.join("plain");
    fs::create_dir_all(&plain).unwrap();
    fs::write(plain.join("file.txt"), b"plain").unwrap();
    let native = open(&plain, FILE_LIST_DIRECTORY);
    for api in [Query::Classic, Query::Extended] {
        for class in [
            FILE_ID_BOTH_DIRECTORY_INFORMATION,
            FILE_ID_FULL_DIRECTORY_INFORMATION,
        ] {
            for (name, id) in collect(native, api, class, 64 * 1024, false) {
                let path = match name.as_str() {
                    "." => plain.clone(),
                    ".." => root.to_path_buf(),
                    other => plain.join(other),
                };
                assert_eq!(id, native_id(&path), "{api:?} class {class} native {name}");
            }
        }
    }
    unsafe { CloseHandle(native) };
    fs::write(root.join("file-ids-done.txt"), b"done").unwrap();
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
        // Exercise the runner's TEMP alias and a deliberately different case.
        // The manifest is canonicalized independently of the caller's cwd.
        for cwd in [root.clone(), root.with_file_name("PROJECT 한글 SPACE")] {
            let output = Command::new(binary())
                .arg("--project")
                .arg(&root)
                .arg("--cache-dir")
                .arg(&cache)
                .args([
                    "--log-level=debug",
                    "--color=never",
                    "run",
                    "--",
                    "native-fixture",
                    "--exact",
                    "native_child",
                    "--nocapture",
                ])
                .env(MODE, "read")
                .env(PROJECT, &root)
                .current_dir(&cwd)
                .output()
                .unwrap();
            assert!(
                output.status.success(),
                "ZIP executable failed from {}: {} {}",
                cwd.display(),
                String::from_utf8_lossy(&output.stderr),
                String::from_utf8_lossy(&output.stdout)
            );
        }
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
fn merged_directory_records_carry_native_file_ids() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let cache = fixture.path().join("private-cache");
    // Deny the attribute read of one listed entry so enumeration meets an
    // entry it can list but may not be able to open. Whether the deny entry
    // blocks the open depends on the account, so the child compares each
    // record with what a native open of the entry actually reports.
    let restricted = root.join("restricted.txt");
    fs::write(&restricted, b"restricted").unwrap();
    let acl = |arguments: &[&str]| {
        let output = Command::new("icacls")
            .arg(&restricted)
            .args(arguments)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "icacls {arguments:?} failed: {} {}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
    };
    acl(&["/deny", "*S-1-1-0:(RA)"]);
    let output = run(&root, &cache, "file-ids").output().unwrap();
    acl(&["/remove:d", "*S-1-1-0"]);
    assert!(
        output.status.success(),
        "file id fixture failed {:?}: {} {}",
        output.status.code(),
        String::from_utf8_lossy(&output.stderr),
        String::from_utf8_lossy(&output.stdout)
    );
    assert!(
        root.join("file-ids-done.txt").is_file(),
        "the child assertions did not complete"
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
    let node = if Path::new(&node).is_absolute() {
        PathBuf::from(node)
    } else {
        pnport::executable::find_on_path(&node, std::env::var_os("PATH").as_deref(), &root)
            .expect("native Node executable is required")
    };
    let script = r#"
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const root = process.argv[1];
const stage = value => fs.writeSync(2, JSON.stringify({event: 'pnport_node_fixture', pid: process.pid, arch: process.arch, stage: value}) + '\n');
stage('read');
const dependency = path.join(root, 'node_modules/example/data.txt');
assert.equal(fs.readFileSync(dependency, 'utf8'), 'dependency');
stage('enumerate');
assert(fs.readdirSync(root).includes('node_modules'));
stage('worker_threads');
const { Worker } = require('node:worker_threads');
for (let index = 0; index < 4; index++) {
  new Worker(`const { workerData } = require('node:worker_threads');
    require('node:assert/strict').equal(require('node:fs').readFileSync(workerData, 'utf8'), 'dependency');`,
    { eval: true, workerData: dependency }).on('error', error => { throw error; });
}
stage('crypto');
assert.equal(require('node:crypto').randomBytes(32).length, 32);
stage('child');
const child = require('node:child_process').execFileSync(process.execPath,
  ['-e', 'process.stderr.write(JSON.stringify({event: "pnport_node_fixture", pid: process.pid, stage: "child_read"}) + "\\n"); process.stdout.write(require("node:fs").readFileSync(process.argv[1]))', dependency],
  { env: { SystemRoot: process.env.SystemRoot, PATH: process.env.PATH } });
assert.equal(child.toString(), 'dependency');
stage('loopback');
const net = require('node:net');
const server = net.createServer(socket => socket.end('loopback'));
server.listen(0, '127.0.0.1', () => {
  const client = net.connect(server.address().port, '127.0.0.1');
  let received = '';
  client.on('data', chunk => received += chunk);
  client.on('end', () => { assert.equal(received, 'loopback'); server.close(); });
});
"#;
    let output = node_output(
        Command::new(binary())
            .arg("--project")
            .arg(&root)
            .arg("--cache-dir")
            .arg(fixture.path().join("cache"))
            .args(["--log-level=debug", "--color=never", "run", "--"])
            .arg(node)
            .arg("-e")
            .arg(script)
            .arg(&root)
            .current_dir(&root),
    );
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

//! Native finite commands for tracing/installation validation. Never
//! distributed.
use std::{fs, process::Command, time::Duration};
fn main() {
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    match args.first().map(String::as_str).unwrap_or("read-write") {
        "read-write" => {
            let _ = fs::read("input.txt");
            let _ = fs::read("missing.txt");
            let _ = fs::read_dir(".");
            fs::create_dir_all("out").unwrap();
            fs::write(
                "out/result.txt",
                args.get(1).map(String::as_str).unwrap_or("stable"),
            )
            .unwrap();
            println!("child stdout preserved");
            eprintln!("child stderr preserved");
        }
        #[cfg(unix)]
        "rename" | "renameat" | "renameat2" | "renamex" | "renameatx" => {
            use std::{ffi::CString, os::fd::AsRawFd};
            let source = CString::new(args[1].as_str()).unwrap();
            let destination = CString::new(args[2].as_str()).unwrap();
            let destination_dir = fs::File::open(&args[3]).unwrap();
            let full_destination = CString::new(format!("{}/{}", args[3], args[2])).unwrap();
            // SAFETY: all strings remain alive and NUL-terminated, and the
            // directory descriptor remains open for the call.
            let result = unsafe {
                match args[0].as_str() {
                    "rename" => libc::rename(source.as_ptr(), full_destination.as_ptr()),
                    "renameat" => libc::renameat(
                        libc::AT_FDCWD,
                        source.as_ptr(),
                        destination_dir.as_raw_fd(),
                        destination.as_ptr(),
                    ),
                    #[cfg(target_os = "linux")]
                    "renameat2" => libc::renameat2(
                        libc::AT_FDCWD,
                        source.as_ptr(),
                        destination_dir.as_raw_fd(),
                        destination.as_ptr(),
                        0,
                    ),
                    #[cfg(target_os = "macos")]
                    "renamex" => libc::renamex_np(source.as_ptr(), full_destination.as_ptr(), 0),
                    #[cfg(target_os = "macos")]
                    "renameatx" => libc::renameatx_np(
                        libc::AT_FDCWD,
                        source.as_ptr(),
                        destination_dir.as_raw_fd(),
                        destination.as_ptr(),
                        0,
                    ),
                    _ => panic!("unsupported native rename fixture"),
                }
            };
            assert_eq!(result == 0, args[1] == "input.txt");
        }
        "policy-write" => {
            let output = std::path::Path::new(&args[1]);
            fs::create_dir_all(output.parent().unwrap()).unwrap();
            fs::write(output, "allowed output").unwrap();
            if let Some(extra) = args.get(2) {
                if extra.ends_with('/') {
                    fs::create_dir_all(extra).unwrap();
                } else {
                    fs::write(extra, "forbidden output").unwrap();
                }
            }
        }
        "read" => {
            let _ = fs::read(args.get(1).map(String::as_str).unwrap_or("input.txt"));
        }
        "fspy-environment" | "fspy-environment-child" => {
            let expected = if args[1] == "present" {
                Some(std::ffi::OsString::from("caller-selected"))
            } else {
                None
            };
            assert_eq!(std::env::var_os("FSPY"), expected);
            let _ = fs::read("input.txt");
            if args[0] == "fspy-environment-child" {
                let status = Command::new(std::env::current_exe().unwrap())
                    .args(["fspy-environment", &args[1]])
                    .status()
                    .unwrap();
                std::process::exit(status.code().unwrap_or(1));
            }
        }
        "list" => {
            let _ = fs::read_dir("out");
        }
        #[cfg(target_os = "macos")]
        "protected-child" => {
            let status = Command::new("/bin/sh")
                .args(["-c", "printf original > protected-child-result"])
                .status()
                .unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        "child" => {
            let status = Command::new(std::env::current_exe().unwrap())
                .arg("read-write")
                .status()
                .unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        "native-child" => {
            let status = Command::new(&args[1]).arg("read-write").status().unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        #[cfg(unix)]
        "invalid-payload-child" => {
            let status = Command::new(std::env::current_exe().unwrap())
                .env("FSPY_PAYLOAD", "invalid")
                .arg("read-write")
                .status()
                .unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        "linger" => {
            #[allow(
                clippy::zombie_processes,
                reason = "the fixture deliberately leaves a child for Runlens process-group \
                          reaping"
            )]
            let _child = Command::new(std::env::current_exe().unwrap())
                .arg("sleep")
                .spawn()
                .unwrap();
        }
        #[cfg(unix)]
        "execl-many" => {
            use std::os::unix::ffi::OsStrExt;
            let executable =
                std::ffi::CString::new(std::env::current_exe().unwrap().as_os_str().as_bytes())
                    .unwrap();
            unsafe {
                libc::execl(
                    executable.as_ptr(),
                    executable.as_ptr(),
                    c"check-argv".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"x".as_ptr(),
                    c"last".as_ptr(),
                    std::ptr::null::<libc::c_char>(),
                );
            }
            panic!("execl should replace the fixture");
        }
        "check-argv" => {
            assert_eq!(args.len(), 34);
            assert_eq!(args.last().unwrap(), "last");
            let _ = fs::read("input.txt");
        }
        "sleep" => std::thread::sleep(Duration::from_secs(60)),
        #[cfg(unix)]
        "removed-cwd" => {
            let root = std::env::current_dir().unwrap();
            let nested = root.join("removed-cwd");
            fs::create_dir(&nested).unwrap();
            std::env::set_current_dir(&nested).unwrap();
            fs::remove_dir(&nested).unwrap();
            assert!(fs::read("missing").is_err());
            fs::write(
                root.join("continued"),
                "collection failure did not abort execution",
            )
            .unwrap();
            std::env::set_current_dir(root).unwrap();
        }
        "ready-sleep" => {
            fs::write("child-ready", "ready").unwrap();
            std::thread::sleep(Duration::from_secs(60));
        }
        "fail" => std::process::exit(23),
        "vary-content" | "vary-set" | "vary-permissions" => {
            fs::create_dir_all("out").unwrap();
            let cwd = std::env::current_dir().unwrap();
            let round = cwd.parent().unwrap().file_name().unwrap().to_str().unwrap();
            let path = if args[0] == "vary-set" {
                format!("out/{round}")
            } else {
                "out/result.txt".into()
            };
            fs::write(
                &path,
                if args[0] == "vary-content" {
                    round
                } else {
                    "stable"
                },
            )
            .unwrap();
            #[cfg(unix)]
            if args[0] == "vary-permissions" {
                use std::os::unix::fs::PermissionsExt;
                fs::set_permissions(
                    path,
                    fs::Permissions::from_mode(if round == "round-1" { 0o755 } else { 0o644 }),
                )
                .unwrap();
            }
        }
        "benchmark" => {
            let mut bytes = 0usize;
            for entry in fs::read_dir("inputs").unwrap() {
                bytes += fs::read(entry.unwrap().path()).unwrap().len();
            }
            std::hint::black_box(bytes);
        }
        "overflow" => {
            for index in 0..10000 {
                let _ = fs::metadata(format!("missing-{index}"));
            }
        }
        "stdin" => {
            let mut input = String::new();
            use std::io::Read;
            std::io::stdin().read_to_string(&mut input).unwrap();
            print!("{input}");
        }
        "env" => {
            fs::create_dir_all("out").unwrap();
            let path =
                std::path::Path::new(&std::env::var_os("HOME").unwrap()).join("previous-run");
            assert!(!path.exists());
            fs::write(&path, "local cache").unwrap();
            assert!(std::env::var_os("RUNLENS_AMBIENT_SECRET").is_none());
            fs::write("out/result.txt", "stable").unwrap();
        }
        _ => std::process::exit(2),
    }
}

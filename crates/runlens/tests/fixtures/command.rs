//! Native finite commands for tracing/installation validation. Never
//! distributed.
use std::{fs, process::Command, time::Duration};
#[cfg(windows)]
mod windows_native;
fn main() {
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    match args.first().map(String::as_str).unwrap_or("read-write") {
        #[cfg(target_os = "linux")]
        "uring-setup" | "uring-sqpoll" | "uring-enter" | "uring-register" => {
            // Linux UAPI io_uring_params is 120 bytes and 8-byte aligned. Zero
            // initializes every reserved field; SQPOLL is bit 1 of flags (u32 #2).
            let mut params = [0_u64; 15];
            if args[0] == "uring-sqpoll" {
                params[1] = 2;
            }
            // SAFETY: setup receives a live, correctly sized parameter buffer.
            // The other calls intentionally use an invalid fd and null buffers.
            let result = unsafe {
                match args[0].as_str() {
                    "uring-enter" => libc::syscall(libc::SYS_io_uring_enter, -1, 0, 0, 0, 0, 0),
                    "uring-register" => libc::syscall(libc::SYS_io_uring_register, -1, 0, 0, 0),
                    _ => libc::syscall(libc::SYS_io_uring_setup, 2, params.as_mut_ptr()),
                }
            };
            if result < 0 {
                println!(
                    "error:{}",
                    std::io::Error::last_os_error().raw_os_error().unwrap()
                );
            } else {
                // SAFETY: successful setup returns an owned ring descriptor.
                unsafe {
                    libc::close(result as i32);
                }
                println!("created");
            }
        }
        #[cfg(target_os = "linux")]
        "file-handle" => {
            let path = std::path::Path::new(&args[1]);
            let full = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let parent = std::ffi::CString::new(path.parent().unwrap().to_str().unwrap()).unwrap();
            let leaf = std::ffi::CString::new(path.file_name().unwrap().to_str().unwrap()).unwrap();
            // Zero-length handle storage exercises the native sizing result
            // without retaining an opaque handle or requiring export support.
            let mut handle = [0_u32; 2];
            let mut mount = 0_i32;
            // SAFETY: all operands remain live; the two-u32 header advertises no payload.
            unsafe {
                let (fd, name, flags) = match args[2].as_str() {
                    "relative" => (
                        libc::open(parent.as_ptr(), libc::O_RDONLY | libc::O_DIRECTORY),
                        leaf.as_ptr(),
                        0,
                    ),
                    "descriptor" => (
                        libc::open(full.as_ptr(), libc::O_PATH),
                        c"".as_ptr(),
                        libc::AT_EMPTY_PATH,
                    ),
                    _ => (libc::AT_FDCWD, full.as_ptr(), 0),
                };
                let result = libc::syscall(
                    libc::SYS_name_to_handle_at,
                    fd,
                    name,
                    handle.as_mut_ptr(),
                    &mut mount,
                    flags,
                );
                if result < 0 {
                    println!(
                        "error={}",
                        std::io::Error::last_os_error().raw_os_error().unwrap()
                    );
                } else {
                    println!("resolved");
                }
                if fd >= 0 {
                    libc::close(fd);
                }
            }
        }
        #[cfg(target_os = "linux")]
        "fanotify-watch" => {
            let path = std::path::Path::new(&args[1]);
            let full = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let parent = std::ffi::CString::new(path.parent().unwrap().to_str().unwrap()).unwrap();
            let leaf = std::ffi::CString::new(path.file_name().unwrap().to_str().unwrap()).unwrap();
            // SAFETY: live path operands and owned descriptors; mark is attempted
            // even when the host rejects group creation, preserving that error.
            unsafe {
                let fd = libc::syscall(
                    libc::SYS_fanotify_init,
                    libc::FAN_CLOEXEC | libc::FAN_NONBLOCK | libc::FAN_REPORT_FID,
                    libc::O_RDONLY,
                ) as i32;
                let (dir, name) = match args[2].as_str() {
                    "relative" => (
                        libc::open(parent.as_ptr(), libc::O_RDONLY | libc::O_DIRECTORY),
                        leaf.as_ptr(),
                    ),
                    "descriptor" => (libc::open(full.as_ptr(), libc::O_RDONLY), std::ptr::null()),
                    _ => (libc::AT_FDCWD, full.as_ptr()),
                };
                let result = libc::syscall(
                    libc::SYS_fanotify_mark,
                    fd,
                    libc::FAN_MARK_ADD,
                    libc::FAN_ACCESS,
                    dir,
                    name,
                );
                let error = std::io::Error::last_os_error().raw_os_error().unwrap();
                if result < 0 {
                    println!("error={error}");
                } else {
                    println!("registered");
                }
                if fd >= 0 {
                    libc::close(fd);
                }
                if dir >= 0 {
                    libc::close(dir);
                }
            }
        }
        #[cfg(target_os = "linux")]
        "inotify-watch" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            // SAFETY: the path remains a terminated string and fd is owned here.
            unsafe {
                let fd = libc::inotify_init1(libc::IN_CLOEXEC | libc::IN_NONBLOCK);
                assert!(fd >= 0);
                let result = libc::syscall(
                    libc::SYS_inotify_add_watch,
                    fd,
                    path.as_ptr(),
                    libc::IN_ACCESS,
                );
                println!("{}", if result >= 0 { 0 } else { -1 });
                libc::close(fd);
            }
        }
        "scan-temporary" => {
            fn scan(path: &std::path::Path, depth: usize) -> usize {
                if depth > 4 {
                    return 0;
                }
                let mut files = 0;
                for entry in fs::read_dir(path).unwrap() {
                    let entry = entry.unwrap();
                    if entry.file_type().unwrap().is_dir() {
                        scan(&entry.path(), depth + 1);
                    } else {
                        let _ = fs::read(entry.path());
                        files += 1;
                    }
                }
                files
            }
            println!("{}", scan(std::path::Path::new(&args[1]), 0));
        }
        #[cfg(unix)]
        "chdir" | "fchdir" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            // SAFETY: both paths remain valid terminated strings and the owned
            // descriptor stays open through fchdir. Print only the OS result.
            let result = unsafe {
                if args[0] == "chdir" {
                    libc::chdir(path.as_ptr())
                } else {
                    let original = std::ffi::CString::new(args[2].as_bytes()).unwrap();
                    let fd = libc::open(original.as_ptr(), libc::O_RDONLY | libc::O_DIRECTORY);
                    assert!(fd >= 0);
                    assert_eq!(libc::rename(original.as_ptr(), path.as_ptr()), 0);
                    let result = libc::fchdir(fd);
                    libc::close(fd);
                    result
                }
            };
            println!("{result}");
        }
        #[cfg(target_os = "macos")]
        "macos-raw-abi" => {
            let mut mib = [libc::CTL_KERN, libc::KERN_OSTYPE];
            let mut bytes = [0_u8; 64];
            let mut length = bytes.len();
            // SAFETY: valid sysctl buffers and six operands exercise register
            // and stack forwarding; getpid/close cover zero/one and errno.
            unsafe {
                assert_eq!(libc::syscall(20), libc::getpid());
                assert_eq!(libc::syscall(6, -1), -1);
                assert_eq!(*libc::__error(), libc::EBADF);
                let result = libc::syscall(
                    202,
                    mib.as_mut_ptr(),
                    2_u32,
                    bytes.as_mut_ptr(),
                    &mut length,
                    std::ptr::null::<u8>(),
                    0_usize,
                );
                assert_eq!(result, 0);
                println!("{}", String::from_utf8_lossy(&bytes[..length]));
            }
        }
        #[cfg(target_os = "macos")]
        "macos-fork-raw" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            // SAFETY: the fork child uses only scalar syscalls/live buffers and
            // finite delay; no Rust allocation or lock is used after fork.
            unsafe {
                let fd = libc::open(path.as_ptr(), libc::O_WRONLY | libc::O_CREAT, 0o600);
                assert!(fd >= 0);
                let mut ready = [0_i32; 2];
                assert_eq!(libc::pipe(ready.as_mut_ptr()), 0);
                let pid = libc::fork();
                assert!(pid >= 0);
                if pid == 0 {
                    // Inline kernel entry deliberately bypasses every symbol
                    // interposer. The parent fork boundary must already mark loss.
                    #[cfg(target_arch = "aarch64")]
                    core::arch::asm!("svc #0x80", in("x16") 147_u64, lateout("x0") _, lateout("x1") _);
                    #[cfg(target_arch = "x86_64")]
                    core::arch::asm!("syscall", inlateout("rax") 0x0200_0093_u64 => _, lateout("rcx") _, lateout("r11") _);
                    if libc::getpgrp() != libc::getpid() {
                        libc::_exit(98);
                    }
                    libc::close(ready[0]);
                    libc::write(ready[1], b"x".as_ptr().cast(), 1);
                    libc::close(ready[1]);
                    libc::close(0);
                    libc::close(1);
                    libc::close(2);
                    libc::usleep(200_000);
                    libc::write(fd, b"done".as_ptr().cast(), 4);
                    libc::_exit(0);
                }
                libc::close(ready[1]);
                let mut byte = 0_u8;
                assert_eq!(libc::read(ready[0], (&mut byte as *mut u8).cast(), 1), 1);
                libc::close(ready[0]);
                libc::close(fd);
            }
        }
        #[cfg(target_os = "macos")]
        "macos-spawn-open" => {
            let program =
                std::ffi::CString::new(std::env::current_exe().unwrap().to_str().unwrap()).unwrap();
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let argv = [
                program.as_ptr().cast_mut(),
                c"spawn-stdin".as_ptr().cast_mut(),
                std::ptr::null_mut(),
            ];
            unsafe extern "C" {
                static environ: *const *mut libc::c_char;
            }
            // SAFETY: initialized native actions, live strings and environment;
            // the successful finite child is reaped before destroying actions.
            unsafe {
                let mut actions = std::mem::zeroed();
                assert_eq!(libc::posix_spawn_file_actions_init(&mut actions), 0);
                assert_eq!(
                    libc::posix_spawn_file_actions_addopen(
                        &mut actions,
                        0,
                        path.as_ptr(),
                        libc::O_RDONLY,
                        0
                    ),
                    0
                );
                let mut pid = 0;
                let spawn = if args[2] == "search" {
                    libc::posix_spawnp
                } else {
                    libc::posix_spawn
                };
                let result = spawn(
                    &mut pid,
                    program.as_ptr(),
                    &actions,
                    std::ptr::null(),
                    argv.as_ptr(),
                    environ,
                );
                if result == 0 {
                    let mut status = 0;
                    assert_eq!(libc::waitpid(pid, &mut status, 0), pid);
                    assert_eq!(status, 0);
                }
                println!("spawn={result}");
                assert_eq!(libc::posix_spawn_file_actions_destroy(&mut actions), 0);
            }
        }
        "source-timestamps" => {
            let source = fs::metadata("input.txt").unwrap().modified().unwrap();
            let directory = fs::metadata("source-dir").unwrap().modified().unwrap();
            fs::write("out/result", format!("{source:?}/{directory:?}")).unwrap();
        }
        "spawn-stdin" => {
            print!("{}", std::io::read_to_string(std::io::stdin()).unwrap());
        }
        #[cfg(target_os = "macos")]
        "macos-execvp-child" => {
            let status = Command::new(std::env::current_exe().unwrap())
                .arg("macos-execvp-custom")
                .args(&args[1..])
                .status()
                .unwrap();
            std::process::exit(status.code().unwrap_or(99));
        }
        #[cfg(target_os = "macos")]
        "macos-execvp-custom" => {
            let prog = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let search = std::ffi::CString::new(args[2].as_bytes()).unwrap();
            let values: Vec<_> = std::iter::once(&args[1])
                .chain(args[3..].iter())
                .map(|arg| std::ffi::CString::new(arg.as_bytes()).unwrap())
                .collect();
            let mut argv: Vec<_> = values.iter().map(|arg| arg.as_ptr().cast_mut()).collect();
            argv.push(std::ptr::null_mut());
            // SAFETY: all strings and the NULL-terminated argv remain live.
            unsafe {
                libc::execvP(prog.as_ptr(), search.as_ptr(), argv.as_ptr());
            }
            println!(
                "error={}",
                std::io::Error::last_os_error().raw_os_error().unwrap()
            );
        }
        #[cfg(unix)]
        "path-exec" => {
            let path = std::ffi::CString::new(args[2].as_bytes()).unwrap();
            let argv = [
                path.as_ptr(),
                c"fallback-argument".as_ptr(),
                std::ptr::null(),
            ];
            // SAFETY: terminated live strings and argv, with a valid current environment.
            unsafe {
                match args[1].as_str() {
                    "execvp" => {
                        libc::execvp(path.as_ptr(), argv.as_ptr());
                    }
                    "execlp" => {
                        libc::execlp(
                            path.as_ptr(),
                            path.as_ptr(),
                            argv[1],
                            std::ptr::null::<libc::c_char>(),
                        );
                    }
                    #[cfg(target_os = "linux")]
                    "execvpe" => {
                        unsafe extern "C" {
                            static environ: *const *const libc::c_char;
                        }
                        libc::execvpe(path.as_ptr(), argv.as_ptr(), environ);
                    }
                    _ => panic!("unknown exec family"),
                }
            }
            println!(
                "{}",
                std::io::Error::last_os_error().raw_os_error().unwrap()
            );
        }
        #[cfg(target_os = "linux")]
        "libc-execveat" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let argv = [path.as_ptr(), c"read".as_ptr(), std::ptr::null()];
            unsafe extern "C" {
                static environ: *const *const libc::c_char;
            }
            let flags = match args[2].as_str() {
                "nofollow" => libc::AT_SYMLINK_NOFOLLOW,
                "invalid" => 0x40000000,
                "empty" => libc::AT_EMPTY_PATH,
                _ => 0,
            };
            // SAFETY: live C strings and process environment. Success replaces
            // this fixture; failures print only the kernel's numeric errno.
            unsafe {
                let fd = if flags == libc::AT_EMPTY_PATH {
                    libc::open(path.as_ptr(), libc::O_RDONLY)
                } else {
                    libc::AT_FDCWD
                };
                libc::execveat(
                    fd,
                    if flags == libc::AT_EMPTY_PATH {
                        c"".as_ptr()
                    } else {
                        path.as_ptr()
                    },
                    argv.as_ptr().cast(),
                    environ.cast(),
                    flags,
                );
            }
            println!(
                "{}",
                std::io::Error::last_os_error().raw_os_error().unwrap()
            );
        }
        #[cfg(target_os = "linux")]
        "execve-script" | "execveat-script" | "execveat-empty-script" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let argv = [path.as_ptr(), std::ptr::null()];
            unsafe extern "C" {
                static environ: *const *const libc::c_char;
            }
            // SAFETY: live pathname/argv and process-owned environment; a
            // successful raw exec replaces this fixture without preload adaptation.
            unsafe {
                if args[0] == "execve-script" {
                    libc::syscall(libc::SYS_execve, path.as_ptr(), argv.as_ptr(), environ);
                } else {
                    let empty = args[0] == "execveat-empty-script";
                    let fd = if empty {
                        libc::open(path.as_ptr(), libc::O_RDONLY)
                    } else {
                        libc::AT_FDCWD
                    };
                    libc::syscall(
                        libc::SYS_execveat,
                        fd,
                        if empty { c"".as_ptr() } else { path.as_ptr() },
                        argv.as_ptr(),
                        environ,
                        if empty { libc::AT_EMPTY_PATH } else { 0 },
                    );
                }
            }
            panic!(
                "raw script exec failed: {}",
                std::io::Error::last_os_error()
            );
        }
        #[cfg(target_os = "linux")]
        "getxattr" | "lgetxattr" | "fgetxattr" | "listxattr" | "llistxattr" | "flistxattr" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let mut buffer = [0u8; 4096];
            // SAFETY: bounded output and live name/path. Opening descriptor
            // variants write-only ensures an open-read cannot mask missing hooks.
            unsafe {
                match args[0].as_str() {
                    "getxattr" | "lgetxattr" => {
                        let number = if args[0] == "getxattr" {
                            libc::SYS_getxattr
                        } else {
                            libc::SYS_lgetxattr
                        };
                        libc::syscall(
                            number,
                            path.as_ptr(),
                            c"user.runlens".as_ptr(),
                            buffer.as_mut_ptr(),
                            buffer.len(),
                        );
                    }
                    "listxattr" | "llistxattr" => {
                        let number = if args[0] == "listxattr" {
                            libc::SYS_listxattr
                        } else {
                            libc::SYS_llistxattr
                        };
                        libc::syscall(number, path.as_ptr(), buffer.as_mut_ptr(), buffer.len());
                    }
                    _ => {
                        let fd = libc::open(path.as_ptr(), libc::O_WRONLY);
                        if args[0] == "fgetxattr" {
                            libc::syscall(
                                libc::SYS_fgetxattr,
                                fd,
                                c"user.runlens".as_ptr(),
                                buffer.as_mut_ptr(),
                                buffer.len(),
                            );
                        } else {
                            libc::syscall(
                                libc::SYS_flistxattr,
                                fd,
                                buffer.as_mut_ptr(),
                                buffer.len(),
                            );
                        }
                        libc::close(fd);
                    }
                }
            }
        }
        #[cfg(target_os = "linux")]
        "statfs" | "statvfs" | "fstatfs" | "fstatvfs" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            // SAFETY: live pathname and exact output structures. Write-only
            // descriptor opens cannot mask absent metadata read collection.
            unsafe {
                let mut raw = std::mem::MaybeUninit::<libc::statfs>::zeroed();
                let mut vfs = std::mem::MaybeUninit::<libc::statvfs>::zeroed();
                match args[0].as_str() {
                    "statfs" => {
                        libc::syscall(libc::SYS_statfs, path.as_ptr(), raw.as_mut_ptr());
                    }
                    "statvfs" => {
                        libc::statvfs(path.as_ptr(), vfs.as_mut_ptr());
                    }
                    mode => {
                        let fd = libc::open(path.as_ptr(), libc::O_WRONLY);
                        if mode == "fstatfs" {
                            libc::syscall(libc::SYS_fstatfs, fd, raw.as_mut_ptr());
                        } else {
                            libc::fstatvfs(fd, vfs.as_mut_ptr());
                        }
                        libc::close(fd);
                    }
                }
            }
        }
        #[cfg(target_os = "linux")]
        "stat-empty-path" => {
            // A null AT_EMPTY_PATH lookup is valid on Linux 6.11+. The empty
            // string form works on older minimum-OS kernels as well.
            unsafe {
                let mut stat = std::mem::MaybeUninit::<libc::statx>::zeroed();
                assert_eq!(
                    libc::syscall(
                        libc::SYS_statx,
                        -1,
                        std::ptr::null::<libc::c_char>(),
                        0,
                        libc::STATX_BASIC_STATS,
                        stat.as_mut_ptr()
                    ),
                    -1
                );
                for path in [std::ptr::null(), c"".as_ptr()] {
                    libc::syscall(
                        libc::SYS_statx,
                        libc::AT_FDCWD,
                        path,
                        libc::AT_EMPTY_PATH,
                        libc::STATX_BASIC_STATS,
                        stat.as_mut_ptr(),
                    );
                    let mut stat = std::mem::MaybeUninit::<libc::stat>::zeroed();
                    let number = libc::SYS_newfstatat;
                    libc::syscall(
                        number,
                        libc::AT_FDCWD,
                        path,
                        stat.as_mut_ptr(),
                        libc::AT_EMPTY_PATH,
                    );
                }
            }
        }
        #[cfg(target_os = "macos")]
        "macos-getxattr" | "macos-fgetxattr" | "macos-listxattr" | "macos-flistxattr" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let mut bytes = [0u8; 4096];
            let query = args[2] == "size";
            let (buffer, size) = if query {
                (std::ptr::null_mut(), 0)
            } else {
                (bytes.as_mut_ptr(), bytes.len())
            };
            // SAFETY: live C strings, bounded output, and fixture-owned fd.
            let (result, error) = unsafe {
                let fd = if args[0].starts_with("macos-f") {
                    let fd = libc::open(path.as_ptr(), libc::O_RDONLY);
                    assert!(fd >= 0);
                    fd
                } else {
                    -1
                };
                let result = match args[0].as_str() {
                    "macos-getxattr" => libc::getxattr(
                        path.as_ptr(),
                        c"user.ATTR-NAME-CANARY".as_ptr(),
                        buffer.cast(),
                        size,
                        0,
                        libc::XATTR_NOFOLLOW,
                    ),
                    "macos-fgetxattr" => libc::fgetxattr(
                        fd,
                        c"user.ATTR-NAME-CANARY".as_ptr(),
                        buffer.cast(),
                        size,
                        0,
                        0,
                    ),
                    "macos-listxattr" => {
                        libc::listxattr(path.as_ptr(), buffer.cast(), size, libc::XATTR_NOFOLLOW)
                    }
                    _ => libc::flistxattr(fd, buffer.cast(), size, 0),
                };
                let error = std::io::Error::last_os_error().raw_os_error().unwrap();
                if fd >= 0 {
                    libc::close(fd);
                }
                (result, error)
            };
            if result < 0 {
                println!("error={error}");
            } else if query {
                println!("size={result}");
            } else {
                println!("bytes={result}:{:?}", &bytes[..result as usize]);
            }
        }
        #[cfg(target_os = "macos")]
        "libc-readlink" | "libc-readlinkat" => {
            let path = std::path::Path::new(&args[1]);
            let full = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let mut buffer = [0u8; 4096];
            // SAFETY: names and bounded output remain live. The fixture owns
            // the directory descriptor and forwards it without changing cwd.
            let result = unsafe {
                if args[0] == "libc-readlink" {
                    libc::readlink(full.as_ptr(), buffer.as_mut_ptr().cast(), buffer.len())
                } else {
                    let parent =
                        std::ffi::CString::new(path.parent().unwrap().to_str().unwrap()).unwrap();
                    let name = std::ffi::CString::new(path.file_name().unwrap().to_str().unwrap())
                        .unwrap();
                    let fd = libc::open(parent.as_ptr(), libc::O_RDONLY | libc::O_DIRECTORY);
                    assert!(fd >= 0);
                    let result = libc::readlinkat(
                        fd,
                        name.as_ptr(),
                        buffer.as_mut_ptr().cast(),
                        buffer.len(),
                    );
                    let error = std::io::Error::last_os_error().raw_os_error().unwrap();
                    libc::close(fd);
                    if result < 0 {
                        println!("error={error}");
                        return;
                    }
                    result
                }
            };
            if result < 0 {
                println!(
                    "error={}",
                    std::io::Error::last_os_error().raw_os_error().unwrap()
                );
            } else {
                println!(
                    "bytes={result}:{}",
                    String::from_utf8_lossy(&buffer[..result as usize])
                );
            }
        }
        #[cfg(target_os = "linux")]
        "readlink" | "readlinkat" | "readlinkat-empty" => {
            let path = std::ffi::CString::new(args[1].as_bytes()).unwrap();
            let mut buffer = [0u8; 4096];
            // SAFETY: each syscall receives a live C pathname and bounded output.
            unsafe {
                #[cfg(target_arch = "x86_64")]
                if args[0] == "readlink" {
                    libc::syscall(
                        libc::SYS_readlink,
                        path.as_ptr(),
                        buffer.as_mut_ptr(),
                        buffer.len(),
                    );
                    return;
                }
                let fd = if args[0] == "readlinkat-empty" {
                    libc::open(path.as_ptr(), libc::O_PATH | libc::O_NOFOLLOW)
                } else {
                    libc::AT_FDCWD
                };
                let name = if args[0] == "readlinkat-empty" {
                    c"".as_ptr()
                } else {
                    path.as_ptr()
                };
                libc::syscall(
                    libc::SYS_readlinkat,
                    fd,
                    name,
                    buffer.as_mut_ptr(),
                    buffer.len(),
                );
                if fd >= 0 {
                    libc::close(fd);
                }
            }
        }
        #[cfg(unix)]
        "transient-symlink" => {
            if args.get(2).is_some_and(|state| state == "before") {
                fs::rename("transient", "original-directory").unwrap();
            }
            std::os::unix::fs::symlink(&args[1], "transient").unwrap();
            assert_eq!(
                fs::read_to_string("transient/input.txt").unwrap(),
                "external"
            );
            fs::remove_file("transient").unwrap();
            if args.get(2).is_some_and(|state| state == "after") {
                fs::create_dir("transient").unwrap();
            }
        }
        #[cfg(target_os = "linux")]
        "syscall-arities" => {
            // SAFETY: each libc syscall receives precisely its documented
            // arguments. A generic Rust varargs hook must never consume six.
            unsafe {
                assert_eq!(
                    libc::syscall(libc::SYS_getpid),
                    libc::getpid() as libc::c_long
                );
                assert!(libc::syscall(libc::SYS_gettid) > 0);
                assert_eq!(libc::syscall(libc::SYS_close, -1), -1);
                let fd = libc::syscall(
                    libc::SYS_openat,
                    libc::AT_FDCWD,
                    c"input.txt".as_ptr(),
                    libc::O_RDONLY,
                );
                assert!(fd >= 0);
                assert_eq!(libc::syscall(libc::SYS_close, fd), 0);
                let mut stat = std::mem::MaybeUninit::<libc::statx>::zeroed();
                assert_eq!(
                    libc::syscall(
                        libc::SYS_statx,
                        libc::AT_FDCWD,
                        c"input.txt".as_ptr(),
                        0,
                        libc::STATX_SIZE,
                        stat.as_mut_ptr()
                    ),
                    0
                );
                assert_eq!(stat.assume_init().stx_size, 5);
            }
        }
        #[cfg(target_os = "linux")]
        "delete-raw-syscall" => {
            let path = std::ffi::CString::new(args[1].as_str()).unwrap();
            // SAFETY: live C pathname and scalar unlinkat arguments; bypass
            // every libc symbol to verify kernel-level dynamic-image coverage.
            unsafe {
                #[cfg(target_arch = "x86_64")]
                core::arch::asm!("syscall", inlateout("rax") libc::SYS_unlinkat => _,
                    in("rdi") libc::AT_FDCWD as i64, in("rsi") path.as_ptr(), in("rdx") 0usize,
                    lateout("rcx") _, lateout("r11") _, options(nostack));
                #[cfg(target_arch = "aarch64")]
                core::arch::asm!("svc 0", in("x8") libc::SYS_unlinkat,
                    inlateout("x0") libc::AT_FDCWD as i64 => _, in("x1") path.as_ptr(),
                    in("x2") 0usize, options(nostack));
            }
        }
        #[cfg(unix)]
        mode if mode.starts_with("mutate-") => {
            use std::{ffi::CString, os::fd::AsRawFd};
            let path = CString::new(args[1].as_str()).unwrap();
            let base = fs::File::open(&args[2]).unwrap();
            let relative = CString::new(
                std::path::Path::new(&args[1])
                    .strip_prefix(&args[2])
                    .unwrap()
                    .to_str()
                    .unwrap(),
            )
            .unwrap();
            // SAFETY: caller-owned strings and the directory descriptor stay
            // live; failures are deliberate observations, not fixture failures.
            unsafe {
                match mode {
                    "mutate-mkdir" => {
                        libc::mkdir(path.as_ptr(), 0o700);
                    }
                    "mutate-mkdirat" => {
                        libc::mkdirat(base.as_raw_fd(), relative.as_ptr(), 0o700);
                    }
                    "mutate-chmod" => {
                        libc::chmod(path.as_ptr(), 0o600);
                    }
                    "mutate-chmodat" => {
                        libc::fchmodat(base.as_raw_fd(), relative.as_ptr(), 0o600, 0);
                    }
                    "mutate-chown" => {
                        libc::chown(path.as_ptr(), libc::getuid(), libc::getgid());
                    }
                    "mutate-chownat" => {
                        libc::fchownat(
                            base.as_raw_fd(),
                            relative.as_ptr(),
                            libc::getuid(),
                            libc::getgid(),
                            0,
                        );
                    }
                    "mutate-truncate" => {
                        libc::truncate(path.as_ptr(), 0);
                    }
                    "mutate-utimes" => {
                        libc::utimes(path.as_ptr(), std::ptr::null());
                    }
                    #[cfg(target_os = "linux")]
                    "mutate-futimesat" => {
                        unsafe extern "C" {
                            fn futimesat(
                                fd: libc::c_int,
                                path: *const libc::c_char,
                                times: *const libc::timeval,
                            ) -> libc::c_int;
                        }
                        futimesat(base.as_raw_fd(), relative.as_ptr(), std::ptr::null());
                    }
                    "mutate-utimensat" => {
                        libc::utimensat(base.as_raw_fd(), relative.as_ptr(), std::ptr::null(), 0);
                    }
                    "mutate-link" => {
                        libc::link(c"input.txt".as_ptr(), path.as_ptr());
                    }
                    "mutate-linkat" => {
                        libc::linkat(
                            libc::AT_FDCWD,
                            c"input.txt".as_ptr(),
                            base.as_raw_fd(),
                            relative.as_ptr(),
                            0,
                        );
                    }
                    "mutate-symlink" => {
                        libc::symlink(c"opaque-target".as_ptr(), path.as_ptr());
                    }
                    "mutate-symlinkat" => {
                        libc::symlinkat(
                            c"opaque-target".as_ptr(),
                            base.as_raw_fd(),
                            relative.as_ptr(),
                        );
                    }
                    _ => panic!("unknown mutation fixture"),
                }
            }
        }
        #[cfg(windows)]
        "windows-unresolved-relative-root" => windows_native::unresolved_relative_root(),
        #[cfg(windows)]
        "windows-malformed-file-attributes" => windows_native::malformed_file_attributes(),
        #[cfg(windows)]
        "windows-native-delete" => windows_native::delete_file(&args[1]),
        #[cfg(windows)]
        "windows-native-child" => windows_native::spawn(&args[1]),
        #[cfg(windows)]
        "windows-native-leaf" => fs::write(&args[1], "native child output").unwrap(),
        #[cfg(windows)]
        "windows-rename" | "windows-delete" => {
            if args[0] == "windows-rename" {
                fs::rename(&args[1], &args[2]).unwrap();
            } else {
                fs::remove_file(&args[1]).unwrap();
            }
        }
        #[cfg(windows)]
        "windows-create-readonly"
        | "windows-open-if-readonly"
        | "windows-overwrite-readonly"
        | "windows-open-readonly" => {
            use std::os::windows::fs::OpenOptionsExt;
            let mut options = fs::OpenOptions::new();
            options.read(true).write(true).access_mode(0x8000_0000); // GENERIC_READ overrides access rights.
            match args[0].as_str() {
                "windows-create-readonly" => {
                    options.create_new(true);
                }
                "windows-open-if-readonly" => {
                    options.create(true);
                }
                "windows-overwrite-readonly" => {
                    options.create(true).truncate(true);
                }
                _ => {}
            }
            let opened = options.open(&args[1]);
            if args[0] == "windows-create-readonly" {
                assert!(opened.is_ok());
            }
            // Failed attempts must also retain the disposition's write intent.
        }
        "large-output" => {
            // Sparse output keeps this fixture cheap while ensuring the
            // after-snapshot has hashing work after its lifecycle log event.
            fs::File::create("large-output")
                .unwrap()
                .set_len(1024 * 1024 * 1024)
                .unwrap();
        }
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
        #[cfg(unix)]
        "delete-unlink"
        | "delete-unlinkat"
        | "delete-unlinkat-relative"
        | "delete-rmdir"
        | "delete-directory-at"
        | "delete-remove" => {
            use std::{ffi::CString, os::fd::AsRawFd};
            let path = CString::new(args[1].as_str()).unwrap();
            let directory = fs::File::open(&args[2]).unwrap();
            let relative = CString::new(
                std::path::Path::new(&args[1])
                    .file_name()
                    .unwrap()
                    .to_str()
                    .unwrap(),
            )
            .unwrap();
            // SAFETY: all path strings and the directory descriptor remain live.
            let result = unsafe {
                match args[0].as_str() {
                    "delete-unlink" => libc::unlink(path.as_ptr()),
                    "delete-rmdir" => libc::rmdir(path.as_ptr()),
                    "delete-remove" => libc::remove(path.as_ptr()),
                    "delete-unlinkat" => libc::unlinkat(libc::AT_FDCWD, path.as_ptr(), 0),
                    "delete-unlinkat-relative" => {
                        libc::unlinkat(directory.as_raw_fd(), relative.as_ptr(), 0)
                    }
                    "delete-directory-at" => {
                        libc::unlinkat(directory.as_raw_fd(), relative.as_ptr(), libc::AT_REMOVEDIR)
                    }
                    _ => unreachable!(),
                }
            };
            assert_eq!(result == 0, args[3] == "present");
        }
        #[cfg(unix)]
        "open-create" | "open-truncate" | "open-read" | "stream-rplus" | "stream-wplus"
        | "stream-aplus" | "stream-read" => {
            let path = std::ffi::CString::new(args[1].as_str()).unwrap();
            // SAFETY: NUL-terminated strings remain alive, and only successful
            // handles are used and closed. Rejected opens still exercise attempts.
            unsafe {
                if args[0].starts_with("stream-") {
                    let mode = match args[0].as_str() {
                        "stream-rplus" => c"r+",
                        "stream-wplus" => c"w+b",
                        "stream-aplus" => c"ab+",
                        _ => c"rb",
                    };
                    let stream = libc::fopen(path.as_ptr(), mode.as_ptr());
                    if !stream.is_null() {
                        if args[0] != "stream-read" {
                            assert!(libc::fputc(65, stream) >= 0);
                        }
                        assert_eq!(libc::fclose(stream), 0);
                    }
                } else {
                    let flags = libc::O_RDONLY
                        | match args[0].as_str() {
                            "open-create" => libc::O_CREAT,
                            "open-truncate" => libc::O_TRUNC,
                            _ => 0,
                        };
                    let fd = libc::open(path.as_ptr(), flags, 0o600 as libc::c_int);
                    if fd >= 0 {
                        assert_eq!(libc::close(fd), 0);
                    }
                }
            }
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
        #[cfg(unix)]
        "detach-setsid" | "detach-setpgid" | "detach-spawn" | "detach-raw-setsid"
        | "detach-raw-setpgid" => {
            use std::{io::Read, os::unix::process::CommandExt, process::Stdio};
            let mut command = Command::new(std::env::current_exe().unwrap());
            command
                .args(["detach-child", &args[0], &args[1]])
                .stdout(Stdio::piped());
            if args[0] == "detach-spawn" {
                command.process_group(0);
            }
            #[allow(
                clippy::zombie_processes,
                reason = "finite detached child intentionally outlives its parent to test lost \
                          lifecycle coverage"
            )]
            let mut child = command.spawn().unwrap();
            let mut ready = [0u8];
            child.stdout.take().unwrap().read_exact(&mut ready).unwrap();
            assert_eq!(ready, [1]);
        }
        #[cfg(unix)]
        "detach-child" => {
            use std::os::fd::AsRawFd;
            let output = fs::File::create(&args[2]).unwrap();
            // SAFETY: valid scalar process calls and live byte buffers. The
            // bounded child outlives its parent and writes through a held file.
            unsafe {
                match args[1].as_str() {
                    #[cfg(target_os = "macos")]
                    "detach-raw-setsid" => assert!(libc::syscall(147) > 0),
                    #[cfg(target_os = "macos")]
                    "detach-raw-setpgid" => assert_eq!(libc::syscall(82, 0, 0), 0),
                    "detach-setsid" => assert!(libc::setsid() > 0),
                    "detach-setpgid" => assert_eq!(libc::setpgid(0, 0), 0),
                    _ => assert_eq!(libc::getpgrp(), libc::getpid()),
                }
                assert_eq!(libc::write(1, [1u8].as_ptr().cast(), 1), 1);
                libc::close(0);
                libc::close(1);
                libc::close(2);
                libc::usleep(200_000);
                libc::write(output.as_raw_fd(), b"done".as_ptr().cast(), 4);
                libc::_exit(0);
            }
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
        #[cfg(unix)]
        "inherited-fd" => {
            let fd: i32 = args[1].parse().unwrap();
            // SAFETY: scalar inherited descriptor and live bounded input bytes.
            assert_eq!(
                unsafe { libc::write(fd, b"FD-CANARY".as_ptr().cast(), 9) },
                9
            );
        }
        "argv0" => println!("{}", std::env::args().next().unwrap()),
        "stdio" => {
            print!("STDOUT-CANARY");
            eprint!("STDERR-CANARY");
        }
        "stdin" => {
            let mut input = String::new();
            use std::io::Read;
            std::io::stdin().read_to_string(&mut input).unwrap();
            print!("{input}");
        }
        "metadata-marker" => {
            fs::write(&args[1], "command started").unwrap();
        }
        "prepare-output" => {
            assert!(!std::path::Path::new("out").exists());
            fs::create_dir("out").unwrap();
        }
        #[cfg(unix)]
        "windows-context" => {
            for name in ["SystemRoot", "WINDIR", "COMSPEC", "PATHEXT"] {
                let value = std::env::var(name).ok();
                assert_eq!(
                    value.as_deref(),
                    if args[1] == "selected" {
                        Some("OS-CONTEXT-CANARY")
                    } else {
                        None
                    }
                );
            }
            if args[2] == "prepare" {
                fs::create_dir("out").unwrap();
            } else {
                fs::write("out/result", "stable output").unwrap();
            }
        }
        "env" => {
            assert!(std::path::Path::new("out").is_dir());
            let path =
                std::path::Path::new(&std::env::var_os("HOME").unwrap()).join("previous-run");
            assert!(!path.exists());
            fs::write(&path, "local cache").unwrap();
            let cache =
                std::path::PathBuf::from(std::env::var_os("XDG_CACHE_HOME").unwrap()).join("entry");
            fs::write(&cache, "cache content").unwrap();
            assert_eq!(fs::read_to_string(&cache).unwrap(), "cache content");
            assert!(std::env::var_os("RUNLENS_AMBIENT_SECRET").is_none());
            fs::write("out/result.txt", "stable").unwrap();
        }
        _ => std::process::exit(2),
    }
}

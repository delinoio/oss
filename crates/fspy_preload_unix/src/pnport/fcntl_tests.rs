// SPDX-License-Identifier: Apache-2.0
//! Isolated native ABI and descriptor-state controls for the macOS adapter.

use std::{
    collections::HashMap,
    ffi::CString,
    fs,
    os::fd::{AsRawFd, FromRawFd, OwnedFd},
    process::{Command, Stdio},
    sync::atomic::Ordering,
    time::{Duration, Instant},
};

use super::{
    Cache, ForkMutex, Graph, Guard, INSIDE, RUNTIME, Runtime, TLS_READY, Translation, View,
    pnport_fcntl,
};

pub(super) fn check_native_forwarding(mut after_call: impl FnMut()) {
    // SAFETY: These native descriptors are owned until all fcntl calls finish.
    // Every pointer argument references a live writable PATH_MAX buffer.
    unsafe {
        let queue = libc::kqueue();
        assert!(queue >= 0);
        let queue = OwnedFd::from_raw_fd(queue);
        assert_eq!(
            pnport_fcntl(queue.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC),
            0
        );
        after_call();
        assert_eq!(
            pnport_fcntl(queue.as_raw_fd(), libc::F_GETFD),
            libc::FD_CLOEXEC
        );
        after_call();

        let fd = libc::open(c".".as_ptr(), libc::O_RDONLY);
        assert!(fd >= 0);
        let fd = OwnedFd::from_raw_fd(fd);
        let mut actual = [0u8; libc::PATH_MAX as usize];
        let mut expected = [0u8; libc::PATH_MAX as usize];
        assert_eq!(
            libc::fcntl(fd.as_raw_fd(), libc::F_GETPATH, expected.as_mut_ptr()),
            0
        );
        assert_eq!(
            pnport_fcntl(
                fd.as_raw_fd(),
                libc::F_GETPATH,
                actual.as_mut_ptr().cast::<libc::c_void>()
            ),
            0
        );
        assert_eq!(actual, expected);
        after_call();
        for (command, minimum) in [(libc::F_DUPFD, 64), (libc::F_DUPFD_CLOEXEC, 96)] {
            let duplicated = pnport_fcntl(fd.as_raw_fd(), command, minimum);
            assert!(duplicated >= minimum);
            let duplicated = OwnedFd::from_raw_fd(duplicated);
            after_call();
            if command == libc::F_DUPFD_CLOEXEC {
                assert_eq!(
                    pnport_fcntl(duplicated.as_raw_fd(), libc::F_GETFD),
                    libc::FD_CLOEXEC
                );
                after_call();
            }
        }
        assert_eq!(pnport_fcntl(-1, libc::F_GETFD), -1);
        assert_eq!(*libc::__error(), libc::EBADF);
        after_call();
        assert_eq!(pnport_fcntl(-1, libc::F_SETFD, libc::FD_CLOEXEC), -1);
        assert_eq!(*libc::__error(), libc::EBADF);
        after_call();
        actual.fill(0x5a);
        assert_eq!(
            pnport_fcntl(
                -1,
                libc::F_GETPATH,
                actual.as_mut_ptr().cast::<libc::c_void>()
            ),
            -1
        );
        assert_eq!(*libc::__error(), libc::EBADF);
        assert!(actual.iter().all(|byte| *byte == 0x5a));
        after_call();
    }
}

fn isolated(name: &str, scenario: fn()) {
    const SELECTOR: &str = "PNPORT_FCNTL_UNIT_SCENARIO";
    if std::env::var(SELECTOR).as_deref() == Ok(name) {
        scenario();
        return;
    }
    // Readiness and OnceLock runtime state belong to this child process only.
    // A bounded wait also turns accidental mutex reentry into a test failure.
    let mut child = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", name, "--nocapture"])
        .env(SELECTOR, name)
        .env_remove("PNPORT_SESSION")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(15);
    while child.try_wait().unwrap().is_none() {
        if Instant::now() >= deadline {
            child.kill().unwrap();
            child.wait().unwrap();
            panic!("isolated fcntl scenario timed out: {name}");
        }
        std::thread::sleep(Duration::from_millis(10));
    }
    let output = child.wait_with_output().unwrap();
    assert!(
        output.status.success()
            && String::from_utf8_lossy(&output.stdout).contains("1 passed; 0 failed"),
        "{name}: stdout={} stderr={}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
}

fn install_runtime() -> tempfile::TempDir {
    assert!(RUNTIME.get().is_none());
    let root = tempfile::tempdir().unwrap();
    let data = serde_json::json!({
        "enableTopLevelFallback": true, "ignorePatternData": null,
        "dependencyTreeRoots": [{"name":"root","reference":"workspace:."}],
        "fallbackPool": [], "fallbackExclusionList": [],
        "packageRegistryData": [
            [null, [[null, {"packageLocation":"./","packageDependencies":[],"linkType":"SOFT","discardFromLookup":true}]]],
            ["root", [["workspace:.", {"packageLocation":"./","packageDependencies":[],"linkType":"SOFT"}]]]
        ]
    });
    let manifest = root.path().join(".pnp.cjs");
    fs::write(
        &manifest,
        format!(
            "const RAW_RUNTIME_STATE =\n'{}';\n",
            serde_json::to_string(&data).unwrap()
        ),
    )
    .unwrap();
    let graph = Graph::load(&manifest).unwrap();
    let cache = Cache::open(root.path().join("cache")).unwrap();
    assert!(
        RUNTIME
            .set(
                ForkMutex::new(Runtime {
                    view: View::new(graph, cache, root.path().join("session")),
                    descriptors: HashMap::new(),
                    cwd: None,
                    directories: HashMap::new(),
                })
                .unwrap()
            )
            .is_ok()
    );
    root
}

fn descriptor_state(
    runtime: &Runtime,
) -> Vec<(i32, std::path::PathBuf, std::path::PathBuf, bool, bool)> {
    let mut state: Vec<_> = runtime
        .descriptors
        .iter()
        .map(|(fd, t)| {
            (
                *fd,
                t.logical.clone(),
                t.physical.clone(),
                t.readonly,
                t.virtual_link,
            )
        })
        .collect();
    state.sort();
    state
}

#[test]
fn rejected_admission_skips_locked_runtime() {
    isolated(
        "pnport::fcntl_tests::rejected_admission_skips_locked_runtime",
        || {
            let _root = install_runtime();
            let owner = Guard::enter().unwrap();
            let runtime = RUNTIME.get().unwrap().lock().unwrap();
            let before = descriptor_state(&runtime);
            check_native_forwarding(|| assert!(Guard::enter().is_none()));
            assert_eq!(descriptor_state(&runtime), before);
            drop(runtime);
            drop(owner);
            assert!(Guard::enter().is_some());
        },
    );
}

#[test]
fn tls_not_ready_forwards_without_runtime_or_guard_state() {
    isolated(
        "pnport::fcntl_tests::tls_not_ready_forwards_without_runtime_or_guard_state",
        || {
            let _root = install_runtime();
            let runtime = RUNTIME.get().unwrap().lock().unwrap();
            let before = descriptor_state(&runtime);
            TLS_READY.store(false, Ordering::Release);
            check_native_forwarding(|| {
                assert!(Guard::enter().is_none());
                INSIDE.with(|inside| assert!(!inside.get()));
            });
            assert_eq!(descriptor_state(&runtime), before);
            TLS_READY.store(true, Ordering::Release);
            drop(runtime);
            assert!(Guard::enter().is_some());
        },
    );
}

#[test]
fn admitted_duplication_preserves_state_on_failure() {
    isolated(
        "pnport::fcntl_tests::admitted_duplication_preserves_state_on_failure",
        || {
            let root = install_runtime();
            let path = CString::new(root.path().as_os_str().as_encoded_bytes()).unwrap();
            // SAFETY: The path is NUL-terminated, all live descriptors remain
            // owned, and the closed descriptor is used only for
            // native EBADF controls.
            unsafe {
                let fd = libc::open(path.as_ptr(), libc::O_RDONLY);
                assert!(fd >= 0);
                let fd = OwnedFd::from_raw_fd(fd);
                let closed = libc::dup(fd.as_raw_fd());
                assert!(closed >= 0);
                assert_eq!(libc::close(closed), 0);
                {
                    let _owner = Guard::enter().unwrap();
                    let mut runtime = RUNTIME.get().unwrap().lock().unwrap();
                    for key in [fd.as_raw_fd(), -1, closed] {
                        runtime.descriptors.insert(
                            key,
                            Translation {
                                logical: root.path().join(format!("peer-{key}")),
                                physical: root.path().to_path_buf(),
                                readonly: true,
                                virtual_link: false,
                                kind: super::PathKind::Dependency,
                            },
                        );
                    }
                }
                let before = descriptor_state(&RUNTIME.get().unwrap().lock().unwrap());
                for invalid in [-1, closed] {
                    for command in [libc::F_DUPFD, libc::F_DUPFD_CLOEXEC] {
                        assert_eq!(pnport_fcntl(invalid, command, 64), -1);
                        assert_eq!(*libc::__error(), libc::EBADF);
                        assert_eq!(
                            descriptor_state(&RUNTIME.get().unwrap().lock().unwrap()),
                            before
                        );
                    }
                }
                for command in [libc::F_DUPFD, libc::F_DUPFD_CLOEXEC] {
                    let duplicated = pnport_fcntl(fd.as_raw_fd(), command, 64);
                    assert!(duplicated >= 64);
                    let duplicated = OwnedFd::from_raw_fd(duplicated);
                    let _owner = Guard::enter().unwrap();
                    let runtime = RUNTIME.get().unwrap().lock().unwrap();
                    let source = runtime.descriptors.get(&fd.as_raw_fd()).unwrap();
                    let target = runtime.descriptors.get(&duplicated.as_raw_fd()).unwrap();
                    assert_eq!(source.logical, target.logical);
                    assert_eq!(source.physical, target.physical);
                    assert_eq!(source.readonly, target.readonly);
                    assert_eq!(source.virtual_link, target.virtual_link);
                }
            }
        },
    );
}

#[test]
fn transfer_extents_rejects_managed_secondary_descriptor() {
    isolated(
        "pnport::fcntl_tests::transfer_extents_rejects_managed_secondary_descriptor",
        || {
            let root = install_runtime();
            let primary_path = root.path().join("primary");
            let secondary_path = root.path().join("secondary");
            fs::write(&primary_path, b"primary").unwrap();
            fs::write(&secondary_path, b"secondary").unwrap();
            let primary_physical = fs::canonicalize(&primary_path).unwrap();
            let secondary_physical = fs::canonicalize(&secondary_path).unwrap();
            let primary = CString::new(primary_path.as_os_str().as_encoded_bytes()).unwrap();
            let secondary = CString::new(secondary_path.as_os_str().as_encoded_bytes()).unwrap();
            // SAFETY: Both descriptors remain owned until the native call and
            // their paths are NUL-terminated temporary fixture paths.
            unsafe {
                let primary = libc::open(primary.as_ptr(), libc::O_RDWR);
                assert!(primary >= 0);
                let primary = OwnedFd::from_raw_fd(primary);
                let secondary = libc::open(secondary.as_ptr(), libc::O_RDWR);
                assert!(secondary >= 0);
                let secondary = OwnedFd::from_raw_fd(secondary);
                {
                    let _owner = Guard::enter().unwrap();
                    let mut runtime = RUNTIME.get().unwrap().lock().unwrap();
                    runtime.descriptors.insert(
                        primary.as_raw_fd(),
                        Translation {
                            logical: primary_path,
                            physical: primary_physical,
                            readonly: false,
                            virtual_link: false,
                            kind: super::PathKind::Native,
                        },
                    );
                    runtime.descriptors.insert(
                        secondary.as_raw_fd(),
                        Translation {
                            logical: secondary_path,
                            physical: secondary_physical,
                            readonly: true,
                            virtual_link: false,
                            kind: super::PathKind::Dependency,
                        },
                    );
                }
                assert_eq!(
                    pnport_fcntl(
                        primary.as_raw_fd(),
                        libc::F_TRANSFEREXTENTS,
                        secondary.as_raw_fd()
                    ),
                    -1
                );
                assert_eq!(*libc::__error(), libc::EROFS);
                assert_eq!(
                    pnport_fcntl(-1, libc::F_TRANSFEREXTENTS, secondary.as_raw_fd()),
                    -1
                );
                assert_eq!(*libc::__error(), libc::EBADF);
                assert_eq!(
                    pnport_fcntl(primary.as_raw_fd(), libc::F_TRANSFEREXTENTS, -1),
                    -1
                );
                assert_eq!(*libc::__error(), libc::EINVAL);
            }
        },
    );
}

// SPDX-License-Identifier: Apache-2.0
use std::{fs, fs::File, io};

use super::*;
use crate::{context::CancelError, paths};

struct Fixture {
    _temp: tempfile::TempDir,
    src: String,
    dst: String,
}

fn fixture() -> Fixture {
    let temp = tempfile::tempdir().unwrap();
    let parent = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
    let src = paths::join2(&parent, "source 한글");
    let dst = paths::join2(&parent, "destination space");
    fs::create_dir_all(paths::join2(&src, "nested")).unwrap();
    for name in ["a", "nested/b", "empty"] {
        let data: &[u8] = if name == "empty" {
            b""
        } else {
            b"original cache data"
        };
        let path = paths::join2(&src, &paths::from_slash(name));
        fs::write(&path, data).unwrap();
        set_mode(&path, 0o640);
    }
    Fixture {
        _temp: temp,
        src,
        dst,
    }
}

#[cfg(unix)]
fn set_mode(path: &str, mode: u32) {
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(path, fs::Permissions::from_mode(mode)).unwrap();
}

#[cfg(not(unix))]
fn set_mode(path: &str, mode: u32) {
    let mut p = fs::metadata(path).unwrap().permissions();
    p.set_readonly(mode & 0o200 == 0);
    fs::set_permissions(path, p).unwrap();
}

fn unsupported_clone(_: &Context, _: &File, _: &str) -> Result<()> {
    Err(Unsupported.into())
}

fn absent(path: &str) {
    match fs::symlink_metadata(path) {
        Err(e) if e.kind() == io::ErrorKind::NotFound => {}
        other => panic!("expected absent {path}: {other:?}"),
    }
}

fn join(a: &str, b: &str) -> String {
    if b == "." {
        a.to_string()
    } else {
        paths::join2(a, &paths::from_slash(b))
    }
}

fn bg() -> Context {
    Context::background()
}

fn assert_isolation(src: &str, dst: &str) {
    fs::write(join(dst, "a"), b"changed").unwrap();
    assert_eq!(fs::read(join(src, "a")).unwrap(), b"original cache data");
    remove_all(dst);
    assert_eq!(
        fs::read(join(src, "nested/b")).unwrap(),
        b"original cache data"
    );
}

fn remove_all(path: &str) {
    paths::walk_dir(path, &mut |e| {
        if !e.file_type.is_symlink() {
            set_owner_access(&e.path, e.is_dir())?;
        }
        Ok(paths::Walk::Continue)
    })
    .unwrap();
    fs::remove_dir_all(path).unwrap();
}

fn modified(path: &str) -> std::time::SystemTime {
    fs::metadata(path).unwrap().modified().unwrap()
}

fn perm(path: &str) -> u32 {
    paths::go_file_mode(&fs::metadata(path).unwrap()) & 0o777
}

/// Opens a file or directory with permission to change its times. Windows
/// opens directories only with backup semantics.
fn open_for_times(path: &str) -> File {
    #[cfg(windows)]
    {
        use std::os::windows::fs::OpenOptionsExt;

        use windows_sys::Win32::Storage::FileSystem::{
            FILE_FLAG_BACKUP_SEMANTICS, FILE_WRITE_ATTRIBUTES,
        };
        File::options()
            .access_mode(FILE_WRITE_ATTRIBUTES)
            .custom_flags(FILE_FLAG_BACKUP_SEMANTICS)
            .open(path)
            .unwrap()
    }
    #[cfg(unix)]
    File::options()
        .write(!fs::metadata(path).unwrap().is_dir())
        .read(true)
        .open(path)
        .unwrap()
}

#[test]
fn copy_preserves_freshness_and_isolation() {
    let f = fixture();
    let stamp = std::time::UNIX_EPOCH + std::time::Duration::new(1_700_000_000, 123_456_700);
    for name in ["a", "nested/b", "nested", "."] {
        open_for_times(&join(&f.src, name))
            .set_times(fs::FileTimes::new().set_accessed(stamp).set_modified(stamp))
            .unwrap();
    }
    let result = tree_with(&bg(), &f.src, &f.dst, true, &mut unsupported_clone).unwrap();
    assert_eq!(
        result,
        CloneResult {
            cloned_files: 0,
            copied_files: 3,
            mode: Mode::Copy
        }
    );
    for name in ["a", "nested/b", "empty", "nested", "."] {
        let (o, d) = (join(&f.src, name), join(&f.dst, name));
        assert_ne!(
            paths::file_id(&o, true).unwrap(),
            paths::file_id(&d, true).unwrap(),
            "shared inode {name}"
        );
        assert_eq!(modified(&o), modified(&d), "mtime {name}");
        assert_eq!(perm(&o), perm(&d), "perm {name}");
    }
    assert_isolation(&f.src, &f.dst);
}

#[test]
fn fallback_requires_explicit_authorization() {
    let f = fixture();
    let err = tree_with(&bg(), &f.src, &f.dst, false, &mut unsupported_clone).unwrap_err();
    assert!(crate::error::find::<Unsupported>(&err).is_some(), "{err}");
    absent(&f.dst);
}

#[test]
fn operational_failures_never_copy() {
    for failure in [
        io::ErrorKind::PermissionDenied,
        io::ErrorKind::StorageFull,
        io::ErrorKind::Other,
    ] {
        let f = fixture();
        let mut calls = 0;
        let err = tree_with(&bg(), &f.src, &f.dst, true, &mut |ctx, file, path| {
            calls += 1;
            if calls == 1 {
                return copy_file(ctx, file, path);
            }
            fs::write(path, b"partial").unwrap();
            Err(io::Error::new(failure, "injected failure").into())
        })
        .unwrap_err();
        assert!(crate::error::is_io_kind(&err, failure), "{err}");
        assert_eq!(calls, 2);
        absent(&f.dst);
        assert_eq!(fs::read(join(&f.src, "a")).unwrap(), b"original cache data");
    }
}

#[test]
fn partial_native_fallback_starts_fresh() {
    let f = fixture();
    let mut calls = 0;
    let result = tree_with(&bg(), &f.src, &f.dst, true, &mut |ctx, file, path| {
        calls += 1;
        if calls == 1 {
            return copy_file(ctx, file, path);
        }
        fs::write(path, b"junk".repeat(1000))?;
        let mut reader = file;
        reader.seek(SeekFrom::Start(3))?;
        Err(Unsupported.into())
    })
    .unwrap();
    assert_eq!(
        result,
        CloneResult {
            cloned_files: 1,
            copied_files: 2,
            mode: Mode::Mixed
        }
    );
    for name in ["a", "empty", "nested/b"] {
        assert_eq!(
            fs::read(join(&f.src, name)).unwrap(),
            fs::read(join(&f.dst, name)).unwrap(),
            "{name}"
        );
    }
}

#[test]
fn cancellation_cleans_stage() {
    for before in [true, false] {
        let f = fixture();
        let (ctx, cancel) = bg().with_cancel();
        if before {
            cancel.cancel();
        }
        let err = tree_with(&ctx, &f.src, &f.dst, true, &mut |ctx, file, path| {
            let copied = copy_file(ctx, file, path);
            cancel.cancel();
            copied
        })
        .unwrap_err();
        assert!(crate::error::is(&err, &CancelError::Canceled), "{err}");
        absent(&f.dst);
    }
}

#[test]
fn copy_checks_cancellation_between_reads() {
    let (ctx, cancel) = bg().with_cancel();
    let mut reader = ContextReader {
        ctx: &ctx,
        reader: &b"contents"[..],
    };
    cancel.cancel();
    let err = reader.read(&mut [0_u8; 1]).unwrap_err();
    assert!(matches!(
        cancel_or_io(err).downcast_ref::<CancelError>(),
        Some(CancelError::Canceled)
    ));
}

#[test]
fn existing_destination_and_overlap() {
    let f = fixture();
    fs::create_dir(&f.dst).unwrap();
    let sentinel = join(&f.dst, "keep");
    fs::write(&sentinel, b"keep").unwrap();
    for target in [f.dst.clone(), f.src.clone(), join(&f.src, "inside")] {
        assert!(
            tree_with(&bg(), &f.src, &target, true, &mut unsupported_clone).is_err(),
            "accepted {target}"
        );
    }
    assert_eq!(fs::read(&sentinel).unwrap(), b"keep");
    absent(&join(&f.src, "inside"));
}

#[cfg(unix)]
#[test]
fn reject_symlinks() {
    let f = fixture();
    std::os::unix::fs::symlink("a", join(&f.src, "link")).unwrap();
    let err = tree_with(&bg(), &f.src, &f.dst, true, &mut unsupported_clone).unwrap_err();
    assert!(err.to_string().contains("link or special file"), "{err}");
    absent(&f.dst);
}

#[test]
fn source_mutation_fails() {
    let f = fixture();
    let (src, dst) = (f.src.clone(), f.dst.clone());
    let result = tree_with(&bg(), &f.src, &f.dst, false, &mut |ctx, file, path| {
        copy_file(ctx, file, path)?;
        let relative = paths::rel(&dst, path).unwrap();
        fs::write(
            paths::join2(&src, &relative),
            b"changed by unmanaged writer",
        )?;
        Ok(())
    });
    assert!(result.is_err(), "accepted changed source");
    absent(&f.dst);
}

#[test]
fn native_tree() {
    let f = fixture();
    // A non-aligned, multi-cluster file exercises ReFS EOF handling.
    let payload = b"native clone fixture".repeat(10001);
    fs::write(join(&f.src, "large"), &payload).unwrap();
    let result = match tree(&bg(), &f.src, &f.dst, false) {
        Err(e)
            if crate::error::find::<Unsupported>(&e).is_some()
                && std::env::var("CCOW_TEST_NATIVE").as_deref() != Ok("1") =>
        {
            eprintln!("native supported filesystem unavailable: {e}");
            return;
        }
        other => other.unwrap(),
    };
    assert_eq!(
        result,
        CloneResult {
            cloned_files: 4,
            copied_files: 0,
            mode: Mode::Clone
        }
    );
    assert_eq!(fs::read(join(&f.dst, "large")).unwrap(), payload);
    let (a, b) = (join(&f.src, "large"), join(&f.dst, "large"));
    assert_ne!(
        paths::file_id(&a, true).unwrap(),
        paths::file_id(&b, true).unwrap(),
        "hard link"
    );
    assert_eq!(modified(&a), modified(&b));
    assert_eq!(perm(&a), perm(&b));
    assert_isolation(&f.src, &f.dst);
}

#[cfg(unix)]
#[test]
fn ancestor_alias_cannot_recurse() {
    let f = fixture();
    let alias = paths::join2(&paths::dir(&f.src), "alias");
    std::os::unix::fs::symlink(&f.src, &alias).unwrap();
    let dst = paths::join2(&alias, "inside");
    assert!(tree_with(&bg(), &f.src, &dst, true, &mut unsupported_clone).is_err());
    absent(&dst);
    absent(&join(&f.src, "inside"));
}

#[test]
fn empty_tree() {
    let temp = tempfile::tempdir().unwrap();
    let parent = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
    let (src, dst) = (paths::join2(&parent, "src"), paths::join2(&parent, "dst"));
    fs::create_dir(&src).unwrap();
    let result = tree_with(&bg(), &src, &dst, false, &mut |_, _, _| {
        panic!("cloned directory as file")
    })
    .unwrap();
    assert_eq!(result, CloneResult::default());
}

#[test]
fn source_hardlinks_become_independent_files() {
    let f = fixture();
    fs::hard_link(join(&f.src, "a"), join(&f.src, "alias")).unwrap();
    tree_with(&bg(), &f.src, &f.dst, true, &mut unsupported_clone).unwrap();
    assert_ne!(
        paths::file_id(&join(&f.dst, "a"), true).unwrap(),
        paths::file_id(&join(&f.dst, "alias"), true).unwrap()
    );
    assert_isolation(&f.src, &f.dst);
}

#[cfg(unix)]
#[test]
fn reject_fifo_without_blocking() {
    let f = fixture();
    let fifo = std::ffi::CString::new(join(&f.src, "pipe")).unwrap();
    // SAFETY: `fifo` is NUL-terminated.
    assert_eq!(unsafe { libc::mkfifo(fifo.as_ptr(), 0o600) }, 0);
    assert!(tree_with(&bg(), &f.src, &f.dst, true, &mut unsupported_clone).is_err());
    absent(&f.dst);
}

#[cfg(unix)]
#[test]
fn read_only_directory_cleanup() {
    let f = fixture();
    let readonly = join(&f.src, "0-readonly");
    fs::create_dir(&readonly).unwrap();
    let item = join(&readonly, "item");
    fs::write(&item, b"").unwrap();
    set_mode(&item, 0o400);
    set_mode(&readonly, 0o500);
    let err = tree_with(&bg(), &f.src, &f.dst, true, &mut |ctx, file, path| {
        if paths::base(path) == "a" {
            return Err(io::Error::from_raw_os_error(libc::ENOSPC).into());
        }
        copy_file(ctx, file, path)
    });
    set_mode(&readonly, 0o700);
    let err = err.unwrap_err();
    assert!(
        crate::error::find::<io::Error>(&err).and_then(io::Error::raw_os_error)
            == Some(libc::ENOSPC),
        "{err}"
    );
    absent(&f.dst);
}

#[cfg(unix)]
#[test]
fn capability_classification() {
    for code in [
        libc::ENOSPC,
        libc::EACCES,
        libc::EPERM,
        libc::EIO,
        libc::EINVAL,
        libc::EBADF,
    ] {
        let got = native::capability_error(io::Error::from_raw_os_error(code));
        assert!(
            crate::error::find::<Unsupported>(&got).is_none(),
            "misclassified {code}"
        );
        assert_eq!(
            crate::error::find::<io::Error>(&got).and_then(io::Error::raw_os_error),
            Some(code)
        );
    }
    #[cfg(target_os = "linux")]
    let supported = [libc::EXDEV, libc::EOPNOTSUPP, libc::ENOTTY, libc::ENOSYS];
    #[cfg(not(target_os = "linux"))]
    let supported = [libc::EXDEV, libc::ENOTSUP, libc::ENOSYS];
    for code in supported {
        let got = native::capability_error(io::Error::from_raw_os_error(code));
        assert!(
            crate::error::find::<Unsupported>(&got).is_some(),
            "missing {code}"
        );
        assert_eq!(
            crate::error::find::<io::Error>(&got).and_then(io::Error::raw_os_error),
            Some(code)
        );
    }
}

#[cfg(windows)]
#[test]
fn windows_capability_classification() {
    use windows_sys::Win32::Foundation::{
        ERROR_ACCESS_DENIED, ERROR_CRC, ERROR_DISK_FULL, ERROR_INVALID_FUNCTION,
        ERROR_INVALID_PARAMETER, ERROR_NOT_SAME_DEVICE, ERROR_NOT_SUPPORTED,
    };
    for code in [
        ERROR_DISK_FULL,
        ERROR_ACCESS_DENIED,
        ERROR_CRC,
        ERROR_INVALID_PARAMETER,
    ] {
        let got = native::capability_error(io::Error::from_raw_os_error(code as i32));
        assert!(
            crate::error::find::<Unsupported>(&got).is_none(),
            "misclassified {code}"
        );
    }
    for code in [
        ERROR_NOT_SAME_DEVICE,
        ERROR_NOT_SUPPORTED,
        ERROR_INVALID_FUNCTION,
    ] {
        let got = native::capability_error(io::Error::from_raw_os_error(code as i32));
        assert!(
            crate::error::find::<Unsupported>(&got).is_some(),
            "missing {code}"
        );
    }
}

#[cfg(all(windows, target_pointer_width = "64"))]
#[test]
fn windows_ioctl_layout() {
    use std::mem::{offset_of, size_of};
    assert_eq!(size_of::<native::DuplicateExtents>(), 32);
    assert_eq!(offset_of!(native::DuplicateExtents, source_offset), 8);
    assert_eq!(offset_of!(native::DuplicateExtents, byte_count), 24);
    assert_eq!(size_of::<native::IntegrityInformation>(), 16);
}

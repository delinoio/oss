//! Shared access classification for libc interception and Linux seccomp.
use fspy_shared::ipc::AccessMode;
use nix::libc;

pub fn open_flags(flags: libc::c_int) -> AccessMode {
    let mut mode = match flags & libc::O_ACCMODE {
        libc::O_RDWR => AccessMode::READ | AccessMode::WRITE,
        libc::O_WRONLY => AccessMode::WRITE,
        _ => AccessMode::READ,
    };
    // Creation/truncation mutate directory entries or content independently of
    // descriptor access permissions, including failed attempts. See PATCHES.md.
    let mutation = flags & (libc::O_CREAT | libc::O_TRUNC) != 0;
    #[cfg(target_os = "linux")]
    let mutation = mutation || flags & libc::O_TMPFILE == libc::O_TMPFILE;
    if mutation { mode |= AccessMode::WRITE; }
    if flags & libc::O_NOFOLLOW != 0 && mode.contains(AccessMode::READ) {
        mode.remove(AccessMode::READ);
        mode |= AccessMode::READ_NOFOLLOW;
    }
    mode
}

pub fn stream_mode(mode: &[u8]) -> AccessMode {
    let update = mode.contains(&b'+');
    let read = update || mode.contains(&b'r');
    let write = update || mode.contains(&b'w') || mode.contains(&b'a');
    match (read, write) {
        (false, true) => AccessMode::WRITE,
        (true, true) => AccessMode::READ | AccessMode::WRITE,
        _ => AccessMode::READ,
    }
}

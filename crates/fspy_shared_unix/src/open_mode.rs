use fspy_shared::ipc::AccessMode;
use nix::libc;

/// Classify both data access and filesystem mutations performed by open itself.
#[must_use]
pub fn from_flags(flags: libc::c_int) -> AccessMode {
    let mut mode = match flags & libc::O_ACCMODE {
        libc::O_RDWR => AccessMode::READ | AccessMode::WRITE,
        libc::O_WRONLY => AccessMode::WRITE,
        _ => AccessMode::READ,
    };
    if flags & (libc::O_CREAT | libc::O_TRUNC) != 0 {
        mode |= AccessMode::WRITE;
    }
    #[cfg(target_os = "linux")]
    if flags & libc::O_TMPFILE == libc::O_TMPFILE {
        mode |= AccessMode::WRITE;
    }
    mode
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn mutating_read_only_opens_are_writes() {
        for flags in [
            libc::O_RDONLY | libc::O_CREAT,
            libc::O_RDONLY | libc::O_TRUNC,
        ] {
            assert_eq!(from_flags(flags), AccessMode::READ | AccessMode::WRITE);
        }
        #[cfg(target_os = "linux")]
        assert_eq!(
            from_flags(libc::O_RDONLY | libc::O_TMPFILE),
            AccessMode::READ | AccessMode::WRITE
        );
        assert_eq!(from_flags(libc::O_RDONLY), AccessMode::READ);
    }
}

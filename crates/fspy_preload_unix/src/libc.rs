#[cfg(not(all(target_os = "macos", feature = "pnport")))]
pub use libc::*;

unsafe extern "C" {
    // On macOS x86_64, directory functions use $INODE64 symbol suffix for 64-bit
    // inode support. On arm64, 64-bit inodes are the only option so no suffix
    // is needed. https://github.com/apple-open-source-mirror/Libc/blob/5e566be7a7047360adfb35ffc44c6a019a854bea/include/dirent.h#L198
    #[cfg_attr(
        all(target_os = "macos", target_arch = "x86_64"),
        link_name = "scandir$INODE64"
    )]
    #[cfg(not(all(target_os = "macos", feature = "pnport")))]
    pub unsafe fn scandir(
        dirname: *const c_char,
        namelist: *mut c_void,
        select: *const c_void,
        compar: *const c_void,
    ) -> c_int;

    #[cfg(all(target_os = "macos", not(feature = "pnport")))]
    #[cfg_attr(target_arch = "x86_64", link_name = "scandir_b$INODE64")]
    pub unsafe fn scandir_b(
        dirname: *const c_char,
        namelist: *mut c_void,
        select: *const c_void,
        compar: *const c_void,
    ) -> c_int;

    #[cfg(not(all(target_os = "macos", feature = "pnport")))]
    pub unsafe fn getdirentries(
        fd: c_int,
        buf: *mut c_char,
        nbytes: c_int,
        basep: *mut c_long,
    ) -> c_int;

    #[cfg(all(target_os = "macos", not(feature = "pnport")))]
    #[link_name = "open$NOCANCEL"]
    pub unsafe fn open_nocancel(path: *const c_char, flags: c_int, ...) -> c_int;

    #[cfg(all(target_os = "macos", not(feature = "pnport")))]
    #[link_name = "openat$NOCANCEL"]
    pub unsafe fn openat_nocancel(dirfd: c_int, path: *const c_char, flags: c_int, ...) -> c_int;

    #[cfg(all(target_os = "macos", not(feature = "pnport")))]
    pub unsafe fn __getdirentries64(
        fd: c_int,
        buf: *mut u8,
        buf_len: usize,
        basep: *mut i64,
    ) -> isize;
}

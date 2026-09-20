//! Identify the actual mapped main image, never a reopened executable pathname.
// macOS sys/proc_info.h PROC_PIDREGIONPATHINFO ABI: fixed 96-byte region info
// followed by libc's vnode_info_path. Query only our own live Mach-O header.
#[repr(C)]
struct Region {
    attributes: [u32; 4],
    offset: u64,
    counters: [u32; 14],
    address: u64,
    size: u64,
}
#[repr(C)]
struct RegionWithPath {
    region: Region,
    vnode: libc::vnode_info_path,
}
#[allow(deprecated, reason = "Pinned libc exposes the stable dyld ABI without a new dependency")]
pub fn current() -> Option<(u64, u64)> {
    // SAFETY: dyld owns loaded image headers for the process lifetime; proc_pidinfo writes
    // into a correctly sized, aligned local ABI structure. No path is opened.
    unsafe {
        // DYLD_INSERT_LIBRARIES may precede the main image in dyld's list.
        // Loaded native headers are readable and owned by dyld during startup.
        let address = (0..libc::_dyld_image_count().min(65536)).find_map(|index| {
            let header = libc::_dyld_get_image_header(index);
            (!header.is_null() && (*header).filetype == 2).then_some(header as u64)
        })?;
        let mut info = std::mem::MaybeUninit::<RegionWithPath>::zeroed();
        let size = std::mem::size_of::<RegionWithPath>() as i32;
        if address == 0 || libc::proc_pidinfo(libc::getpid(), 8, address,
            info.as_mut_ptr().cast(), size) != size { return None; }
        let info = info.assume_init();
        let end = info.region.address.checked_add(info.region.size)?;
        let stat = info.vnode.vip_vi.vi_stat;
        (address >= info.region.address && address < end && stat.vst_ino != 0)
            .then_some((u64::from(stat.vst_dev), stat.vst_ino))
    }
}

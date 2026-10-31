#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Native physical data extents, not st_size or st_blocks. No file contents emitted.

Reject compressed/inline/unknown mappings and multiple devices. Metadata is not
included. Call only on quiescent disposable caches; FIEMAP_FLAG_SYNC flushes data.
"""
import json
import os
import stat
import struct
import sys

if sys.platform != "win32":
    import fcntl

FIEMAP = 0xC020660B
COUNT = 256
HEADER = 32
EXTENT = 56


def linux_extents(file):
    result = []
    start = 0
    with open(file, "rb") as handle:
        while True:
            buffer = bytearray(HEADER + COUNT * EXTENT)
            struct.pack_into("=QQIIII", buffer, 0, start, (1 << 64) - 1 - start, 1, 0, COUNT, 0)
            fcntl.ioctl(handle.fileno(), FIEMAP, buffer, True)
            mapped = struct.unpack_from("=I", buffer, 20)[0]
            if mapped == 0:
                break
            for index in range(mapped):
                logical, physical, length, _, _, flags, _, _, _ = struct.unpack_from(
                    "=QQQQQIIII", buffer, HEADER + index * EXTENT
                )
                # LAST, UNWRITTEN and SHARED are the only supported flags.
                if flags & ~(0x1 | 0x800 | 0x2000) or not physical or not length:
                    raise ValueError("unsupported extent mapping")
                result.append([physical, physical + length, bool(flags & 0x2000)])
                start = logical + length
            if flags & 1:
                break
    return result


def darwin_extents(file):
    # Darwin sys/fcntl.h packs log2phys to four-byte alignment. APFS may deny
    # physical mapping to an unprivileged caller: fail closed in that case.
    result = []
    block = os.statvfs(file).f_bsize
    with open(file, "rb") as handle:
        os.fsync(handle.fileno())
        size = os.fstat(handle.fileno()).st_size
        limit = ((size + block - 1) // block) * block
        offset = 0
        while offset < limit:
            request = struct.pack("=Iqq", 0, limit - offset, offset)
            flags, length, physical = struct.unpack("=Iqq", fcntl.fcntl(handle, 65, request))
            if flags or physical < 0 or length <= 0:
                raise ValueError("unsupported APFS physical mapping")
            length = min(length, limit - offset)
            if physical % block or length % block:
                raise ValueError("unaligned APFS physical mapping")
            result.append([physical, physical + length, True])
            offset += length
    return result


def windows_extents(file):
    # FSCTL_GET_RETRIEVAL_POINTERS maps file VCNs to volume LCNs. Querying a
    # mapping can require additional volume privileges. Never use allocation
    # size or DUPLICATE_EXTENTS success as a substitute for these mappings.
    import ctypes
    from ctypes import wintypes
    kernel = ctypes.WinDLL("kernel32", use_last_error=True)
    kernel.CreateFileW.restype = wintypes.HANDLE
    kernel.CreateFileW.argtypes = [wintypes.LPCWSTR, wintypes.DWORD, wintypes.DWORD,
                                   ctypes.c_void_p, wintypes.DWORD, wintypes.DWORD, wintypes.HANDLE]
    kernel.DeviceIoControl.argtypes = [wintypes.HANDLE, wintypes.DWORD, ctypes.c_void_p,
                                      wintypes.DWORD, ctypes.c_void_p, wintypes.DWORD,
                                      ctypes.POINTER(wintypes.DWORD), ctypes.c_void_p]
    kernel.CloseHandle.argtypes = [wintypes.HANDLE]
    kernel.GetVolumePathNameW.argtypes = [wintypes.LPCWSTR, wintypes.LPWSTR, wintypes.DWORD]
    kernel.GetDiskFreeSpaceW.argtypes = [wintypes.LPCWSTR] + [ctypes.POINTER(wintypes.DWORD)] * 4
    volume = ctypes.create_unicode_buffer(32768)
    if not kernel.GetVolumePathNameW(str(file), volume, len(volume)):
        raise ctypes.WinError(ctypes.get_last_error())
    sectors, sector_bytes, free, total = (wintypes.DWORD() for _ in range(4))
    if not kernel.GetDiskFreeSpaceW(volume.value, ctypes.byref(sectors), ctypes.byref(sector_bytes),
                                    ctypes.byref(free), ctypes.byref(total)):
        raise ctypes.WinError(ctypes.get_last_error())
    cluster = sectors.value * sector_bytes.value
    if not cluster:
        raise ValueError("unknown cluster size")
    handle = kernel.CreateFileW(str(file), 0x80000000, 7, None, 3, 0, None)
    if handle == wintypes.HANDLE(-1).value:
        raise ctypes.WinError(ctypes.get_last_error())
    result = []
    start = 0
    try:
        while True:
            request = ctypes.c_longlong(start)
            output = ctypes.create_string_buffer(16 + COUNT * 16)
            returned = wintypes.DWORD()
            ok = kernel.DeviceIoControl(handle, 0x90073, ctypes.byref(request), 8,
                                        output, len(output), ctypes.byref(returned), None)
            error = ctypes.get_last_error() if not ok else 0
            if error == 38:  # ERROR_HANDLE_EOF: empty or wholly sparse file.
                break
            if error not in (0, 234):  # ERROR_MORE_DATA permits pagination.
                raise ctypes.WinError(error)
            count = struct.unpack_from("=I", output.raw)[0]
            if not count or count > COUNT or returned.value < 16 + count * 16:
                raise ValueError("invalid retrieval pointers")
            current = struct.unpack_from("=q", output.raw, 8)[0]
            for index in range(count):
                next_vcn, lcn = struct.unpack_from("=qq", output.raw, 16 + index * 16)
                if next_vcn <= current or current < start:
                    raise ValueError("invalid retrieval range")
                if lcn >= 0:
                    result.append([lcn * cluster, (lcn + next_vcn - current) * cluster, True])
                elif lcn != -1:
                    raise ValueError("unsupported retrieval mapping")
                current = next_vcn
            start = current
            if ok:
                break
    finally:
        kernel.CloseHandle(handle)
    return result


def extents(file):
    if sys.platform == "darwin":
        return darwin_extents(file)
    if sys.platform == "win32":
        return windows_extents(file)
    if sys.platform == "linux":
        return linux_extents(file)
    raise ValueError("unsupported native mapping platform")


def measure(roots):
    devices = set()
    files = []
    for group, root in enumerate(roots):
        root_info = os.lstat(root)
        if not stat.S_ISDIR(root_info.st_mode):
            raise ValueError("cache root is not a directory")
        for directory, dirs, names in os.walk(root, followlinks=False):
            for name in dirs + names:
                file = os.path.join(directory, name)
                info = os.lstat(file)
                if stat.S_ISDIR(info.st_mode):
                    continue
                if not stat.S_ISREG(info.st_mode):
                    raise ValueError("non-regular cache entry")
                devices.add(info.st_dev)
                files.append((group, info.st_ino, extents(file)))
    if len(devices) != 1:
        raise ValueError("expected nonempty single-device caches")
    intervals = sorted((a, b) for _, _, items in files for a, b, _ in items)
    total = 0
    end = 0
    for a, b in intervals:
        total += max(0, b - max(end, a))
        end = max(end, b)
    # Matching physical ranges in distinct inodes AND kernel SHARED flags prove
    # sharing. Equal bytes or a successful clone syscall alone do not.
    shared = False
    source = [(inode, a, b) for group, inode, items in files if group == 0
              for a, b, flag in items if flag]
    for group, inode, items in files:
        if group == 0:
            continue
        for a, b, flag in items:
            if flag and any(inode != other and max(a, x) < min(b, y)
                            for other, x, y in source):
                shared = True
    return {"physicalBytes": total, "shared": shared, "method": {"linux": "linux-fiemap-data-extents", "darwin": "darwin-log2phys-data-extents", "win32": "windows-retrieval-pointers-data-extents"}[sys.platform]}


if __name__ == "__main__":
    try:
        print(json.dumps(measure(sys.argv[1:])))
    except Exception:
        # Do not put private filenames or native diagnostics in CI output.
        print("native physical mapping unavailable", file=sys.stderr)
        sys.exit(1)

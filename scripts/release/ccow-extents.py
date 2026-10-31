#!/usr/bin/env python3
# SPDX-License-Identifier: Apache-2.0
"""Linux FIEMAP data extents, not st_size or st_blocks. No file contents emitted.

Reject compressed/inline/unknown mappings and multiple devices. Metadata is not
included. Call only on quiescent disposable caches; FIEMAP_FLAG_SYNC flushes data.
"""
import fcntl
import json
import os
import stat
import struct
import sys

FIEMAP = 0xC020660B
COUNT = 256
HEADER = 32
EXTENT = 56


def extents(file):
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


def measure(roots):
    devices = set()
    files = []
    for group, root in enumerate(roots):
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
    return {"physicalBytes": total, "shared": shared, "method": "linux-fiemap-data-extents"}


if __name__ == "__main__":
    print(json.dumps(measure(sys.argv[1:])))

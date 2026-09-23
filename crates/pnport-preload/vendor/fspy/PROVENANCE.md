# fspy provenance

Source: https://github.com/voidzero-dev/vite-task
Revision: 3aac49e31fba6905bb0b3d0e29d7755493241e9c
Original path: crates/fspy_preload_unix/src/macros/macos.rs
Additional Linux reference: crates/fspy_seccomp_unotify/src/target.rs, retained as
linux-target.rs from the same immutable revision.
License: MIT, retained verbatim in LICENSE.

macos.rs is an unmodified reference copy. The Mach-O interpose entry mechanism
in ../../src/unix.rs is adapted from it. The pnport adaptation uses named original
function pointers and a recursion guard because translation performs native I/O.
Upstream's filesystem event recorder and macOS executable replacement are not
used. pnport owns graph translation, immutable backing, injection failure policy,
and all local modifications. linux-target.rs is an unmodified reference for
setting no-new-privs and installing a Linux seccomp filter. The Linux runtime in
crates/pnport/src/linux.rs adapts that filter-installation mechanism and a
selected filesystem syscall set. It uses SECCOMP_RET_TRACE with PTRACE_TRACEME
on pnport-owned children, because pathname virtualization must replace syscall
arguments before the kernel opens a path; fspy's USER_NOTIF listener and IPC
protocol are not copied. The filter, syscall arguments, descriptor state,
process lifecycle, PnP translation, and diagnostics are pnport-owned code.
Windows Detours integration remains separate. Retained references alone are
not evidence of platform support.

To update: inspect an immutable upstream revision, compare this source file and
its MIT notice, port only required mechanisms on the repository's pinned Rust
nightly, record the new revision/local changes here, and rerun native conformance.
Never enable upstream system-executable substitution or update the toolchain as
an incidental vendor change.

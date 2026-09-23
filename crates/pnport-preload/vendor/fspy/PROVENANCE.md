# fspy provenance

Source: https://github.com/voidzero-dev/vite-task
Revision: 3aac49e31fba6905bb0b3d0e29d7755493241e9c
Original path: crates/fspy_preload_unix/src/macros/macos.rs
License: MIT, retained verbatim in LICENSE.

macos.rs is an unmodified reference copy. The Mach-O interpose entry mechanism
in ../../src/unix.rs is adapted from it. The pnport adaptation uses named original
function pointers and a recursion guard because translation performs native I/O.
Upstream's filesystem event recorder and macOS executable replacement are not
used. pnport owns graph translation, immutable backing, injection failure policy,
and all local modifications. Linux syscall and Windows Detours integration must
be audited separately; the retained macOS primitive is not evidence of support.

To update: inspect an immutable upstream revision, compare this source file and
its MIT notice, port only required mechanisms on the repository's pinned Rust
nightly, record the new revision/local changes here, and rerun native conformance.
Never enable upstream system-executable substitution or update the toolchain as
an incidental vendor change.

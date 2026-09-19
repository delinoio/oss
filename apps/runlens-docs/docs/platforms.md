# Platforms and tracing prerequisites

The release target matrix is macOS 13+, Windows 10 22H2+, and Ubuntu 22.04+, each on x64 and arm64. Release approval requires actual execution and installation evidence for every advertised target; source code and cross-compilation alone do not establish support. Consult the [release evidence](/releases) before treating a target as certified.

```sh
runlens doctor
runlens doctor --json
```

## macOS

Use a native architecture executable that permits library injection. SIP-protected system executables and interpreters cannot be tracked without changing the requested program. Runlens rejects known protected targets before launch and never substitutes another shell or core utility. Explicitly select an installed injectable tool where appropriate. Rosetta and mixed-architecture execution are excluded. Hardened or otherwise protected programs may prevent collection; incomplete observations cannot pass verification.

## Windows

Use a native x64 or arm64 PE executable. Scripts require an explicit interpreter. DLL injection and private Job Object ownership must be available; the job is assigned before the target resumes. Protected processes and mixed-architecture children are unsupported. Symlink source copying requires the applicable Windows capability.

## Ubuntu and Linux artifacts

Linux artifacts target glibc hosts. Alpine/musl hosts are excluded. A statically linked child executable on a supported glibc host is not automatically excluded: the backend uses seccomp user notifications and same-user process inspection for supported native static binaries. The kernel and calling environment must permit these operations. No privileged tracing deployment is included.

## Observation limits

Runlens supports finite noninteractive commands and supported child processes. Detached/background services, PTYs, interactive prompts, and persistent supervision are excluded. Directory membership, symlinks, missing paths, permission failures, and unstable files remain distinct observations.

Complete collection means completion within documented backend and snapshot coverage. It does not prove observation of every possible dependency. Environment reads, networking, clocks, randomness, detailed process timelines, and event ordering are not captured.

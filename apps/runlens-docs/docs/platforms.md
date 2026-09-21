# Platforms and tracing prerequisites

The release target matrix is macOS 13+, Windows 10 22H2+, and Ubuntu 22.04+, each on x64 and arm64. Release approval requires actual execution and installation evidence for every advertised target; source code and cross-compilation alone do not establish support. Consult the [release evidence](/releases) before treating a target as certified.

```sh
runlens doctor
runlens doctor --json
```

## macOS

Use a native architecture executable that permits library injection. SIP-protected system executables and interpreters cannot be tracked without changing the requested program. Runlens rejects known protected targets before launch and never substitutes another shell or core utility. Explicitly select an installed injectable tool where appropriate. Rosetta and mixed-architecture execution are excluded. Passive executable inspection rejects restricted segments and restrictive code-signing flags, including hardened runtime and library validation, before launch. This is conservative even when an entitlement might allow injection. Other protection or runtime collection failures remain incomplete and cannot pass verification.

Loaded macOS libraries outside the active system shared cache produce incomplete collection. Their image paths are retained as read evidence, but earlier loader reads and library initializers cannot be fully observed. The requested program continues, including when a library changes its behavior; these receipts cannot certify a cache or policy pass.

## Windows

Use a native x64 or arm64 PE executable. Scripts require an explicit interpreter. DLL injection and private Job Object ownership must be available; the job is assigned before the target resumes. Protected processes and mixed-architecture children are unsupported. Symlink source copying requires the applicable Windows capability.

File creation and deletion count as write attempts even when no content is written. Windows rename and hard-link operations currently produce incomplete collection because destination tracking is limited. The command continues, but its report cannot establish a verification pass.

## Ubuntu and Linux artifacts

Linux artifacts target glibc hosts. Alpine/musl hosts are excluded. A statically linked child executable on a supported glibc host is not automatically excluded: the backend uses seccomp user notifications and same-user process inspection for supported native static binaries. The kernel and calling environment must permit these operations. No privileged tracing deployment is included.

Linux file-handle lookups count as read attempts, including failed lookups and sizing probes. Returned handles and mount identifiers are not stored.

## Observation limits

Runlens supports finite noninteractive commands and supported child processes. Detached/background services, PTYs, interactive prompts, and persistent supervision are excluded. Directory membership, symlinks, missing paths, permission failures, and unstable files remain distinct observations.

Directory creation and metadata changes are write attempts. When a command creates `build` before writing `build/out/result`, a write allowlist for `build/out/**` also needs an explicit `build` entry. A symlink's target text alone does not count as reading the target.

If an inspected executable changes while Runlens prepares its snapshot, Runlens refuses that launch with an incomplete diagnostic. An executable digest is retained only when it can be tied to the launched image and remains stable through completion. Missing or mismatched identity evidence makes collection incomplete. Retry after the executable is stable. These checks do not provide an OS security boundary.

Complete collection means completion within documented backend and snapshot coverage. It does not prove observation of every possible dependency. Environment reads, networking, clocks, randomness, detailed process timelines, and event ordering are not captured.

Direct shebang scripts run with incomplete identity evidence because a script checksum does not identify its interpreter chain. Use an explicit supported native interpreter in argv when comparison requires executable identity.

Redirecting standard input, output, or error through a pre-opened regular file keeps the requested I/O working but makes collection incomplete. Runlens cannot certify accesses through those inherited file handles. Pipes and terminals remain supported, and stream contents are never saved in a report.

On Unix, commands that change process sessions/groups or request a separate group when spawning produce incomplete collection. Detached descendants can outlive observation and cannot be certified or reliably cleaned up by group-based ownership. Use finite children that stay in the command's process group for lifecycle verification.

Windows children created directly through native process APIs that bypass normal CreateProcess tracing produce incomplete collection. The child continues inside inherited Job ownership, but its filesystem accesses cannot establish a verification pass.

On Unix, additional inherited file descriptors such as shell descriptor 3 also make collection incomplete. Runlens preserves these caller-owned resources, but access through them cannot certify a policy pass.

On Windows, the temporary collector path must be representable in the active system code page. Runlens tries an available Windows short-path alias for Unicode locations; if neither spelling is representable, it fails before starting the command. Select an accessible temporary directory with a representable path (for example, an ASCII-only `TEMP`/`TMP` path) and retry. UTF-8 system code pages support Unicode paths directly.

Linux commands using io_uring continue to run, but their reports are incomplete and cannot pass verification. This includes setup, SQPOLL, submission, and registration attempts: asynchronous ring operations are outside the current collection coverage.

An access below a directory that was created or removed during the command has uncertain workspace scope. Before/after observations cannot rule out a temporary symlink to an external location. Such evidence cannot pass policy or cache verification, even when the output change is known. Create required output directories in an explicit preparation step when you need established directory ancestry for the target run.

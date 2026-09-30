# Public Grok automatic Read security assessment, 2026-09-30

Issue: [#1091](https://github.com/delinoio/oss/issues/1091). PR:
[#1230](https://github.com/delinoio/oss/pull/1230). Inspected head:
`586a5949dfe98036237def05f9d5755faafc7537`.

This maintenance pass assessed the unresolved
[Codex security finding](https://github.com/delinoio/oss/pull/1230#discussion_r4144405319)
(`PRRT_kwDORRAKg86nhbPq`). It did not execute an installed native Grok test,
authenticate a provider account, add confinement, change execution policy or
resolve the thread. The observations below distinguish current source inspection
from retained native evidence.

## Findings

- `internal/worker/grok_execution.go` constructs the original Grok process using
  an explicit private environment, runtime home and owned workspace lease, then
  calls the new adaptive `RunFirstInput` controller. Those ownership and
  credential boundaries do not grant a filesystem sandbox.
- `internal/process/process.go` configures the selected native executable,
  environment and working directory. Its process-scope implementations retain
  descendant lifecycle ownership. No root-confinement policy is supplied by the
  Grok runner, and the process configuration has no filesystem sandbox field.
- `internal/harness/grok/tools.go` validates bounded original `target_file` data
  and matching native results. It does not restrict the native Read to the
  assigned workspace. These are observation validators, not a native
  pre-execution filesystem policy.
- The retained `TestManualNativeGrokFileTools` fixture includes
  `owned-read-outside`: it generates a private file outside the assigned
  workspace and checks its contents in the next scripted provider request.
  The harness contract records installed-native macOS evidence for that profile.
  This pass inspected the fixture and contract; it did not rerun that opt-in
  installed-native test.
- The pinned Read profile has no client permission request. A path check after
  a notification or at public result storage cannot prove prevention of the
  original native filesystem read. No synthetic approval callback, cancellation
  race or provider-output filtering is accepted as a confinement proof.

The review's filesystem-access premise is supported. Passing ordered-journal,
response, cleanup and protocol tests cannot disprove it.

## Contract boundary and pending decision

The normative Native Agent Options requirement preserves native permission
behavior and says those controls neither create a common DeliDev sandbox nor
emulate native approval. The API Credential Proxy requirement explicitly adds
no OS sandbox and distinguishes scoped upstream-key protection from unrestricted
same-user filesystem/process access. The issue #1091 public contract preserves
automatic Read without inventing an allow/deny response and requires retaining
the existing first-text execution path.

Consequently, confinement or disabling existing public Grok input changes the
documented execution boundary. A maintainer decision has been requested before
changing that policy. The proposed choices are a pre-input public Grok gate until
confinement is ready, implementing filesystem confinement in this PR, or keeping
the existing native trust model. Merely reverting to the earlier first-text
observation validator does not establish prevention of native tool execution;
a disabling policy must be enforced before public native input. A confinement
policy needs independent original-process and symlink/escape evidence on every
supported platform, while preserving native protocol behavior and owned cleanup.

The finding remains unresolved; no risk acceptance, successful confinement,
review approval or merge readiness is inferred. The maintenance heartbeat stays
active and must not repeat this blocked policy mutation or ask the same question
without a decision or new evidence.

The initial current-head inventory found no failing checks and several pending
CI jobs. Pending checks remain separate from this review blocker and are not
passing results. This pass changes validation evidence only; no protocol,
migration, source, ownership or policy contract is changed.

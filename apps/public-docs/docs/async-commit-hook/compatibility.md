# Compatibility and validation evidence

Unix descendant cleanup is verified with detached, reparented processes on the local macOS host and in a Linux container. macOS checks require the account’s background launchd domain; unavailable ownership services prevent command startup. Finish active runs before upgrading, and keep ownership state intact until completion. These checks do not replace qualification on every minimum supported OS version.

Configuration, state, CLI JSON and local API start at version 1. The release artifacts target macOS 13+, Windows 10 22H2+ and Ubuntu 22.04 LTS+ on amd64/arm64. Browser support is current stable desktop Chrome and Edge; agent adapters target Codex, Claude Code and OpenCode. Shells are user-installed.

Build validation, local automated tests and actual platform integration are distinct. The six-target actual-machine integration campaign was explicitly excluded from this implementation and is not represented as completed. Each release includes compatibility metadata describing the evidence actually produced. There is no workload latency/throughput SLA.

Local validation on September 19, 2026 used macOS 26.6.2 arm64, sh (GNU bash 3.2.57), Chrome 153.0.8010.48, Codex CLI 0.145.0, Claude Code 2.1.126 and OpenCode 1.1.53. Agent settings were isolated; MCP connection and the supplied skill installation were checked with each client. The shared MCP workflow also passed automated protocol tests. Edge execution was unavailable in that environment and has not been recorded as validated. Windows and Linux archives passed cross-build checks; that does not certify runtime integration on those systems or on the minimum OS versions.

No Safari/Firefox/mobile UI, multi-account system daemon, remote results, feature flags, GitHub Actions execution, act/Runmoor integration, plugin ABI, automatic command retry or background automatic update is provided.

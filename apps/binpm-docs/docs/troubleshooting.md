# Troubleshooting

Use this page when binpm can parse your request but cannot select, verify, install, or expose the expected executable. The checks below focus on read-only diagnostics first, then on the specific distribution and target boundaries that commonly explain install failures.

## Explain Asset Selection

Use `binpm explain` to inspect how binpm reads a source, chooses a release asset for your target, finds a binary, and determines verification status.

```bash
binpm explain github:BurntSushi/ripgrep
binpm explain rg --local
```

Explaining a source may contact the source provider for release information, but it does not change manifests, lockfiles, cached assets, or installed executables.

GitHub.com shorthand input such as `BurntSushi/ripgrep` or `https://github.com/BurntSushi/ripgrep` is normalized to `github:BurntSushi/ripgrep`. If you paste a GitLab URL, rewrite it as `gitlab:<host>/<namespace...>/<project>`; direct URL installs are not a binpm source backend.

## Fix GitLab HTTPS Rejections

GitLab assets are scored only after binpm verifies that the release link URL, any direct asset URL, and the final redirect target are HTTPS. `binpm explain` distinguishes those rejection reasons before target scoring.

If every matching GitLab asset is rejected for HTTPS, update the GitLab release link to use HTTPS or publish a secure direct asset URL. Redirect diagnostics show only the origin, so query strings, credentials, and tokens are not echoed.

## Resolve CPU Feature Variants

Some projects publish CPU feature variants such as `baseline` and `modern`. binpm treats these as CPU feature signals, not architecture names.

Automatic selection prefers baseline-compatible assets. A `modern` asset is rejected unless binpm has explicit host CPU capability support or you add a target override after verifying compatibility.

For modern-only releases, first verify that the target machines support the upstream CPU feature requirements. Then add a canonical target override, for example `[tools.<cmd>.targets.linux-x86_64-gnu]`, that names the exact modern asset and upstream binary. `binpm explain <source>` may print an unverified snippet to start from, but source explain has not downloaded or inspected the archive.

## Resolve Binary Ambiguity

When an archive contains multiple plausible executables, the error lists candidate archive members and includes retry commands.

```bash
binpm install <source> --as <cmd> --bin <candidate>
binpm add <cmd> <source> --bin <candidate>
binpm x --package <source> --bin <candidate> <cmd>
```

Use the `install --as ... --bin ...` form for a global command alias, the `add` form to persist a local installed command alias in `binpm.toml`, and the `x --package` form for one-off execution. In each command, `<cmd>` is the installed command alias and `<candidate>` is the upstream binary or archive member.

## Resolve binpm Distribution Failures

Homebrew installs consume first-party prebuilt binpm archives for macOS and Linux x64/arm64. If Homebrew reports an unsupported platform or a missing release archive, the formula is not expected to build from source. Retry on a supported Homebrew platform, use the direct installer on a supported host, use `cargo binstall`, or build from source.

`cargo-binstall` for binpm also resolves first-party release assets only. Quick-install and compile fallbacks are disabled, so an unsupported cargo-binstall target should be treated as a distribution boundary instead of a prompt to bypass verification or use an unowned binary source.

Direct installer failures before any artifact download usually mean the host is outside the first-party binpm release matrix or `cosign` is missing from `PATH`. Install `cosign`, choose a supported macOS/Linux/Windows x64 or arm64 host, or build from source for other runtime targets.

## Validate Local State

Use `binpm doctor` to inspect manifest discovery, lockfile readability, cache state, installed executables, PATH visibility, and provider configuration without changing them.

```bash
binpm doctor
```

When `~/.binpm/bin` is not on `PATH`, doctor prints setup guidance that points to `binpm env --global --shell <bash|zsh|fish|powershell>`. binpm does not edit profile files from doctor or env output; persistent profile changes are opt-in. Persist only the printed global bin command in shell profiles; the project-local command is for the current project or shell session only.

## Verify Installed Records

Use `binpm verify` to validate local tool metadata, cached bytes, and installed executables.

```bash
binpm verify
binpm verify --require-verified
```

# Troubleshooting

## Explain Asset Selection

Use `binpm explain` to inspect how binpm reads a source, chooses a release asset for your target, finds a binary, and determines verification status.

```bash
binpm explain github:BurntSushi/ripgrep
binpm explain rg --local
```

Explaining a source may contact the source provider for release information, but it does not change manifests, lockfiles, cached assets, or installed executables.

## Resolve Binary Ambiguity

When an archive contains multiple plausible executables, the error lists candidate archive members and includes retry commands.

```bash
binpm add <cmd> <source> --bin <candidate>
binpm x --package <source> --bin <candidate> <cmd>
```

Use the `add` form to persist the selection in `binpm.toml`. Use the `x --package` form for one-off execution.

## Validate Local State

Use `binpm doctor` to inspect manifest discovery, lockfile readability, cache state, installed executables, PATH visibility, and provider configuration without changing them.

```bash
binpm doctor
```

When `~/.binpm/bin` is not on `PATH`, doctor prints setup guidance that points to `binpm env --shell <bash|zsh|fish|powershell>`. binpm does not edit profile files from doctor or env output; persistent profile changes are opt-in. Persist only the printed global bin command in shell profiles; the project-local command is for the current project or shell session only.

## Verify Installed Records

Use `binpm verify` to validate local tool metadata, cached bytes, and installed executables.

```bash
binpm verify
binpm verify --require-verified
```

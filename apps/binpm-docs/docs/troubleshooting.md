# Troubleshooting

## Explain Asset Selection

Use `binpm explain` to inspect source parsing, release selection, target normalization, asset candidate scoring, binary discovery, and verification decisions.

```bash
binpm explain github:BurntSushi/ripgrep
binpm explain rg --local
```

Source-form explanation may perform read-only provider release lookup. It must not mutate manifests, lockfiles, package records, cache entries, or executables.

## Validate Local State

Use `binpm doctor` to inspect manifest discovery, lockfile readability, package records, cache state, installed executable records, PATH visibility, and provider configuration without mutation.

```bash
binpm doctor
```

When `~/.binpm/bin` is not on `PATH`, doctor prints setup guidance that points to `binpm env --shell <bash|zsh|fish|powershell>`. binpm does not edit profile files from doctor or env output; persistent profile changes are opt-in. Persist only the printed global bin command in shell profiles; the project-local command is for the current project or shell session only.

## Verify Installed Records

Use `binpm verify` to validate lockfile records, package records, cache bytes, and installed executable records.

```bash
binpm verify
binpm verify --require-verified
```

## Documentation Validation

Run the docs build before publishing documentation changes:

```bash
pnpm --filter binpm-docs test
```

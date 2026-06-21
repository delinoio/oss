# Troubleshooting

## Explain Asset Selection

Use `binpm explain` to inspect source parsing, release selection, target normalization, asset candidate scoring, binary discovery, and verification decisions.

```bash
binpm explain github:BurntSushi/ripgrep
binpm explain rg --local
```

Source-form explanation may perform read-only provider release lookup. It must not mutate manifests, lockfiles, package records, cache entries, or executables.

## Resolve Binary Ambiguity

When an archive contains multiple plausible executables, the error lists candidate archive members and includes retry commands.

```bash
binpm add <cmd> <source> --bin <candidate>
binpm x --package <source> --bin <candidate> <cmd>
```

Use the `add` form to persist the selection in `binpm.toml`. Use the `x --package` form for one-off execution.

## Validate Local State

Use `binpm doctor` to inspect manifest discovery, lockfile readability, package records, cache state, installed executable records, PATH visibility, and provider configuration without mutation.

```bash
binpm doctor
```

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

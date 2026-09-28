# Getting started with clibox

## Install and check the version

After checking the requirements in [Install clibox](/clibox/install), pin published version 0.2.0 for these examples, then check its help:

```sh
pnpm add -D -E @delino/clibox@0.2.0
pnpm exec clibox --version
pnpm exec clibox --help
```

These examples use the published 0.2.0 command names. See [Migrating older command syntax](/clibox/migration) if upgrading from 0.1.6.

## Try commands without changing files

```sh
pnpm exec clibox text replace hello hi --text "hello world"
pnpm exec clibox base64 encode --text hello
pnpm exec clibox hash compute --text hello
pnpm exec clibox time add 2024-01-31 --from date --months 1
```

Text replacement prints `hi world`; Base64 prints `aGVsbG8=`. Neither adds a newline. Time arithmetic prints `2024-02-29T00:00:00Z`. Hashing uses SHA-256 by default and hashes the exact text without a newline.

## Use a package script

```json
{
  "scripts": {
    "build:production": "clibox run env NODE_ENV=production node build.js",
    "check:port": "clibox port list 3000",
    "wait:server": "clibox wait tcp localhost:3000 --timeout 30s"
  }
}
```

Replace `build.js` with your project's existing entrypoint. `run env` changes only the child environment and preserves the child's exit status. `port list` observes owners without terminating them. Start your own server before running `wait:server`; a successful TCP connection only proves connectivity.

## Process local configuration

For your own existing input files:

```sh
pnpm exec clibox dotenv list --input local.env
pnpm exec clibox dotenv merge base.env local.env --output merged.env
pnpm exec clibox yaml normalize --input config.yaml --output normalized.yaml
```

`dotenv list` prints keys only. Merge output can contain secret values; choose its destination carefully. Existing output is preserved unless you explicitly authorize replacement. See [Configuration commands](/clibox/configuration) for syntax and size limits, and [Output and cancellation](/clibox/output) before using `--force` or `--in-place`.

Use the [command index](/clibox/commands) to find the complete syntax and examples.

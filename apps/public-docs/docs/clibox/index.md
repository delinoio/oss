# clibox

clibox is a native command-line toolbox for everyday development, available through `@delino/clibox` on npm and signed Linux release archives. Pin it in a JavaScript project to share the same command version across operating systems.

## What you can do

- Run a child with environment variables, inspect or terminate local port owners, open resources, and use the desktop text clipboard.
- Coordinate scripts with local rate limits or locks, HTTP service readiness, retries, and runtime or idle timeouts.
- Replace text, format or adjust dates, encode or decode Base64, and compute or verify hashes.
- Wait for a TCP endpoint, HTTP response, or regular file.
- List dotenv keys, merge configuration layers, and normalize YAML.
- In source builds for a future release, trace file operations, compare records, watch observed inputs, measure read coverage and delays, verify reproduction bundles, and pause matching operations.

The [command index](/clibox/commands) covers all 25 commands in published version 0.2.0 and links to the seven upcoming [file access workflows](/clibox/fspy). Start with [installation](/clibox/install) and the [getting started guide](/clibox/getting-started).

## Supported environments

The npm launcher requires Node.js 22 or newer. Prebuilt packages support macOS and Windows x64/arm64, and Linux x64/arm64 with glibc or musl. GNU Linux requires glibc 2.34 or newer. Native Linux installation does not require Node.js. Desktop clipboard and resource-opening commands require a usable desktop session and, on Linux, separately installed helpers.

## Predictable scripts

Use the same commands from a shell or `package.json` scripts. clibox works with your current user permissions and adds no saved application configuration, cache, history, or telemetry. Command results go to stdout and redacted diagnostics go to stderr. See [output and cancellation](/clibox/output) for exit codes, partial output, and file replacement boundaries.

Published version 0.2.0 includes `run env`, the five `run with-*` execution wrappers, and `system cpus`. The [migration guide](/clibox/migration) explains older syntax. The `fspy` family remains in source until a future manual release.

## Learn more

- [System commands](/clibox/system)
- [Text, time, Base64, and hashes](/clibox/transformations)
- [Readiness waits](/clibox/wait)
- [Configuration commands](/clibox/configuration)
- [Releases and verification](/clibox/releases)
- [Troubleshooting](/clibox/troubleshooting)

clibox is Apache-2.0 licensed. Report problems through [GitHub Issues](https://github.com/delinoio/oss/issues).

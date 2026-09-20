# TaskFlow

TaskFlow schedules your existing build, test, installation, and development commands.
The `tflow` CLI discovers projects from pnpm, Cargo, and Go, then combines those
relationships with tasks declared in `taskflow.yml`.

## Build from source

TaskFlow currently ships as source. Clone the [Delino OSS repository](https://github.com/delinoio/oss),
install Rust through rustup and a platform C compiler, and run this command from the checkout:

```sh
cargo build --locked --release -p taskflow --bin tflow
```

Add the generated `target/release` directory to your executable search path. The
binary is `tflow` on macOS/Linux and `tflow.exe` on Windows. The checkout selects
its Rust toolchain. There is no published Cargo package or prebuilt release yet.

Host execution targets macOS 13+, Linux, and Windows on x64 and arm64. Docker tasks
run Linux containers through a local daemon. Install only the native tools your
tasks use. pnpm discovery requires lockfile-query support (pnpm 10.23 or later).
Native conformance fixtures exercise pnpm 10.26.2, Go 1.25, the selected Rust
toolchain, Vitest 4.1.11, and Jest 29.7.0. Other command shapes may require the
generic test adapter described in [Sharding and CI](taskflow/ci).

Linux requires a mounted proc filesystem and permission to execute anonymous memory-backed
files (`memfd`); temporary storage may be mounted `noexec`. macOS requires executable
private temporary storage and an available user launchd domain. TaskFlow reports an error when
process ownership cannot be established or cleanup cannot be verified.

## First task

Create `taskflow.yml` in a project with Node.js installed:

```yaml
version: 1
project: hello
tasks:
  greet:
    command: [node, -e, "console.log('Hello from TaskFlow')"]
```

```sh
tflow check
tflow query tasks
tflow plan greet
tflow run greet
```

Tasks execute in the directory containing their configuration. TaskFlow does not
invent commands for projects without configuration. Those projects still appear
in graph queries. A native dependency does not automatically add a separate build
command: Cargo, Go, and JavaScript build tools retain their own compilation work.

## Guides

- [Configuration](taskflow/configuration): project discovery, prerequisites, inputs, and platforms.
- [Commands and sessions](taskflow/commands): queries, affected selection, services, watches, and schedules.
- [Caching and secrets](taskflow/cache): output restoration, dotenv, masking, and R2/S3.
- [Sharding and CI](taskflow/ci): supported adapters, complete accounting, and GitHub Actions export.

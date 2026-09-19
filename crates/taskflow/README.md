# TaskFlow

TaskFlow runs explicit commands using native project relationships, task prerequisites,
declared inputs, and owned outputs. It provides the `tflow` CLI and a reusable Rust
library. It leaves compilation and native incremental caches to Cargo, Go, and pnpm.

The crate is unpublished. From a source checkout, build with:

```sh
cargo build --locked --release -p taskflow --bin tflow
```

Use the generated `target/release/tflow` executable (`tflow.exe` on Windows).
Run `tflow --help`, then `tflow check` and `tflow plan <task>` in your project.

- [User guide](../../apps/public-docs/docs/taskflow.md)
- [Configuration](../../apps/public-docs/docs/taskflow/configuration.md)
- [Commands and sessions](../../apps/public-docs/docs/taskflow/commands.md)
- [Caching and secrets](../../apps/public-docs/docs/taskflow/cache.md)
- [Sharding and CI](../../apps/public-docs/docs/taskflow/ci.md)
- [Engine contract](../../docs/crates-taskflow-foundation.md)
- [Conformance evidence](../../docs/crates-taskflow-conformance.md)

Validate the isolated engine with `cargo test -p taskflow`. External native and
Docker/S3 fixtures are explicitly ignored in the default suite; run
`cargo test -p taskflow --test conformance -- --include-ignored` after installing
the tools and pulling the immutable fixture images documented in the conformance
contract. CI exercises the native matrix separately from Docker/S3 transport.

`cargo run -p taskflow -- schema` prints the version-1 configuration JSON Schema.
The committed `taskflow.schema.json` must reproduce exactly.

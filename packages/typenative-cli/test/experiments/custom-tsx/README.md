# Custom TSX native rendering experiment

This macOS arm64 experiment uses a small repository-owned TSX runtime instead of React or Solid. It checks a numeric signal, synchronous effects, an intrinsic-element tree, a text binding and a programmatically dispatched button handler. A small transform using TypeScript's AST API lowers supported TSX to calls to the local `h` factory and preserves type annotations. ScriptC compiles the transformed application into native code.

The native application calls a Rust static library through ScriptC FFI. The library keeps one Blitz document in the same process, updates its counter element and paints two 640 × 360 PNG frames with the CPU renderer. It checks that both frames contain painted content and that their pixels differ. The initial frame contains counter `0`; the handler changes it to `1` before the second frame. This tests offscreen rendering, not a native window or an OS mouse event.

## Reproduce

Use Node 24, Rust/Cargo and an installed macOS SDK. Prepare exact compiler dependencies outside the repository, then run from the repository root:

```sh
probe_dir="$(mktemp -d '/private/tmp/typenative-custom-tsx.XXXXXX')"
npm install --prefix "$probe_dir" --save-exact scriptc@0.3.5 typescript@5.9.3
node "packages/typenative-cli/test/experiments/custom-tsx/run.mjs" "$probe_dir" "$probe_dir/output"
```

The output directory must not exist. The runner never installs or upgrades tools. It copies the standalone host fixture there and builds with its lockfile and the reviewed Blitz revision. The Rust fixture is not the production host and is not a root Cargo workspace member. Its locked dependencies are experimental inputs, not a dependency security acceptance claim. The `target` directory, generated sources, executables, images and logs belong to the caller's output directory.

The runner records commands, versions, exit statuses and fixed-fixture output in `result.json`. It requires exact stdout and empty stderr from application executions, checks the final executable's arm64 Mach-O header and returns status 1 on failed checks. Compiler and execution subprocesses have 120-second limits; the cold Rust build has a 15-minute limit. On failure, inspect the report before rerunning with a new output directory. On success, inspect `frame-0.png` and `frame-1.png`.

## Boundaries

- The runtime reruns all registered effects synchronously. It has no automatic dependency tracking, disposal, batching, scheduling or reconciliation.
- JSX supports intrinsic elements and the fixture's `id`, `text` and `onClick` properties. Function components, arbitrary attributes and React API compatibility are excluded.
- The narrow JSX transform rejects unsupported tags, spread attributes and child forms. `transpileModule` produces the Node reference; ScriptC checks the typed, transformed native input. This is not a complete source TSX typechecking toolchain.
- The host accepts only the fixture's node IDs and text values. It uses `set_inner_html` with those restricted values and retains the containing Blitz document. It is not a general HTML mutation API.
- The Node reference and the separate native reference use an in-memory validation sink. That sink is never linked into the Blitz executable and does not establish rendering.
- The final host graph is checked for common scripting-engine packages. This and the use of ScriptC are not a complete binary audit for embedded engines.
- Native windows, OS input, accessibility, Korean IME, application lifecycle, native code replacement, services, plugins, packaging and other platforms remain outside this experiment.

This experiment does not change TypeNative's React product contract or enable `tn dev`, `tn build` or `tn run`.

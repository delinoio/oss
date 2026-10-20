# TypeNative native todo example

A custom TSX todo application compiled by ScriptC and linked to a Rust Blitz
window in the same native process. Node.js prepares and launches the executable;
it does not execute the application. No browser, webview or JavaScript engine is
used by this example.

The example supports adding tasks with Add or Enter, completion checkboxes,
deletion, a remaining count, an empty state, resizing and scrolling. The input
receives focus when the app opens. Tab moves between controls; Space or Enter
activates a focused control. Enter during composition does not submit a task.
Application text is escaped before HTML serialization.

## Prepare and run

Use macOS arm64, Node.js 24, Rust/Cargo and the Xcode Command Line Tools or a full
macOS SDK. Install the exact tooling in a directory outside the repository:

```sh
todo_tools="$(mktemp -d '/private/tmp/typenative-todo.XXXXXX')"
npm install --prefix "$todo_tools" --save-exact scriptc@0.3.5 typescript@5.9.3
node "packages/typenative-cli/examples/native-todo/run.mjs" "$todo_tools" "$todo_tools/build"
```

Run these commands from the repository root. The output directory must not
already exist. The runner checks tool versions, copies the standalone Rust
adapter, builds with the committed Cargo lockfile, compiles the application,
checks its arm64 Mach-O header, runs the native integration checks and launches
the window. Compiler dependencies and build artifacts stay outside the repository.
The cold Rust build can take several minutes. `CARGO_TARGET_DIR` may select an
existing build cache outside the repository.
The runner explicitly selects Cargo's `aarch64-apple-darwin` target and links its
target-specific static library, regardless of inherited `build.target` settings.

Interrupting or timing out a build terminates its process group, including
compiler/linker descendants, and escalates to a forced stop when needed.

Append `--build-only` to build and validate without opening the window. The runner
prints the executable and `build-result.json` paths. To reopen the built app,
execute `"$todo_tools/build/typenative-todo"` directly. Node is unnecessary for
that command. Build failures identify the diagnostic file in the output directory.

The report records the source revision, whether build inputs have uncommitted
changes, SHA-256 hashes of the consumed source buffers and a hash of the lowered
TypeScript. Dirty inputs are supported; their hashes identify the actual tested
bytes independently of the previous commit.

## Validation and limits

`pnpm --filter @delino/typenative-cli test` includes reference tests for task
identity, completion, deletion, empty input, Unicode and HTML escaping. The native
runner also invokes `typenative-todo --self-test <output-directory>`. These fixed
checks send synthetic keyboard events through Blitz into compiled TypeScript,
assert DOM and focus updates, and render `initial.png`, `added.png`, `empty.png`
and `narrow.png`. These images are native offscreen renders, not screenshots of
the OS window. Neither test proves physical keyboard/mouse input, screen-reader
support or Korean IME acceptance. Check those separately in the native window.

Tasks live in memory and reset to the three example tasks on restart. The example
does not implement persistence, installation packages, native code updates, the
public SDK, other platforms or the general `tn dev`, `tn build` and `tn run`
pipeline. Those commands retain their explicit unavailable diagnostic.

The TSX runtime supports intrinsic elements, numeric signals, synchronous effects,
text bindings, the example's typed attributes, explicit handlers and explicit
child arrays. It reserializes the list after a change and retains the entry input.
Focus returns to the input after list changes. It has no component reconciliation,
automatic effect dependency tracking or third-party framework compatibility.

ScriptC is pinned to `0.3.5`, TypeScript to `5.9.3`, and Blitz to revision
`1e5479ae62737d60f7c2f8e8d31524f5cfe2ec01`. The runner checks for common scripting
engines in the Rust dependency graph; this is not a complete binary audit or a
dependency security acceptance claim. Repository-owned code is Apache-2.0.
Dependencies keep their original licenses; this source example is not a release
distribution.

# ScriptC compatibility patches

The manifest pins ScriptC source and the patch digest. `LICENSE-ScriptC` preserves the upstream Apache-2.0 license. The patch modifies upstream source; it does not relicense it or select a newer upstream version.

## Patch scope

- Strict CommonJS functions need unmapped `arguments` just as strict ES module functions do. Recognize local and inherited directive prologues and class strictness. Preserve refusal of sloppy functions, late string expressions, escaped strings and unrelated directives. Remove this patch when the selected upstream revision implements the same semantics with native regression coverage.
- JavaScript `for (key in object)` writes strings to its binding. A later numeric `var key` initializer must not restrict that hoisted binding to numbers. Index these writes by resolved symbol, retain a dynamic slot for unannotated JavaScript bindings, and preserve typed declarations. This applies to minified React `cloneElement` and equivalent JavaScript. Remove this patch when upstream preserves these writes, including captured bindings, with native regression coverage.

## Prepare a compiler checkout

Use a separate checkout at the exact revision in `scriptc.json`. From this package, run:

```sh
pnpm toolchain:patch --check "/absolute/path/to/scriptc"
pnpm toolchain:patch "/absolute/path/to/scriptc"
```

The second command changes the selected checkout. Both commands reject mismatched revisions, tracked edits and corrupt patches. The command never installs or upgrades tools. Follow the selected upstream build instructions to build the compiler, LLVM helper and native runtime packs together. Do not combine source-built compiler output with a runtime pack merely because both report `0.3.5`.

The patch adds compiler regression tests. In the prepared checkout, run:

```sh
pnpm exec vitest run packages/compiler/src/frontend/strict-arguments.test.ts packages/compiler/src/frontend/lowering/lower-stmts.test.ts
```

The native cases need a built host runtime and code-generation helper. They compare compiled output with Node.js reference output. They do not establish TypeNative platform acceptance or full React support. Preserve compiler failures; do not translate a passing source analysis into a native pass.

These patches do not resolve conditional CommonJS export forwarding or development React's wrapped exports. The unmodified public React entry point remains the baseline for the existing `test:native` gate. A direct production-file probe is diagnostic evidence only and must not replace that gate. Do not publish a TypeNative release on the strength of that subset.

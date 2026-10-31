# Configuration and sources

> CacheCow 0.1.0 is unreleased. This guide describes intended workflows, not published artifacts or verified native support.

## Select the project and preset

Run CacheCow from the project directory you intend to build. In a monorepo, the current execution directory identifies the project. CacheCow detects a preset when possible; ambiguous detection must return an actionable error. Use `--preset cargo`, `--preset rspack`, or `--preset rsbuild` to select one explicitly.

```sh
ccow run --preset cargo -- cargo build
ccow run --preset rspack -- rspack build
ccow run --preset rsbuild -- rsbuild build
```

These examples assume the real tool is installed and executable and that its cache configuration can be verified. The `--` separates CacheCow options from the command and its arguments.

## Cargo configuration

Keep Cargo's configured target-dir and build-dir. CacheCow must manage both, processing coincident paths once. Do not relocate source files or treat the whole checkout as a cache. Use [Cargo's build-cache documentation](https://doc.rust-lang.org/cargo/reference/build-cache.html) to configure Cargo itself.

Toolchain selection and build settings must match the source cache. CacheCow must reject reuse when locations or compatibility cannot be verified. It must not change fingerprints or modification times to force a cache hit. Dirty tracked files and untracked files are allowed; Cargo and rustc retain responsibility for rebuilding them correctly.

## Rspack and Rsbuild configuration

Configure a persistent cache for the selected project. Only that cache is eligible for CacheCow management. `node_modules`, package download caches, and frontend final output directories are excluded.

Rspack exposes [persistent cache configuration](https://rspack.rs/config/cache). Rsbuild must expose its configured persistent cache through the frontend integration. CacheCow needs verified cache locations and compatibility information for the actual tool invocation, including its version and build settings.

Create `.ccow/discover.cjs` in the directory where you run the build. Export an async function that receives `{ preset, argv, cwd, env }`, resolves the same configuration as your build, and returns:

```js
{
  schema: 1,
  preset, // "rspack" or "rsbuild"
  caches: [{ type: "persistent", path: absolutePersistentCachePath }],
  outputs: [absoluteFinalOutputPath],
  settings: resolvedBuildSettings,
}
```

Include every compiler's persistent cache and final output. Use absolute paths and a nonempty settings object that identifies the resolved build settings. This shape is an integration contract, not a complete configuration resolver: connect it to your project's actual Rspack or Rsbuild configuration. Reject options or dynamic configuration you cannot resolve faithfully. Do not build, write artifacts, or print to stdout from the hook. The hook runs as trusted project code.

Add managed cache contents to Git ignore rules. CacheCow refuses caches containing tracked or non-ignored files. Unknown locations and unverified compatibility stop reuse.

Use explicit `ccow run` when a package-manager script or local executable bypasses PATH shims. It still requires verified preset configuration. Automatic npm/pnpm/yarn script or local-bin integration is outside the first release.

## Default and explicit sources

The default source is the repository's primary checkout. Select another checkout or worktree only within the same Git repository and under the same OS user.

```sh
ccow run --preset cargo --source "../main-checkout" -- cargo build
ccow prepare --preset cargo --source "../main-checkout" -- cargo build
```

The paths are examples; replace them with your source worktree. The source must have a usable, compatible cache. Different tool versions, platforms, architectures, or build settings make a source incompatible. Unknown compatibility also prohibits reuse.

When running in the default primary checkout itself, CacheCow uses its cache directly without cloning it into itself. If the cache is absent, the requested build runs directly there. Worktrees of a bare repository have no primary checkout: select a usable source explicitly or use the interactive no-source build choice described in [cache recovery](/cachecow/cache#unusable-source).

## Save or clear a source

Settings apply to the current worktree:

```sh
ccow config set source "../main-checkout"
ccow config get
ccow config unset source
```

Source precedence is:

1. The current invocation's `--source` option.
2. The saved per-worktree source.
3. The primary checkout default.

Clearing a saved source restores default selection. Changing source selection does not replace an existing working cache. Use an explicit [replacement](/cachecow/cache#explicit-replacement) when that is intended. CacheCow has no saved snapshot catalog or automatic publication step.

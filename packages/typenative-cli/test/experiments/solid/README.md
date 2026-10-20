# Solid native feasibility probe

This isolated experiment checks whether the published Solid client runtime can propagate a signal update through a render effect and its universal renderer in a ScriptC native executable. The renderer uses an in-memory tree and checks text mutations from `0` to `1`. It is a prerequisite check for a possible Blitz renderer, not a replacement for TypeNative's React contract. It does not test JSX, Blitz, a window, native events, hot updates, or platform acceptance.

Prepare dependencies outside the repository. Run these commands from the repository root with Node 24:

```sh
probe_dir="$(mktemp -d '/private/tmp/typenative-solid.XXXXXX')"
npm install --prefix "$probe_dir" --save-exact scriptc@0.3.5 solid-js@1.9.17 esbuild@0.28.2 typescript@5.9.3
node "packages/typenative-cli/test/experiments/solid/run.mjs" "$probe_dir"
```

The runner validates versions, runs the Node reference, builds and executes a native console control, then checks direct-package and single-file bundled Solid execution. A separate bundle checks the universal renderer against a Node reference. It selects `solid-js/dist/solid.js` explicitly because Solid's default Node export is the server runtime; browser export conditions keep the universal renderer on the same reactive runtime. It validates arm64 Mach-O headers before execution, requires exact expected stdout and empty stderr, and returns status 1 on any failed check. In particular, status 0 from an executable that prints an uncaught error is a failure. Each subprocess has a 120-second timeout. The runner removes its temporary probe directory; the caller owns the dependency directory and may remove it afterward.

The bundle uses esbuild's standard syntax minification, which avoids empty `if` bodies rejected by ScriptC's typechecker. It does not change Solid source or substitute a custom signal implementation. Node results establish only the reference behavior. Successful compilation alone establishes neither reactive execution nor the absence of an embedded engine. Attempt Blitz integration only after native reactive execution and universal renderer execution pass.

Keep execution evidence with the change or CI output, including source revision, versions, commands, exit statuses, output and unperformed checks. This probe intentionally remains outside the regular CLI unit suite and does not install or upgrade workspace dependencies.

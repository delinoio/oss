# Editors and language servers

**pnport 0.1.0 is unreleased, and no editor or language-server version is certified.** This guide describes the integration shape to use after a complete release; it is not a claim that a particular editor currently works.

## Configure the executable

Use an editor's language-server executable setting, when available, to invoke pnport with the chosen project and the server command:

```text
pnport --project <installed-project> run -- <language-server> [server-args...]
```

Set the editor's working directory to the workspace it would ordinarily use. `--project` selects dependency data but does not change cwd. Keep the server executable and arguments as separate entries in editors that accept an argument array; do not add shell quoting to that array. If an editor requires a single command string, follow that editor's own quoting rules.

The server's protocol stdout must stay free of pnport diagnostics. pnport writes its diagnostics to stderr and inherits the child's streams, so the editor can communicate over the child's stdin and stdout. The release contract also covers long-running watch processes without a default timeout.

## Recovery

If the PnP graph or an active package archive changes, restart the language-server process tree. Source-file edits should continue through ordinary watch notifications. A missing package, physical `node_modules` conflict, unsupported executable, or injection failure is a pnport error; inspect [doctor and diagnostic output](/pnport/diagnostics) rather than retrying unvirtualized execution.

Report editor, server, host, and project reproduction details through [GitHub Issues](https://github.com/delinoio/oss/issues). Avoid attaching credentials, environment dumps, or private source files.

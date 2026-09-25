# One-shot CLI tasks

`react-forge run <entry.tsx> --output <file> [--data <json>] [--overwrite] [--json]` loads a trusted TSX module's default task function. The task receives `{ data, signal }`, returns a session, and the CLI exports and disposes it. Use an output extension appropriate for the returned format: `.pptx`, `.docx`, `.xlsx`, `.pdf`, or a Figma `.figma.json` receipt.

```sh
npx react-forge run hello.tsx --output hello.pptx
npx react-forge run hello.tsx --output hello.pptx --data '{"title":"Update"}' --json
npx react-forge --help
npx react-forge --version
```

`--data` is inline JSON, not a file name. `--json` requests structured CLI results and errors. An existing destination fails unless `--overwrite` is given; even with overwrite, an imported Office source cannot be replaced. If a local-document task fails before publication, its destination remains unchanged. See [Getting started](/react-forge/getting-started) for a complete TSX entry and [Office editing](/react-forge/office-editing) for imported files.

The CLI executes task code with ordinary caller permissions. It is not a sandbox, and an explicitly invoked Figma operation can change a remote file. On Unix, SIGINT/SIGTERM request cancellation and cleanup with exit statuses 130/143; Windows console Ctrl+C/Ctrl+Break use status 130. Forceful termination cannot guarantee cleanup. Asynchronous work must observe the supplied signal. Unresolved work has no automatic timeout.

Figma tasks may publish remotely before their receipt is saved. A failure can therefore carry a partial or unknown outcome even if the output path does not exist. Follow the [Figma receipt guidance](/react-forge/figma#publication-outcomes-and-receipts), rather than replaying the task blindly. For repeated updates to the same in-memory document, use the [library session API](/react-forge/sessions) or [local MCP sessions](/react-forge/mcp).

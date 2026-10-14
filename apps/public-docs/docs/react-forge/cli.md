# One-shot CLI tasks

`react-forge run <entry.tsx> --output <file> [--data <json>] [--overwrite] [--json]` loads a trusted TSX module's default task function. The task receives `{ data, signal }`, returns a session, and the CLI exports and disposes it. Use an output extension appropriate for the returned format: `.pptx`, `.docx`, `.xlsx`, `.pdf`, or a Figma `.figma.json` receipt.

Start with the complete [first presentation task](/react-forge/getting-started). The CLI is the shortest route to one exported file; use [sessions](/react-forge/sessions) or [MCP](/react-forge/mcp) when updates must share an in-memory document.

## Run a task

```sh
npx react-forge run hello.tsx --output hello.pptx
npx react-forge run hello.tsx --output hello.pptx --data '{"title":"Update"}' --json
npx react-forge --help
npx react-forge --version
```

`--data` is inline JSON, not a file name. `--json` requests structured CLI results and errors. An existing destination fails unless `--overwrite` is given; even with overwrite, an imported Office source cannot be replaced. If a local-document task fails before publication, its destination remains unchanged. See [Getting started](/react-forge/getting-started) for a complete TSX entry and [Office editing](/react-forge/office-editing) for imported files.

## Cancellation and remote outcomes

The CLI executes task code with ordinary caller permissions. It is not a sandbox, and an explicitly invoked Figma operation can change a remote file. On Unix, SIGINT/SIGTERM request cancellation and cleanup with exit statuses 130/143; Windows console Ctrl+C/Ctrl+Break use status 130. Forceful termination cannot guarantee cleanup. Asynchronous work must observe the supplied signal. Unresolved work has no automatic timeout.

Figma tasks may publish remotely before their receipt is saved. A failure can therefore carry a partial or unknown outcome even if the output path does not exist. Follow the [Figma receipt guidance](/react-forge/formats/figma/#publication-outcomes-and-receipts), rather than replaying the task blindly. For repeated updates to the same in-memory document, use the [library session API](/react-forge/sessions) or [local MCP sessions](/react-forge/mcp).

The [SFX format](/react-forge/formats/sfx/) adds `Format.Wav` tasks with `.wav` output. The [Sprite format](/react-forge/formats/sprite/) adds `Format.Sprite` tasks with `.sprite.zip` output. Both are available in npm `0.2.0`.

## Static 3D formats

[GLB](/react-forge/formats/glb/) and [FBX](/react-forge/formats/fbx/) creation are available in npm `0.2.0`. They use SceneSession with registered geometry/textures, explicit file export, and revision-bound world-space bounds. The released static API excludes existing-file editing, animation and rigging. The FBX material profile targets Blender 4.5; local verification does not establish results on other hosts or FBX applications.

## Analyze a scene (unreleased)

`react-forge analyze <entry.tsx> [--data <json>] [--json]` runs a trusted task that returns a GLB or FBX `SceneSession`, waits for its settled revision, and prints topology totals plus up to ten largest mesh node IDs. `--json` returns the complete report, including all mesh rows. The command itself does not export or publish. Task code runs with your permissions and can explicitly perform its own effects. This command is implemented in source but is not yet in the released npm package.

In the unreleased source extension, a GLB/FBX task can set a [caller geometry budget](/react-forge/formats/glb/#caller-geometry-budgets-unreleased) when it creates its session. `run` writes Warn-mode budget notices to stderr in text mode and includes structured warnings in JSON results. A warning may precede a later failed export, so check the publication result. The CLI has no budget flags; the trusted task owns the policy.

## Animated scene tasks (unreleased)

With the unreleased animation extension, return a `SceneSession` from the same
TSX task interface after registering samplers and rendering clips/joints/morphs.
Run `react-forge run character.tsx --output character.glb` (or `.fbx` for a task
using `Format.Fbx`). No extra animation command or external converter is needed;
the CLI waits for pending work, exports one revision and disposes the session.
The [GLB animation example](/react-forge/formats/glb/#animation-authoring-unreleased)
uses this task shape; [FBX](/react-forge/formats/fbx/#animation-authoring-unreleased)
explains curve sampling. These additions are not in the released static API.

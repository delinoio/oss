# Local MCP sessions

`react-forge mcp [--cwd <directory>]` starts a local stdio MCP server. Configure your MCP client to launch the installed `react-forge` executable with Node.js 24 and an absolute task directory. The directory controls relative tool paths and inline imports; imports inside a TSX entry remain relative to that entry file. Keep protocol stdout reserved for MCP messages.

Use the [CLI](/react-forge/cli) for a single task you run yourself. Use MCP when a trusted local client needs to inspect, update, and export the same in-memory session across tool calls.

## Start the server

```sh
mkdir -p tasks
react-forge mcp --cwd "$PWD/tasks"
```

`react_forge_capabilities` reports supported formats and limits. Use `react_forge_execute` with exactly one of a TSX `code` string or an `entry` path and optional JSON `data`. A new task returns a session ID. Later calls pass that `sessionId` and can reuse an in-memory `state` Map for components, setters, refs, and mount handles. Both inline TSX and file entries use React's automatic JSX runtime; a file entry's `tsconfig` JSX settings do not change that transform.

## Return a reusable session

Create `report.tsx` in the task directory for the sequence below:

```tsx
import { createSession, Format, type McpTaskContext } from "@delino/react-forge";
import { Document, Page, Text } from "@delino/react-forge/pdf";

export default async function task({ session, state, data, signal }: McpTaskContext) {
  signal.throwIfAborted();
  const document = session ?? createSession(Format.Pdf);
  if (!session) {
    state.set("render", (title: string) => document.render(
      <Document language="en-US"><Page><Text>{title}</Text></Page></Document>,
    ));
  }
  const title = typeof data === "object" && data !== null && "title" in data
    && typeof data.title === "string" ? data.title : "First draft";
  try {
    await (state.get("render") as (title: string) => Promise<void>)(title);
    return document;
  } catch (error) {
    if (!session) await document.dispose();
    throw error;
  }
}
```

## Typical local document sequence

1. Call `react_forge_execute` with `{"entry":"report.tsx","data":{"title":"First draft"}}` and retain the returned session ID.
2. Call `react_forge_inspect` with that ID. It waits for settled local work and reports revision and target IDs without writing a file. `react_forge_measure` requires a target ID and that exact revision.
3. Call `react_forge_execute` again with the same entry, session ID, and updated data. The session and its retained state Map are reused.
4. Call `react_forge_export` with the ID and an output path. An existing destination requires explicit overwrite; an imported Office source can never be replaced.
5. Call `react_forge_close` to release the session. Exported files remain available.

`react_forge_sessions` lists active sessions. Inspection can filter by node or kind and paginate. Use native target handles obtained inside the callback for `mount()`; a JSON inspection result is not a replacement handle. The task callback receives `{ session, state, data, signal }` and can return a new session or update the existing one. A failed callback is not a transaction: already completed changes remain.

## Figma and failure recovery

For Figma, `react_forge_refresh` reads selected pages, nodes, or resources; `react_forge_inspect` reads cached targets. `react_forge_publish` explicitly applies changes remotely and can save a `.figma.json` receipt. Inspect a partial or unknown receipt, including an error result's attached receipt, before another publish. `react_forge_inspect` with `view: "receipt"` retrieves the latest receipt without writing. See [Figma](/react-forge/formats/figma/).

Sessions exist only in the server process. Disconnect, shutdown, or worker loss ends them; restart does not replay or restore work. Closing and disposal cancel owned work, but a callback that ignores cancellation or blocks synchronously cannot be forcibly interrupted per tool call. Task output is suppressed to protect protocol stdout and avoid logging document content; use structured results and safe diagnostics for troubleshooting. Caller code runs with normal permissions and is not sandboxed.

When your TSX fails to compile, a module cannot be resolved, a task throws, or React cannot render, the tool error includes a bounded message and `error.diagnostics`. Diagnostics identify the `compile`, `task`, or `render` phase and include an inline, entry, or imported-file line and column when React Forge can map them reliably. File names are relative to the configured task directory. Compiler errors use `malformed_input`; uncaught task and render errors use `render`. A recovered React error boundary does not fail the tool. The response omits source excerpts and full stacks. Your exception message may contain private data and will be visible to your MCP client; operational stderr logs keep only fixed classifications.

The [SFX format](/react-forge/formats/sfx/) uses the same execute, inspect, measure, export and close sequence with `Format.Wav` and `.wav` output; timeline measurements use seconds. The [Sprite format](/react-forge/formats/sprite/) uses that sequence with `Format.Sprite` and `.sprite.zip` output; measurements use logical frame pixels. Both are available in npm `0.2.0`.

## Static 3D formats

[GLB](/react-forge/formats/glb/) and [FBX](/react-forge/formats/fbx/) creation are available in npm `0.2.0`. They use SceneSession with registered geometry/textures, explicit file export, and revision-bound world-space bounds. Existing-file editing, animation and rigging are excluded. The FBX material profile targets Blender 4.5; local verification does not establish results on other hosts or FBX applications.

# Local MCP sessions

`react-forge mcp [--cwd <directory>]` starts a local stdio MCP server. Configure your MCP client to launch the installed `react-forge` executable with Node.js 24 and an absolute task directory. The directory controls relative tool paths and inline imports; imports inside a TSX entry remain relative to that entry file. Keep protocol stdout reserved for MCP messages.

```sh
mkdir -p tasks
react-forge mcp --cwd "$PWD/tasks"
```

`react_forge_capabilities` reports supported formats and limits. Use `react_forge_execute` with exactly one of a TSX `code` string or an `entry` path and optional JSON `data`. A new task returns a session ID. Later calls pass that `sessionId` and can reuse an in-memory `state` Map for components, setters, refs, and mount handles. Inline TSX uses the automatic JSX runtime; a file entry may use its own TypeScript configuration.

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

For Figma, `react_forge_refresh` reads selected pages, nodes, or resources; `react_forge_inspect` reads cached targets. `react_forge_publish` explicitly applies changes remotely and can save a `.figma.json` receipt. Inspect a partial or unknown receipt, including an error result's attached receipt, before another publish. `react_forge_inspect` with `view: "receipt"` retrieves the latest receipt without writing. See [Figma](/react-forge/figma).

Sessions exist only in the server process. Disconnect, shutdown, or worker loss ends them; restart does not replay or restore work. Closing and disposal cancel owned work, but a callback that ignores cancellation or blocks synchronously cannot be forcibly interrupted per tool call. Task output is suppressed to protect protocol stdout and avoid logging document content; use structured results and safe diagnostics for troubleshooting. Caller code runs with normal permissions and is not sandboxed.

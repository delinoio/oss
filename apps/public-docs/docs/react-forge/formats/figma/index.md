# Figma Design creation and editing

React Forge builds a local React model for Figma Design, then applies it only when you explicitly call `publish()` or a publishing CLI/MCP operation. It uses the official remote Figma MCP server. Connect Figma in Codex or Claude Code first, use the official Figma MCP `whoami` result to select a plan key, and use that application's existing authorization. Live authentication currently uses macOS Keychain and has been validated on macOS arm64 with Node.js 24; the local Office/PDF six-host support does not establish Figma authentication on every host.

If you only need a local PPTX, DOCX, XLSX, or PDF, start with [Getting started](/react-forge/getting-started). Figma adds an explicit remote publication step and different recovery rules.

## Connect and create

```tsx
import React from "react";
import { createSession, Format, CredentialSource } from "@delino/react-forge";
import { Document, Page, Frame, Text } from "@delino/react-forge/figma";

// Replace this with a plan key returned by the official Figma account inspection.
const selectedPlanKey = "team::YOUR_SELECTED_TEAM";
const session = createSession(Format.Figma, {
  fileName: "Travel companion",
  planKey: selectedPlanKey,
  credentials: { source: CredentialSource.Codex },
});
try {
  await session.render(<Document><Page name="Mobile">
    <Frame name="Home" width={390} height={844} fill="#F5F1E8">
      <Text name="Title" fontSize={32}>Find your somewhere.</Text>
    </Frame>
  </Page></Document>);
  const result = await session.publish();
  console.log(result.url, result.status);
} finally {
  await session.dispose();
}
```

Here `selectedPlanKey` is the plan key you explicitly selected from the official Figma account response. `CredentialSource.Codex`, `ClaudeCode`, and `Auto` are available; automatic selection stays pinned for the session. React Forge does not refresh or rewrite the connected application's credentials. An expired or rejected credential may be reread once; reconnect Figma in that application if authorization is still unavailable.

## Reopen and edit

`openFigma()` accepts a Design URL or file key string, or a parsed `.figma.json` receipt object. It does not read a receipt from a path string. `refresh({ pageId })` reads a page before inspection; `refresh({ pageId, nodeIds })` narrows reads to selected IDs and ancestors, and `refresh({ resources: true })` reads variables and styles. Choose an existing target from `inspect()`, mount only the intended region, then publish. Within a mounted frame, `target={remoteId}` explicitly selects an existing child. Properties you omit and children you do not select remain under the original author's control; removing an owned container that holds unselected children is rejected.

```tsx
import React from "react";
import { readFile } from "node:fs/promises";
import { openFigma, type FigmaReceipt } from "@delino/react-forge";
import { Text } from "@delino/react-forge/figma";

const receipt = JSON.parse(
  await readFile("previous.figma.json", "utf8"),
) as FigmaReceipt;
const editing = await openFigma(receipt);
try {
  const page = editing.inspect().targets.find((target) => target.kind === "PAGE");
  if (!page) throw new Error("Select a page first.");
  await editing.refresh({ pageId: page.remoteId });
  const title = editing.inspect().targets.find(
    (target) => target.kind === "TEXT" && target.name === "Title",
  );
  if (!title) throw new Error("Select an editable title first.");
  await editing.mount(title, <Text>Make room for the unexpected.</Text>);
  await editing.exportFile("edited.figma.json");
} finally {
  await editing.dispose();
}
```

You can pass a Design URL or file key string directly instead of the parsed receipt. A receipt must contain a `fileKey` to reopen it this way. The export publishes the selected change and writes a new receipt; it does not create a binary `.fig` file.

Pages, Auto Layout frames, text, vector shapes, images, components, variants/instances, variables, and styles are available. `nodeKey` links local component, style, and variable references. Use fonts actually available in the Figma file and register PNG/JPEG images explicitly; Figma image uploads have their own [resource limits](/react-forge/limits-and-troubleshooting).

## Publication outcomes and receipts

Publication reports **complete**, **partial**, or **unknown**. A `.figma.json` receipt records the file key and URL when known, plus the revision, confirmed node bindings, image hashes, and outcome; it is not a `.fig` export and contains no authentication token. Confirmed changes from a partial result can remain on the remote file. A `FigmaPublishError` also carries its receipt, including when saving the receipt file fails after Figma changed.

For an unknown outcome with a file key, reopen the file from the parsed receipt or its Design URL and inspect the remote result before another write. If `create_new_file` failed ambiguously, the receipt may have neither `fileKey` nor `url` and cannot be passed to `openFigma()`. Check the selected Figma account and destination for the intended file. Only after you identify the file reliably should you reopen it by its Design URL or key and inspect its contents. If you cannot establish whether creation succeeded, keep the outcome unknown and do not repeat the create operation. Never blindly repeat ambiguous file or node creation. Cancellation prevents later batches but does not undo completed remote changes.

See [CLI](/react-forge/cli) and [MCP](/react-forge/mcp) for their explicit publication operations. No Figma file is created or changed by ordinary local Office/PDF exports.

# Getting started

Install React Forge as described in [Installation](/react-forge/installation). Create `hello.tsx` in your project:

```tsx
import React from "react";
import { createSession, Format } from "@delino/react-forge";
import { Presentation, Slide, Column, Text } from "@delino/react-forge/pptx";

export default async function task({ data, signal }: {
  data?: { title?: string };
  signal: AbortSignal;
}) {
  signal.throwIfAborted();
  const session = createSession(Format.Pptx);
  await session.render(
    <Presentation>
      <Slide>
        <Column padding={32}>
          <Text style={{ fontSize: 32, bold: true }}>
            {data?.title ?? "Hello from React Forge"}
          </Text>
        </Column>
      </Slide>
    </Presentation>,
  );
  return session;
}
```

Run the task with Node.js 24:

```sh
npx react-forge run hello.tsx --output hello.pptx --data '{"title":"Quarterly update"}'
```

The CLI exports the returned session and disposes it. An existing destination fails by default; add `--overwrite` only when replacing that output is intentional. The task runs with your ordinary process permissions, so execute only code you trust. It does not need a running Office application.

## Next steps

Use the [PPTX guide](/react-forge/pptx) for layouts, tables, images, and charts. The [DOCX](/react-forge/docx), [XLSX](/react-forge/xlsx), and [PDF](/react-forge/pdf) guides use different component sets and models. For repeated updates in one process, use the [session library](/react-forge/sessions); for tool-driven retained sessions, use [MCP](/react-forge/mcp). To modify an imported presentation, document, or workbook, follow [Office editing](/react-forge/office-editing).

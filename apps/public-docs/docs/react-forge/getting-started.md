# Getting started

With Node.js 24 and React Forge installed as described in [Installation](/react-forge/installation), create `hello.tsx` in your project. This task renders one slide and returns its session to the CLI for export:

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
  try {
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
  } catch (error) {
    await session.dispose();
    throw error;
  }
}
```

Run the task with Node.js 24:

```sh
npx react-forge run hello.tsx --output hello.pptx --data '{"title":"Quarterly update"}'
```

The command writes `hello.pptx` in the current directory. Open it in a presentation application to inspect the slide. The CLI exports the returned session and disposes it. An existing destination fails by default; add `--overwrite` only when replacing that output is intentional. The task runs with your ordinary process permissions, so execute only code you trust. It does not need a running Office application.

## Next steps

Use the [PPTX guide](/react-forge/formats/pptx/) to add layouts, tables, images, and charts to this task. The [DOCX](/react-forge/formats/docx/), [XLSX](/react-forge/formats/xlsx/), and [PDF](/react-forge/formats/pdf/) guides use different component sets and models. For repeated updates in one process, use the [session library](/react-forge/sessions); for tool-driven retained sessions, use [MCP](/react-forge/mcp). To modify an imported presentation, document, or workbook, follow [Office editing](/react-forge/office-editing).

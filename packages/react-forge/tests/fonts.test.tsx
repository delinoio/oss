import { fileURLToPath } from "node:url";
import assert from "node:assert/strict";
import test from "node:test";
import React from "react";
import { createSession, importOffice, Format, ErrorCode, ForgeError, type NodeHandle } from "../src/index.js";
import { processPptx } from "../src/native.js";
import * as P from "../src/pptx.js";
import * as W from "../src/docx.js";
import * as X from "../src/xlsx.js";
import * as D from "../src/pdf.js";
const view = (format: Format, text: string) => {
  switch (format) {
    case Format.Pptx: return <P.Presentation><P.Slide><P.Column><P.Text>{text}</P.Text></P.Column></P.Slide></P.Presentation>;
    case Format.Docx: return <W.Document><W.Section><W.Paragraph>{text}</W.Paragraph></W.Section></W.Document>;
    case Format.Xlsx: return <X.Workbook><X.Worksheet name="Fonts"><X.Cell address={{ row: 0, column: 0 }} value={text} /></X.Worksheet></X.Workbook>;
    case Format.Pdf: return <D.Document language="en"><D.Page><D.Paragraph>{text}</D.Paragraph></D.Page></D.Document>;
  }
};

test("all formats validate system CJK/RTL/color emoji and caller-only missing-font failures", async () => {
  for (const format of Object.values(Format)) {
    const system = createSession(format);
    const supplied = createSession(format, { systemFonts: false });
    try {
      await system.render(view(format, "English 한국어 日本語 中文 مرحبا שלום 😀"));
      assert.ok((await system.exportBuffer()).length > 0, format);
      await supplied.registerFont({ path: fileURLToPath(new URL("../../../crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf", import.meta.url)) });
      await supplied.render(view(format, "English 한국어 日本語 中文"));
      assert.ok((await supplied.exportBuffer()).length > 0, format);
      for (const text of ["\u{10ffff}", "😀"]) {
        await supplied.render(view(format, text));
        await assert.rejects(supplied.exportBuffer(), (error: unknown) => {
          assert.ok(error instanceof ForgeError);
          assert.equal(error.code, ErrorCode.MissingFont);
          assert.equal(error.context.format, format);
          assert.equal(error.context.location, "fonts");
          return true;
        });
      }
    } finally { await Promise.all([system.dispose(), supplied.dispose()]); }
  }
});

test("registered fallback fonts participate in revision identity", async () => {
  const session = createSession(Format.Docx);
  try {
    await session.render(view(Format.Docx, "Stable text"));
    await session.exportBuffer();
    const previous = session.revision;
    await session.registerFont({ path: fileURLToPath(new URL("../../../crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf", import.meta.url)) });
    await session.exportBuffer();
    assert.equal(session.revision, previous + 1);
  } finally { await session.dispose(); }
});

test("PPTX inspection defers shaping until caller fonts can be registered", async () => {
  const font = { path: fileURLToPath(new URL("../../../crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf", import.meta.url)) };
  const original = createSession(Format.Pptx, { systemFonts: false });
  let imported;
  try {
    await original.registerFont(font);
    await original.render(view(Format.Pptx, "Original text"));
    const bytes = await original.exportBuffer();
    // Exercise the native boundary directly as well: import must not depend on
    // system fallback even before the session has any registered font assets.
    const result = await processPptx("inspect", {}, bytes, new Map(), original.documentId, 0,
      new AbortController().signal, { system: false, ids: [] });
    assert.ok(JSON.parse(result.geometry).source_identity);
    imported = await importOffice(Format.Pptx, bytes, { systemFonts: false });
    const target = imported.inspect().targets.find(t => t.kind === "text")!;
    const ref = React.createRef<NodeHandle>();
    await imported.mount(target, <P.Text ref={ref}>Caller supplied text</P.Text>);
    await assert.rejects(imported.exportBuffer(), { code: ErrorCode.MissingFont });
    await imported.registerFont(font);
    assert.ok((await imported.exportBuffer()).length > 0);
    assert.ok((await imported.measure(ref.current!, { revision: imported.revision })).width > 0);
  } finally { await original.dispose(); await imported?.dispose(); }
});

import assert from "node:assert/strict";
import test from "node:test";
import React from "react";
import { createSession, Format, ErrorCode, ForgeError } from "../src/index.js";
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
      await supplied.registerFont({ path: new URL("../../../crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf", import.meta.url).pathname });
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
    await session.registerFont({ path: new URL("../../../crates/forge-tree-doc/assets/fonts/noto-sans-kr/NotoSansKR-VF.ttf", import.meta.url).pathname });
    await session.exportBuffer();
    assert.equal(session.revision, previous + 1);
  } finally { await session.dispose(); }
});

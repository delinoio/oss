import assert from "node:assert/strict";
import test from "node:test";
import React from "react";
import { createSession, ErrorCode, Format } from "../src/index.js";
import { Column, Presentation, Slide, Text } from "../src/pptx.js";

test("presentation leaves reject children without exporting an older valid render", async () => {
  const session = createSession(Format.Pptx);
  const view = (child: React.ReactNode) => <Presentation><Slide><Column>{child}</Column></Slide></Presentation>;
  try {
    await session.render(view(<Text>Valid revision</Text>));
    assert.ok((await session.exportBuffer()).length > 0);
    for (const type of ["list", "image", "shape", "chart", "connector"]) {
      await session.render(view(React.createElement(`pptx:${type}`, { items: ["Item"] }, <Text>Must not disappear</Text>)));
      await assert.rejects(session.exportBuffer(), { code: ErrorCode.MalformedInput });
    }
    await session.render(view(<Text>Recovered revision</Text>));
    assert.ok((await session.exportBuffer()).length > 0);
  } finally { await session.dispose(); }
});

test("presentation styles reject unsupported semantics and retain supported styling", async () => {
  const session = createSession(Format.Pptx);
  try {
    for (const style of [{ background: "#ffffff" }, { align: "right" }, { language: "en" }, { direction: "rtl" }, { typo: true }]) {
      await session.render(<Presentation><Slide><Column>{React.createElement("pptx:text", { style }, "Cannot drop style")}</Column></Slide></Presentation>);
      await assert.rejects(session.exportBuffer(), { code: ErrorCode.MalformedInput });
    }
    await session.render(<Presentation><Slide><Column><Text style={{ fontSize: 20, bold: true, italic: false, underline: true, color: "#123456" }}>Supported style</Text></Column></Slide></Presentation>);
    assert.ok((await session.exportBuffer()).length > 0);
  } finally { await session.dispose(); }
});

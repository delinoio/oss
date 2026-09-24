import assert from "node:assert/strict";
import { mkdtemp, open, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";
import { createElement } from "react";
import { createSession, importOffice, ErrorCode, Format, limits } from "../src/index.js";
import { RenderRoot } from "../src/renderer.js";

test("published document and asset byte limits reject sparse files before allocation", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-limits-"));
  const path = join(directory, "oversized.bin");
  const file = await open(path, "wx");
  const session = createSession(Format.Pdf);
  try {
    await file.truncate(limits.officeBytes + 1);
    for (const format of [Format.Pptx, Format.Docx, Format.Xlsx]) {
      await assert.rejects(importOffice(format, { path }), { code: ErrorCode.ResourceLimit });
    }
    await file.truncate(limits.imageBytes + 1);
    await assert.rejects(session.registerImage({ path }), { code: ErrorCode.ResourceLimit });
    await assert.rejects(session.registerFont({ path }), { code: ErrorCode.ResourceLimit });
  } finally { await file.close(); await session.dispose(); await rm(directory, { recursive: true, force: true }); }
});

test("tree node, depth and byte boundaries reject the latest snapshot and allow recovery", async () => {
  const root = new RenderRoot("limits");
  try {
    const nodes = Array.from({ length: limits.treeNodes }, (_, key) => createElement("shape", { key }));
    await root.render(nodes);
    assert.equal(root.snapshot().length, limits.treeNodes);
    await root.render([...nodes, createElement("shape", { key: "overflow" })]);
    assert.throws(() => root.snapshot(), { code: ErrorCode.ResourceLimit });
    let deep = createElement("shape");
    for (let depth = 1; depth < limits.treeDepth; depth++) deep = createElement("column", null, deep);
    await root.render(deep); assert.equal(root.snapshot().length, 1);
    await root.render(createElement("column", null, deep));
    assert.throws(() => root.snapshot(), { code: ErrorCode.ResourceLimit });
    await root.render(createElement("text", { text: "x".repeat(limits.treeBytes) }));
    assert.throws(() => root.snapshot(), { code: ErrorCode.ResourceLimit });
    await root.render(createElement("text", { text: "Recovered" }));
    assert.equal(root.snapshot()[0]!.props.text, "Recovered");
  } finally { await root.dispose(); }
});

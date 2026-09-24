import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import React, { Suspense, createRef, use, useState } from "react";
import { createSession, importOffice, Format, ErrorCode, type NodeHandle } from "../src/index.js";
import { Presentation, Slide, Column, Text } from "../src/pptx.js";
import { processPptx } from "../src/native.js";
import { publish, readSource } from "../src/files.js";

const view = (text: string, ref?: React.Ref<NodeHandle>) => <Presentation><Slide><Column padding={24}>
  <Text nodeKey="title" ref={ref}>{text}</Text>
</Column></Slide></Presentation>;

test("native library generation, stable revision measurements, invalid latest render and retry", async () => {
  const session = createSession(Format.Pptx);
  const ref = createRef<NodeHandle>();
  const events: string[] = [];
  session.subscribe(event => { events.push(JSON.stringify(event)); throw new Error("observer failure"); });
  try {
    await session.render(view("Private text", ref));
    const bytes = await session.exportBuffer();
    assert.equal(bytes.subarray(0, 2).toString(), "PK");
    assert.equal(session.revision, 1);
    const geometry = await session.measure(ref.current!, { revision: 1 });
    assert.equal(geometry.x, 24);
    await assert.rejects(session.measure(ref.current!, { revision: 0 }), { code: ErrorCode.Conflict });
    await session.render(<Presentation><Slide><Column><Text style={{ fontSize: -1 }}>Invalid</Text></Column></Slide></Presentation>);
    await assert.rejects(session.exportBuffer());
    await session.render(view("Retry"));
    assert.ok((await session.exportBuffer()).byteLength > 0);
    assert.doesNotMatch(events.join(""), /Private text|observer failure|Users/);
  } finally { await session.dispose(); }
  await assert.rejects(async () => session.exportBuffer(), { code: ErrorCode.Disposed });
});

test("exports pin React state before native work and later updates cannot alter them", async () => {
  const session = createSession(Format.Pptx);
  let set!: (s: string) => void;
  function Document() { const [value, update] = useState("First revision"); set = update; return view(value); }
  try {
    await session.render(<Document />);
    const first = session.exportBuffer();
    // prepare crosses one event-loop turn before pinning; the native work then
    // runs independently while this update commits on the JavaScript thread.
    await new Promise(resolve => setTimeout(resolve, 20));
    set("Second revision");
    const second = session.exportBuffer();
    const [one, two] = await Promise.all([first, second]);
    const inspect = (bytes: Buffer) => processPptx("inspect", {}, bytes, new Map(), session.documentId, 0, new AbortController().signal);
    const [a, b] = await Promise.all([inspect(one), inspect(two)]);
    assert.match(a.model, /First revision/);
    assert.match(b.model, /Second revision/);
  } finally { await session.dispose(); }
});

test("Office import, document-scoped mounts, overlap rejection and independent region updates", async () => {
  const original = createSession(Format.Pptx);
  let imported;
  try {
    await original.render(view("Original"));
    const bytes = await original.exportBuffer();
    imported = await importOffice(Format.Pptx, bytes);
    const inspection = imported.inspect();
    const target = inspection.targets.find(t => t.kind === "text")!;
    await assert.rejects(imported.mount({ ...target }, <Text>Forged</Text>), { code: ErrorCode.InvalidTarget });
    const mounted = await imported.mount(target, <Text>Edited</Text>);
    await assert.rejects(imported.mount(target, <Text>Overlap</Text>), { code: ErrorCode.Conflict });
    const output = await imported.exportBuffer();
    const result = await processPptx("inspect", {}, output, new Map(), imported.documentId, 0, new AbortController().signal);
    assert.match(result.model, /Edited/);
    await mounted.render(<Text>Updated again</Text>);
    assert.ok((await imported.exportBuffer()).byteLength > 0);
    await mounted.unmount();
    assert.ok((await imported.exportBuffer()).byteLength > 0);
  } finally { await original.dispose(); await imported?.dispose(); }
});

test("file conflict, overwrite, source modification and cancellation leave no temporary files", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-files-"));
  const path = join(directory, "output.pptx");
  const session = createSession(Format.Pptx);
  let imported;
  try {
    await session.render(view("Saved"));
    const result = await session.exportFile(path);
    assert.equal(result.published, true);
    const previous = await readFile(path);
    await assert.rejects(session.exportFile(path), { code: ErrorCode.Conflict });
    assert.deepEqual(await readFile(path), previous);
    imported = await importOffice(Format.Pptx, { path });
    await writeFile(path, "External save");
    await assert.rejects(imported.exportFile(path, { overwrite: true }), { code: ErrorCode.Conflict });
    assert.equal((await readFile(path)).toString(), "External save");
    const controller = new AbortController(); controller.abort();
    await assert.rejects(session.exportFile(path, { overwrite: true, signal: controller.signal }), { code: ErrorCode.Cancelled });
    assert.deepEqual(await readdir(directory), ["output.pptx"]);
    await session.exportFile(path, { overwrite: true });
    assert.equal((await readFile(path)).subarray(0, 2).toString(), "PK");
  } finally { await session.dispose(); await imported?.dispose(); await rm(directory, { recursive: true, force: true }); }
});

test("pending Suspense export cancels and dispose completes without resolving its promise", async () => {
  const session = createSession(Format.Pptx);
  const pending = new Promise<string>(() => {});
  function Pending() { return view(use(pending)); }
  await session.render(<Suspense fallback={view("fallback")}><Pending /></Suspense>);
  const controller = new AbortController();
  const output = session.exportBuffer({ signal: controller.signal });
  controller.abort();
  await assert.rejects(output, { code: ErrorCode.Cancelled });
  await session.dispose();
});

test("bounded input reads and concurrent no-clobber publication", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-atomic-"));
  const path = join(directory, "result.bin");
  try {
    await writeFile(path, "12345");
    await assert.rejects(readSource({ path }, 4), { code: ErrorCode.ResourceLimit });
    await rm(path);
    const results = await Promise.allSettled([publish(Buffer.from("first"), path, {}), publish(Buffer.from("second"), path, {})]);
    assert.equal(results.filter(r => r.status === "fulfilled").length, 1);
    assert.deepEqual(await readdir(directory), ["result.bin"]);
    assert.ok(["first", "second"].includes((await readFile(path)).toString()));
  } finally { await rm(directory, { recursive: true, force: true }); }
});

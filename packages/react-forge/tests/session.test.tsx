import assert from "node:assert/strict";
import test from "node:test";
import { mkdtemp, readFile, readdir, rm, writeFile, symlink, rename } from "node:fs/promises";
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
    assert.ok(events.some(event => JSON.parse(event).source === "native" && JSON.parse(event).status === "completed"));
    assert.ok(events.some(event => JSON.parse(event).source === "native" && JSON.parse(event).status === "failed"));
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
    await assert.rejects(imported.exportFile(path, { overwrite: true }), { code: ErrorCode.UnsupportedEdit });
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

test("dispose cancels an in-flight mounted root and all callers await its cleanup", async () => {
  const original = createSession(Format.Pptx);
  await original.render(view("Original"));
  const imported = await importOffice(Format.Pptx, await original.exportBuffer());
  await original.dispose();
  const target = imported.inspect().targets.find(t => t.kind === "text")!;
  const mounted = imported.mount(target, <Text>New root</Text>);
  const result = mounted.catch(error => error as Error);
  await new Promise<void>(resolve => setImmediate(resolve));
  const first = imported.dispose();
  const second = imported.dispose();
  assert.equal(first, second);
  await Promise.all([first, second, result]);
  await assert.rejects(async () => imported.exportBuffer(), { code: ErrorCode.Disposed });
});

test("a new mounted Suspense root arriving during another pending export is awaited", async () => {
  const original = createSession(Format.Pptx);
  await original.render(<Presentation><Slide><Column><Text>One</Text><Text>Two</Text></Column></Slide></Presentation>);
  const imported = await importOffice(Format.Pptx, await original.exportBuffer());
  await original.dispose();
  let resolveFirst!: (value: string) => void;
  let resolveSecond!: (value: string) => void;
  const first = new Promise<string>(resolve => { resolveFirst = resolve; });
  const second = new Promise<string>(resolve => { resolveSecond = resolve; });
  function Pending({ promise }: { promise: Promise<string> }) { return <Text>{use(promise)}</Text>; }
  try {
    const targets = imported.inspect().targets.filter(t => t.kind === "text");
    await imported.mount(targets[0]!, <Suspense fallback={<Text>Fallback one</Text>}><Pending promise={first} /></Suspense>);
    let finished = false;
    const output = imported.exportBuffer().then(bytes => { finished = true; return bytes; });
    await new Promise<void>(resolve => setImmediate(resolve));
    await imported.mount(targets[1]!, <Suspense fallback={<Text>Fallback two</Text>}><Pending promise={second} /></Suspense>);
    resolveFirst("First ready");
    await new Promise(resolve => setTimeout(resolve, 20));
    assert.equal(finished, false);
    resolveSecond("Second ready");
    const result = await processPptx("inspect", {}, await output, new Map(), imported.documentId, 0, new AbortController().signal);
    assert.match(result.model, /First ready/); assert.match(result.model, /Second ready/);
    assert.doesNotMatch(result.model, /Fallback/);
  } finally { await imported.dispose(); }
});

test("external PPTX mounts retain original source identities without requiring embedded metadata", async () => {
  const source = await readFile(new URL("../../../crates/forge-pptx/tests/fixtures/external.pptx", import.meta.url));
  const session = await importOffice(Format.Pptx, source);
  try {
    assert.ok((await session.exportBuffer()).equals(source), "No-op import must preserve the original package bytes");
    const target = session.inspect().targets.find(target => target.kind === "text")!;
    const mounted = await session.mount(target, <Text>External PPTX edited by React</Text>);
    const bytes = await session.exportBuffer();
    const inspected = await processPptx("inspect", {}, bytes, new Map(), session.documentId, 0, new AbortController().signal);
    assert.match(inspected.model, /External PPTX edited by React/);
    await mounted.render(<Text>Second external edit</Text>);
    assert.match((await processPptx("inspect", {}, await session.exportBuffer(), new Map(), session.documentId, 0, new AbortController().signal)).model, /Second external edit/);
  } finally { await session.dispose(); }
});

test("all mounted roots must still be settled when an export pins its snapshot", async () => {
  const original = createSession(Format.Pptx);
  await original.render(<Presentation><Slide><Column><Text>One</Text><Text>Two</Text></Column></Slide></Presentation>);
  const imported = await importOffice(Format.Pptx, await original.exportBuffer());
  await original.dispose();
  const first = Promise.withResolvers<string>();
  const second = Promise.withResolvers<string>();
  let update!: () => void;
  function First() {
    const [pending, setPending] = useState(false);
    update = () => setPending(true);
    return <Text>{pending ? use(first.promise) : "Earlier state"}</Text>;
  }
  function Second() { return <Text>{use(second.promise)}</Text>; }
  try {
    const targets = imported.inspect().targets.filter(t => t.kind === "text");
    await imported.mount(targets[0]!, <Suspense fallback={<Text>Fallback one</Text>}><First /></Suspense>);
    await imported.mount(targets[1]!, <Suspense fallback={<Text>Fallback two</Text>}><Second /></Suspense>);
    let finished = false;
    const output = imported.exportBuffer().then(bytes => { finished = true; return bytes; });
    await new Promise(resolve => setTimeout(resolve, 20));
    update();
    await new Promise(resolve => setTimeout(resolve, 20));
    second.resolve("Second ready");
    await new Promise(resolve => setTimeout(resolve, 50));
    assert.equal(finished, false);
    first.resolve("First updated");
    const result = await processPptx("inspect", {}, await output, new Map(), imported.documentId, 0, new AbortController().signal);
    assert.match(result.model, /First updated/); assert.match(result.model, /Second ready/);
    assert.doesNotMatch(result.model, /Fallback|Earlier state/);
  } finally { first.resolve("cleanup"); second.resolve("cleanup"); await imported.dispose(); }
});


test("source overwrite always fails closed for unchanged, replaced and aliased paths", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-source-"));
  const source = join(directory, "source.bin");
  try {
    await writeFile(source, "Original");
    const { fingerprint } = await readSource({ path: source }, 100);
    await symlink(source, join(directory, "alias.bin"));
    await symlink(directory, join(directory, "directory-alias"), process.platform === "win32" ? "junction" : "dir");
    for (const output of [source, join(directory, "alias.bin"), join(directory, "directory-alias", "source.bin")]) {
      await assert.rejects(publish(Buffer.from("Export"), output, { source: fingerprint, overwrite: true }), { code: ErrorCode.UnsupportedEdit });
      assert.equal((await readFile(source)).toString(), "Original");
    }
    await writeFile(join(directory, "external.bin"), "External save");
    await rename(join(directory, "external.bin"), source);
    await assert.rejects(publish(Buffer.from("Export"), source, { source: fingerprint, overwrite: true }), { code: ErrorCode.UnsupportedEdit });
    assert.equal((await readFile(source)).toString(), "External save");
    const output = join(directory, "separate.bin");
    await publish(Buffer.from("First"), output, { source: fingerprint });
    await publish(Buffer.from("Second"), output, { source: fingerprint, overwrite: true });
    assert.equal((await readFile(output)).toString(), "Second");
    assert.equal((await readdir(directory)).some(name => name.endsWith(".tmp")), false);
  } finally { await rm(directory, { recursive: true, force: true }); }
});

test("file exports retain invocation order across sessions and path aliases after a queued cancellation", async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-ordered-"));
  const path = join(directory, "result.pptx");
  const older = createSession(Format.Pptx);
  const newer = createSession(Format.Pptx);
  const pending = Promise.withResolvers<string>();
  function Delayed() { return view(use(pending.promise)); }
  try {
    await symlink(directory, join(directory, "alias"), process.platform === "win32" ? "junction" : "dir");
    await older.render(<Suspense fallback={view("Fallback")}><Delayed /></Suspense>);
    await newer.render(view("Newer export"));
    const first = older.exportFile(path, { overwrite: true });
    const controller = new AbortController();
    const cancelled = newer.exportFile(path, { overwrite: true, signal: controller.signal });
    controller.abort();
    await assert.rejects(cancelled, { code: ErrorCode.Cancelled });
    let finished = false;
    const last = newer.exportFile(join(directory, "alias", "result.pptx"), { overwrite: true }).then(result => { finished = true; return result; });
    await new Promise(resolve => setTimeout(resolve, 50));
    assert.equal(finished, false);
    await assert.rejects(readFile(path), { code: "ENOENT" });
    pending.resolve("Older export");
    await Promise.all([first, last]);
    const result = await processPptx("inspect", {}, await readFile(path), new Map(), newer.documentId, 0, new AbortController().signal);
    assert.match(result.model, /Newer export/);
    assert.doesNotMatch(result.model, /Older export|Fallback/);
    await older.render(<Presentation><Slide><Text style={{ fontSize: -1 }}>Invalid</Text></Slide></Presentation>);
    await assert.rejects(older.exportFile(path, { overwrite: true }));
    await newer.exportFile(path, { overwrite: true });
  } finally { pending.resolve("cleanup"); await older.dispose(); await newer.dispose(); await rm(directory, { recursive: true, force: true }); }
});


test("Windows publication protects case aliases of existing and removed imported sources", { skip: process.platform !== "win32" }, async () => {
  const directory = await mkdtemp(join(tmpdir(), "react-forge-case-"));
  const source = join(directory, "Source.pptx");
  try {
    await writeFile(source, "Imported bytes");
    const { fingerprint } = await readSource({ path: source }, 100);
    for (const removed of [false, true]) {
      if (removed) await rm(source);
      await assert.rejects(publish(Buffer.from("Replacement"), join(directory, "SOURCE.PPTX"), { overwrite: true, source: fingerprint }), { code: ErrorCode.UnsupportedEdit });
      if (!removed) assert.equal(await readFile(source, "utf8"), "Imported bytes");
    }
  } finally { await rm(directory, { recursive: true, force: true }); }
});

import assert from "node:assert/strict";
import test from "node:test";
import { inflateRawSync } from "node:zlib";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import React, { createRef, useState, type ReactNode } from "react";
import { createSession, importOffice, Format, ErrorCode, type NodeHandle, type Diagnostic, capabilities } from "../src/index.js";
import { Animation, Frame, Image, Layer, Pixel, PixelGrid, Rect, SpriteProject } from "../src/sprite.js";
import { connect } from "./mcp/client.js";

// Independent ZIP reader for these small generated fixtures, using central
// directory lengths so tests do not depend on local-header/data-descriptor mode.
function unpack(bytes: Buffer): Map<string, Buffer> {
  const end = bytes.lastIndexOf(Buffer.from([0x50, 0x4b, 0x05, 0x06]));
  assert.ok(end >= 0);
  const files = new Map<string, Buffer>();
  let cursor = bytes.readUInt32LE(end + 16);
  for (let i = 0; i < bytes.readUInt16LE(end + 10); i++) {
    assert.equal(bytes.readUInt32LE(cursor), 0x02014b50);
    const method = bytes.readUInt16LE(cursor + 10);
    const size = bytes.readUInt32LE(cursor + 20);
    const nameLength = bytes.readUInt16LE(cursor + 28);
    const local = bytes.readUInt32LE(cursor + 42);
    const start = local + 30 + bytes.readUInt16LE(local + 26) + bytes.readUInt16LE(local + 28);
    const name = bytes.subarray(cursor + 46, cursor + 46 + nameLength).toString();
    const data = bytes.subarray(start, start + size);
    files.set(name, method === 8 ? inflateRawSync(data) : data);
    cursor += 46 + nameLength + bytes.readUInt16LE(cursor + 30) + bytes.readUInt16LE(cursor + 32);
  }
  return files;
}
function meta(bytes: Buffer) { return JSON.parse(unpack(bytes).get("sprite.json")!.toString()); }
const scene = (children: ReactNode = <Pixel fill="#ff0000" />, durationMs = 100) => <SpriteProject width={4} height={4}>
  <Animation name="idle"><Frame durationMs={durationMs}>{children}</Frame></Animation>
</SpriteProject>;

test("sprite components use real React state, refs, deterministic exports and logical geometry", async () => {
  const session = createSession(Format.Sprite, { systemFonts: false });
  const frame = createRef<NodeHandle>(), pixel = createRef<NodeHandle>();
  const events: Diagnostic[] = [];
  session.subscribe(event => events.push(event));
  let update!: (next: string) => void;
  function Character() {
    const [color, setColor] = useState("#ff0000"); update = setColor;
    return <SpriteProject width={4} height={4} scale={2} columns={1} palette={{ R: color }}>
      <Animation name="idle"><Frame ref={frame} durationMs={120} pivot={{ x: 2, y: 4 }}>
        <Layer x={1} y={1}><PixelGrid ref={pixel} rows={["R.", ".R"]} /></Layer>
      </Frame></Animation>
    </SpriteProject>;
  }
  try {
    await session.render(<Character />);
    const first = await session.exportBuffer();
    const parts = unpack(first);
    assert.deepEqual([...parts.keys()].sort(), ["frames/0000.png", "sheet.png", "sprite.json"]);
    const sheet = parts.get("sheet.png")!;
    assert.equal(sheet.readUInt32BE(16), 10); assert.equal(sheet.readUInt32BE(20), 10);
    assert.deepEqual(meta(first).frames[0].pivot, { x: 4, y: 8 });
    assert.deepEqual(await session.measure(pixel.current!, { revision: session.revision }), { x: 1, y: 1, width: 2, height: 2, page: 0, coordinateSpace: "sprite_frame", revision: session.revision });
    assert.equal((await session.measure(frame.current!, { revision: session.revision })).width, 4);
    assert.deepEqual(await session.exportBuffer(), first);
    const oldRevision = session.revision;
    update("#00ff00");
    assert.notDeepEqual(await session.exportBuffer(), first);
    await assert.rejects(session.measure(pixel.current!, { revision: oldRevision }), { code: ErrorCode.Conflict });
    assert.ok(events.some(e => e.source === "native" && e.format === Format.Sprite && e.status === "completed"));
    assert.ok(!JSON.stringify(events).includes("#00ff00"));
    assert.equal(capabilities.formats.sprite.extension, ".sprite.zip");
    await assert.rejects(importOffice(Format.Sprite, first), { code: ErrorCode.UnsupportedPackage });
  } finally { await session.dispose(); }
});

test("registered sprite assets settle, belong to a session and reject missing or malformed bytes", async () => {
  const session = createSession(Format.Sprite), foreign = createSession(Format.Sprite);
  try {
    await session.render(scene());
    const source = unpack(await session.exportBuffer()).get("frames/0000.png")!;
    const asset = await session.registerImage(source);
    await session.render(scene(<Image asset={asset} width={4} height={4} source={{ x: 0, y: 0, width: 2, height: 2 }} flipX flipY />));
    assert.ok((await session.exportBuffer()).length);
    await foreign.render(scene(<Image asset={asset} width={4} height={4} />));
    await assert.rejects(foreign.exportBuffer(), { code: ErrorCode.InvalidTarget });
    await session.render(scene(<Image asset={{ documentId: session.documentId, assetId: "missing" }} width={4} height={4} />));
    await assert.rejects(session.exportBuffer(), { code: ErrorCode.InvalidTarget });
    const bad = await session.registerImage(Buffer.from("invalid image"));
    await session.render(scene(<Image asset={bad} width={4} height={4} />));
    await assert.rejects(session.exportBuffer(), { code: ErrorCode.MalformedInput });
  } finally { await session.dispose(); await foreign.dispose(); }
});

test("invalid latest sprite renders cannot publish stale or silently simplified output", async () => {
  const session = createSession(Format.Sprite);
  try {
    await session.render(scene()); await session.exportBuffer();
    const invalid = [
      scene(<Pixel fill="#ff0000">lost text</Pixel>),
      scene(<Rect width={1.5} height={2} fill="#ff0000" />),
      scene(<Rect width={2} height={2} fill="red" />),
      scene(<Pixel fill="#ff0000" {...{ typo: true }} />),
      scene(<Layer {...{ visible: null } as any}><Pixel fill="#ff0000" /></Layer>),
      scene(<Pixel x={Infinity} fill="#ff0000" />),
      scene(<PixelGrid rows={["X"]} />),
      scene(<Pixel fill="#ff0000" />, 0),
      <SpriteProject width={2} height={2}><Frame durationMs={1} /></SpriteProject>,
      <SpriteProject width={2} height={2}><Animation name="idle"><Frame durationMs={1} /></Animation><Animation name="idle"><Frame durationMs={1} /></Animation></SpriteProject>,
    ];
    for (const tree of invalid) { await session.render(tree); await assert.rejects(session.exportBuffer(), { code: ErrorCode.MalformedInput }); }
    await session.render(<SpriteProject width={4096} height={4096} scale={16}><Animation name="large"><Frame durationMs={1} /></Animation></SpriteProject>);
    await assert.rejects(session.exportBuffer(), { code: ErrorCode.ResourceLimit });
    await session.render(scene()); assert.ok((await session.exportBuffer()).length);
  } finally { await session.dispose(); }
});

test("sprite exports pin revisions and preserve existing output on invalid input or cancellation", async () => {
  const session = createSession(Format.Sprite);
  const directory = await mkdtemp(join(tmpdir(), "forge-sprite-"));
  const output = join(directory, "hero.sprite.zip");
  try {
    await session.render(scene(undefined, 123));
    const first = session.exportBuffer();
    while (session.revision === 0) await new Promise(resolve => setImmediate(resolve));
    await session.render(scene(undefined, 456));
    assert.equal(meta(await first).frames[0].duration, 123);
    assert.equal(meta(await session.exportBuffer()).frames[0].duration, 456);
    await session.exportFile(output);
    const original = await readFile(output);
    await assert.rejects(session.exportFile(output), { code: ErrorCode.Conflict });
    await session.render(scene(<Pixel fill="invalid" />));
    await assert.rejects(session.exportFile(output, { overwrite: true }), { code: ErrorCode.MalformedInput });
    assert.deepEqual(await readFile(output), original);
    await session.render(scene());
    await assert.rejects(session.exportFile(output, { overwrite: true, signal: AbortSignal.abort() }), { code: ErrorCode.Cancelled });
    assert.deepEqual(await readFile(output), original);
    await session.exportFile(output, { overwrite: true });
    assert.equal(meta(await readFile(output)).frames[0].duration, 100);
    assert.deepEqual(await readdir(directory), ["hero.sprite.zip"]);
  } finally { await session.dispose(); await rm(directory, { recursive: true, force: true }); }
});

test("running native sprite work cancels and the session remains usable", async () => {
  const session = createSession(Format.Sprite);
  const controller = new AbortController();
  try {
    await session.render(<SpriteProject width={2048} height={2048} padding={0}><Animation name="large">
      <Frame durationMs={100}>{Array.from({ length: 30 }, (_, i) => <Rect key={i} width={2048} height={2048} fill="#12345680" />)}</Frame>
    </Animation></SpriteProject>);
    const work = session.exportBuffer({ signal: controller.signal });
    const timer = setTimeout(() => controller.abort(), 30);
    try { await assert.rejects(work, { code: ErrorCode.Cancelled }); } finally { clearTimeout(timer); }
    await session.render(scene()); assert.ok((await session.exportBuffer()).length);
  } finally { await session.dispose(); }
});

test("MCP loads the sprite subpath, measures frames and enforces atomic bundle extensions", async () => {
  const directory = await mkdtemp(join(tmpdir(), "forge-sprite-mcp-"));
  const mcp = await connect(directory);
  try {
    const result = await mcp.call("execute", { code: `import {createSession,Format} from '@delino/react-forge'; import {SpriteProject,Animation,Frame,Pixel} from '@delino/react-forge/sprite'; export default async()=>{const s=createSession(Format.Sprite);await s.render(<SpriteProject width={2} height={2}><Animation name="idle"><Frame durationMs={100}><Pixel fill="#ff0000"/></Frame></Animation></SpriteProject>);return s;}` });
    const snapshot = await mcp.call("inspect", { sessionId: result.sessionId, kind: "frame" });
    const measured = await mcp.call("measure", { sessionId: result.sessionId, nodeId: snapshot.targets[0].nodeId, revision: snapshot.revision });
    assert.equal(measured.geometry.coordinateSpace, "sprite_frame");
    assert.ok((await mcp.raw("export", { sessionId: result.sessionId, output: "hero.png" })).isError);
    const published = await mcp.call("export", { sessionId: result.sessionId, output: "hero.sprite.zip" });
    assert.equal(published.published, true);
    assert.equal(meta(await readFile(join(directory, "hero.sprite.zip"))).frames.length, 1);
    await mcp.call("close", { sessionId: result.sessionId });
  } finally { await mcp.close(); await rm(directory, { recursive: true, force: true }); }
});


test("sprite layers accept the shared depth boundary and reject excess", async () => {
  const session = createSession(Format.Sprite);
  try {
    let drawing: ReactNode = <Pixel fill="#ffffff" />;
    for (let i = 0; i < 44; i++) drawing = <Layer>{drawing}</Layer>;
    await session.render(scene(drawing));
    assert.ok((await session.exportBuffer()).length);
    await session.render(scene(<Layer>{drawing}</Layer>));
    await assert.rejects(session.exportBuffer(), { code: ErrorCode.ResourceLimit });
  } finally { await session.dispose(); }
});

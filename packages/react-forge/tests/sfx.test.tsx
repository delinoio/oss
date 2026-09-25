import assert from "node:assert/strict";
import test from "node:test";
import React, { createRef, useState } from "react";
import { mkdtemp, readFile, readdir, rm, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { createSession, importOffice } from "../src/session.js";
import { Format, ErrorCode, type Diagnostic, type NodeHandle } from "../src/types.js";
import { Sound, Noise, Tone, Channels } from "../src/sfx.js";
import { capabilities } from "../src/capabilities.js";
import gunshot from "../examples/zombie-gunshot.js";

function pcm(bytes: Buffer): number[] {
  assert.equal(bytes.subarray(0, 4).toString(), "RIFF");
  assert.equal(bytes.subarray(8, 12).toString(), "WAVE");
  assert.equal(bytes.readUInt32LE(4), bytes.length - 8);
  assert.equal(bytes.readUInt32LE(40), bytes.length - 44);
  return Array.from({ length: (bytes.length - 44) / 2 }, (_, i) => bytes.readInt16LE(44 + i * 2));
}

test("zombie-game gunshot has a transient, decaying tail and clean silent ending", async () => {
  const session = await gunshot();
  const variation = await gunshot({ data: { seed: 816 } });
  try {
    const bytes = await session.exportBuffer();
    const samples = pcm(bytes);
    assert.equal(bytes.readUInt32LE(24), 48000); assert.equal(bytes.readUInt16LE(22), 1);
    assert.equal(samples.length, 36000); assert.equal(bytes.length, 72044);
    const rms = (start: number, end: number) => {
      const window = samples.slice(start * 48000, end * 48000);
      return Math.sqrt(window.reduce((sum, sample) => sum + sample * sample, 0) / window.length);
    };
    assert.ok(rms(0, 0.05) > 10 * rms(0.3, 0.55));
    assert.ok(rms(0.3, 0.55) > 0);
    assert.ok(samples.every(sample => Math.abs(sample) <= 31130));
    assert.equal(samples[0], 0); assert.ok(samples.slice(0.65 * 48000).every(sample => sample === 0));
    assert.deepEqual(bytes, await session.exportBuffer());
    assert.notDeepEqual(bytes, await variation.exportBuffer());
  } finally { await session.dispose(); await variation.dispose(); }
});

test("SFX reconciles React state, pins measured timelines, and emits redacted native diagnostics", async () => {
  const session = createSession(Format.Wav, { systemFonts: false });
  const ref = createRef<NodeHandle>();
  const events: Diagnostic[] = []; session.subscribe(event => events.push(event));
  let update!: (seed: number) => void;
  function Effect() {
    const [seed, setSeed] = useState(815); update = setSeed;
    return <Sound duration={0.3} channels={Channels.Stereo}><Noise ref={ref} start={0.05} duration={0.2} seed={seed} pan={-1} /></Sound>;
  }
  try {
    await session.render(<Effect />);
    const snapshot = await session.snapshot();
    const geometry = await session.measure(ref.current!, { revision: snapshot.revision });
    assert.equal(geometry.coordinateSpace, "timeline"); assert.equal(geometry.x, 0.05); assert.equal(geometry.width, 0.2);
    const first = await session.exportBuffer();
    assert.equal(first.readUInt16LE(22), 2); assert.equal(first.readUInt32LE(24), 48000);
    const samples = pcm(first);
    assert.equal(samples.length, 28800); assert.ok(samples.some(value => value !== 0));
    assert.ok(samples.every((sample, i) => i % 2 === 0 || sample === 0));
    assert.deepEqual(await session.exportBuffer(), first);
    update(816);
    const second = await session.exportBuffer();
    assert.notDeepEqual(second, first); assert.ok(session.revision > snapshot.revision);
    await assert.rejects(session.measure(ref.current!, { revision: snapshot.revision }), { code: ErrorCode.Conflict });
    assert.ok(events.some(event => event.source === "native" && event.status === "completed" && event.format === Format.Wav));
    assert.equal(capabilities.formats.wav.bitsPerSample, 16);
    assert.equal(capabilities.formats.wav.import, false);
  } finally { await session.dispose(); }
});

test("SFX rejects invalid latest props, nesting, limits and assets without replacing a valid output", async () => {
  const session = createSession(Format.Wav);
  const directory = await mkdtemp(join(tmpdir(), "forge-sfx-"));
  const output = join(directory, "shot.wav");
  const valid = <Sound duration={0.3}><Noise seed={815} duration={0.2} /></Sound>;
  try {
    await session.render(valid); await session.exportFile(output);
    const before = await readFile(output);
    for (const tree of [
      <Sound duration={NaN}><Tone duration={0.1} frequency={100} /></Sound>,
      <Sound duration={0.3}><Noise duration={0.2} seed={0} /></Sound>,
      <Sound duration={0.3}><Tone duration={0.2} start={0.2} frequency={100} /></Sound>,
      <Sound duration={0.3}><Tone duration={0.2} frequency={100} attack={0} /></Sound>,
      <Sound duration={0.3}><Tone duration={0.2} frequency={100} pan={1} /></Sound>,
      <Sound duration={0.3}><Noise duration={0.2} seed={1} highPass={1000} lowPass={100} /></Sound>,
      React.createElement("sfx:sound", { duration: 0.3, unsupported: true }, <Noise seed={1} duration={0.2} />),
      React.createElement("sfx:sound", { duration: 0.3, channels: null }, <Noise seed={1} duration={0.2} />),
      <Sound duration={0.3}>{React.createElement("sfx:noise", { duration: 0.2, seed: 1 }, "unsupported")}</Sound>,
    ]) {
      await session.render(tree);
      await assert.rejects(session.exportFile(output, { overwrite: true }), { code: ErrorCode.MalformedInput });
      assert.deepEqual(await readFile(output), before);
    }
    await session.render(<Sound duration={0.3}>{Array.from({ length: 257 }, (_, i) => <Noise key={i} duration={0.01} seed={i + 1} />)}</Sound>);
    await assert.rejects(session.exportBuffer(), { code: ErrorCode.ResourceLimit });
    await session.render(<Sound duration={30}>{Array.from({ length: 12 }, (_, i) => <Noise key={i} duration={30} seed={i + 1} />)}</Sound>);
    await assert.rejects(session.exportBuffer(), { code: ErrorCode.ResourceLimit });
    await assert.rejects(session.registerImage(new Uint8Array([1])), { code: ErrorCode.UnsupportedEdit });
    await assert.rejects(importOffice(Format.Wav, before), { code: ErrorCode.UnsupportedPackage });
    await session.render(valid);
    await assert.rejects(session.exportFile(output), { code: ErrorCode.Conflict });
    await session.exportFile(output, { overwrite: true });
    assert.deepEqual(await readdir(directory), ["shot.wav"]);
  } finally { await session.dispose(); await rm(directory, { recursive: true, force: true }); }
});

test("cancelling SFX synthesis preserves the previous output and disposal closes the session", async () => {
  const session = createSession(Format.Wav);
  const directory = await mkdtemp(join(tmpdir(), "forge-sfx-cancel-"));
  const output = join(directory, "shot.wav");
  try {
    await writeFile(output, "previous");
    await session.render(<Sound duration={30}>{Array.from({ length: 10 }, (_, i) => <Noise key={i} duration={30} seed={i + 1} />)}</Sound>);
    const controller = new AbortController();
    const operation = session.exportFile(output, { overwrite: true, signal: controller.signal });
    setImmediate(() => controller.abort());
    await assert.rejects(operation, { code: ErrorCode.Cancelled });
    assert.equal(await readFile(output, "utf8"), "previous");
    assert.deepEqual(await readdir(directory), ["shot.wav"]);
    await session.dispose();
    assert.throws(() => session.exportBuffer(), { code: ErrorCode.Disposed });
  } finally { await session.dispose(); await rm(directory, { recursive: true, force: true }); }
});

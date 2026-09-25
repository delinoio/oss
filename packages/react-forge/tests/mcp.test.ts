import assert from "node:assert/strict";
import { mkdtemp, readFile, rm, writeFile, mkdir } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";
import { connect } from "./mcp/client.js";

const pdf = `import {createSession, Format} from '@delino/react-forge';
import {Document, Page, Text} from '@delino/react-forge/pdf';
export default async ({state}) => {
 const session=createSession(Format.Pdf);
 state.set('render', async text => session.render(<Document language="en-US"><Page><Text>{text}</Text></Page></Document>));
 await state.get('render')('Hello MCP'); return session;
};`;

test("stdio tools keep a session across inline calls and export an exact inspected revision", async () => {
  const cwd = await mkdtemp(join(tmpdir(), "react-forge-mcp-"));
  const peer = await connect(cwd);
  try {
    const tools = await peer.client.listTools();
    assert.equal(tools.tools.length, 9);
    const capabilities = await peer.call("capabilities");
    assert.equal(capabilities.mcp.transport, "stdio");
    const { sessionId } = await peer.call("execute", { code: pdf });
    const inspected = await peer.call("inspect", { sessionId });
    assert.ok(inspected.revision > 0);
    assert.ok(inspected.targets.length > 0);
    const text = inspected.targets.find((target: any) => target.kind === "paragraph");
    assert.ok(text);
    const measured = await peer.call("measure", { sessionId, nodeId: text.nodeId, revision: inspected.revision });
    assert.equal(measured.geometry.coordinateSpace, "page");
    await peer.call("execute", { sessionId, code: "export default async ({state}) => { await state.get('render')('Updated MCP'); };" });
    const next = await peer.call("inspect", { sessionId });
    assert.ok(next.revision > inspected.revision);
    const exported = await peer.call("export", { sessionId, output: "report.pdf" });
    assert.equal(exported.revision, next.revision);
    assert.equal((await readFile(join(cwd, "report.pdf"))).subarray(0, 5).toString(), "%PDF-");
    assert.equal((await peer.call("sessions")).sessions.length, 1);
    await peer.call("close", { sessionId });
    assert.equal((await peer.call("sessions")).sessions.length, 0);
  } finally { await peer.close(); await rm(cwd, { recursive: true, force: true }); }
});

async function fixture(work: (peer: Awaited<ReturnType<typeof connect>>, cwd: string) => Promise<void>) {
  const cwd = await mkdtemp(join(tmpdir(), "react-forge-mcp-"));
  const peer = await connect(cwd);
  try { await work(peer, cwd); }
  finally { await peer.close(); await rm(cwd, { recursive: true, force: true }); }
}
async function errorCode(peer: Awaited<ReturnType<typeof connect>>, tool: string, args: Record<string, unknown>, code: string) {
  const result = await peer.raw(tool, args);
  assert.equal(result.isError, true, JSON.stringify(result));
  assert.equal((result.structuredContent!.error as any).code, code);
  return result.structuredContent!;
}
async function waitFor(path: string) {
  for (let attempt = 0; attempt < 500; attempt++) {
    try { await readFile(path); return; } catch { await new Promise(resolve => setTimeout(resolve, 10)); }
  }
  assert.fail("Fixture did not become ready");
}

test("inline SFX shares the engine, measures time, updates state and exports PCM WAV", async () => fixture(async (peer, cwd) => {
  const created = await peer.call("execute", { code: `import {createSession,Format} from '@delino/react-forge';
    import {Sound,Noise} from '@delino/react-forge/sfx';
    export default async ({state})=>{const s=createSession(Format.Wav);
      state.set('render',seed=>s.render(<Sound duration={0.25}><Noise start={0.05} duration={0.15} seed={seed}/></Sound>));
      await state.get('render')(815);return s;};` });
  const args = { sessionId: created.sessionId };
  const snapshot = await peer.call("inspect", args);
  const layer = snapshot.targets.find((target: any) => target.kind === "noise");
  const measured = await peer.call("measure", { ...args, nodeId: layer.nodeId, revision: snapshot.revision });
  assert.equal(measured.geometry.coordinateSpace, "timeline"); assert.equal(measured.geometry.x, 0.05);
  await errorCode(peer, "export", { ...args, output: "shot.mp3" }, "malformed_input");
  await peer.call("export", { ...args, output: "shot.wav" });
  const before = await readFile(join(cwd, "shot.wav"));
  assert.equal(before.subarray(8, 12).toString(), "WAVE"); assert.equal(before.readUInt32LE(24), 48000);
  assert.equal(before.length, 24044);
  await peer.call("execute", { ...args, code: "export default async({state})=>{await state.get('render')(816);};" });
  await errorCode(peer, "measure", { ...args, nodeId: layer.nodeId, revision: snapshot.revision }, "conflict");
  await peer.call("export", { ...args, output: "shot.wav", overwrite: true });
  assert.notDeepEqual(await readFile(join(cwd, "shot.wav")), before);
  await peer.call("close", args);
}));

const simple = {
  pptx: { imports: "Presentation,Slide,Column,Text", view: '<Presentation><Slide><Column><Text>Original</Text></Column></Slide></Presentation>', kind: "text", edit: '<Text>{data}</Text>' },
  docx: { imports: "Document,Section,Paragraph", view: '<Document><Section><Paragraph>Original</Paragraph></Section></Document>', kind: "paragraph", edit: '<Paragraph>{data}</Paragraph>' },
  xlsx: { imports: "Workbook,Worksheet,Cell", view: '<Workbook><Worksheet name="Report"><Cell address={{row:0,column:0}} value="Original" /></Worksheet></Workbook>', kind: "cell", edit: '<Cell value={data} />' },
};

test("all Office formats import, retain mounted state and protect their original source", async () => fixture(async (peer, cwd) => {
  for (const [format, spec] of Object.entries(simple)) {
    const sourceCode = `import {createSession} from '@delino/react-forge'; import {${spec.imports}} from '@delino/react-forge/${format}';
      export default async ()=>{const s=createSession('${format}'); await s.render(${spec.view});return s;}`;
    const original = await peer.call("execute", { code: sourceCode });
    await peer.call("export", { sessionId: original.sessionId, output: `original.${format}` });
    const bytes = await readFile(join(cwd, `original.${format}`));
    const opened = await peer.call("execute", { code: `import {importOffice} from '@delino/react-forge'; export default ({signal})=>importOffice('${format}',{path:'original.${format}'},{signal});` });
    const inspection = await peer.call("inspect", { sessionId: opened.sessionId, kind: spec.kind });
    const target = inspection.targets.find((item: any) => item.editable);
    assert.ok(target, JSON.stringify(inspection));
    await peer.call("execute", { sessionId: opened.sessionId, data: target.nodeId, code: `import {${spec.imports}} from '@delino/react-forge/${format}';
      export default async ({session,state,data})=>{const target=session.inspect().targets.find(t=>t.nodeId===data);data='First edit';state.set('region',await session.mount(target,${spec.edit}));};` });
    await peer.call("execute", { sessionId: opened.sessionId, data: "Second edit", code: `import {${spec.imports}} from '@delino/react-forge/${format}'; export default async ({state,data})=>{await state.get('region').render(${spec.edit});};` });
    await errorCode(peer, "export", { sessionId: opened.sessionId, output: `original.${format}`, overwrite: true }, "unsupported_edit");
    await peer.call("export", { sessionId: opened.sessionId, output: `edited.${format}` });
    assert.deepEqual(await readFile(join(cwd, `original.${format}`)), bytes);
    assert.notDeepEqual(await readFile(join(cwd, `edited.${format}`)), bytes);
    await errorCode(peer, "export", { sessionId: opened.sessionId, output: `edited.${format}` }, "conflict");
    await peer.call("export", { sessionId: opened.sessionId, output: `edited.${format}`, overwrite: true });
    await peer.call("close", { sessionId: opened.sessionId });
    await peer.call("close", { sessionId: original.sessionId });
  }
}));

test("entries reload, dependencies share identities and caller output never reaches MCP or diagnostics", async () => fixture(async (peer, cwd) => {
  await mkdir(join(cwd, "tasks space #"));
  await writeFile(join(cwd, "helper.ts"), `export const identity={}; export const label='relative helper';`);
  await writeFile(join(cwd, "tasks space #", "entry.tsx"), `import {identity} from '../helper.js'; import * as engine from '@delino/react-forge';
    export default ({state})=>{state.set('identity',identity);state.set('engine',engine); return engine.createSession(engine.Format.Pdf);};`);
  const { sessionId } = await peer.call("execute", { entry: "tasks space #/entry.tsx" });
  await writeFile(join(cwd, "tasks space #", "entry.tsx"), `import {identity} from '../helper.js'; import * as engine from '@delino/react-forge';
    export default ({session,state})=>{if(identity!==state.get('identity')||engine!==state.get('engine'))throw Error('duplicate modules');return session;};`);
  await peer.call("execute", { sessionId, entry: "tasks space #/entry.tsx" });
  await peer.call("execute", { sessionId, code: `import {identity} from './helper.js'; import {writeSync} from 'node:fs';
    console.log('PRIVATE-CONSOLE');console.error('PRIVATE-STDERR');writeSync(1,'PRIVATE-RAW-STDOUT');writeSync(2,'PRIVATE-RAW-STDERR');
    export default ({state})=>{if(identity!==state.get('identity'))throw Error('duplicate inline helper');};` });
  const invalid = await errorCode(peer, "execute", { sessionId, code: "export default ()=>{throw Error('PRIVATE-FAILURE');};" }, "render");
  assert.ok(!JSON.stringify(invalid).includes("PRIVATE"));
  await peer.call("close", { sessionId });
  assert.ok(!peer.stderr().includes("PRIVATE"));
  assert.ok(!peer.stderr().includes(cwd));
  for (const line of peer.stderr().trim().split("\n").filter(Boolean)) assert.equal(JSON.parse(line).source, "mcp");
}));

test("tool validation, filtered pagination, stale measurement and invalid latest render stay recoverable", async () => fixture(async peer => {
  await errorCode(peer, "execute", { code: pdf, entry: "both.tsx" }, "malformed_input");
  await errorCode(peer, "execute", { code: "export default ()=>{};", extra: true }, "malformed_input");
  await errorCode(peer, "execute", { code: "export default <" }, "malformed_input");
  await errorCode(peer, "execute", { code: "export default 1" }, "malformed_input");
  const { sessionId } = await peer.call("execute", { code: pdf });
  const first = await peer.call("inspect", { sessionId, limit: 1 });
  assert.equal(first.targets.length, 1); assert.equal(first.truncated, true); assert.equal(first.nextOffset, 1);
  const second = await peer.call("inspect", { sessionId, limit: 1, offset: first.nextOffset });
  assert.notEqual(first.targets[0].nodeId, second.targets[0].nodeId);
  const paragraph = await peer.call("inspect", { sessionId, kind: "paragraph" });
  await peer.call("execute", { sessionId, code: "export default async ({state})=>{await state.get('render')('Changed');};" });
  await errorCode(peer, "measure", { sessionId, nodeId: paragraph.targets[0].nodeId, revision: first.revision }, "conflict");
  await errorCode(peer, "publish", { sessionId }, "unsupported_edit");
  await errorCode(peer, "execute", { sessionId, code: "import {createSession,Format} from '@delino/react-forge';export default ()=>createSession(Format.Pdf);" }, "invalid_target");
  assert.equal((await peer.call("sessions")).total, 1);
  await peer.call("execute", { sessionId, code: "import {Document,Page,Image,Text} from '@delino/react-forge/pdf';export default async({session})=>{await session.render(<Document language='en-US'><Page><Image><Text>Invalid children</Text></Image></Page></Document>);};" });
  await errorCode(peer, "export", { sessionId, output: "invalid.pdf" }, "malformed_input");
  await peer.call("execute", { sessionId, code: "export default async ({state})=>{await state.get('render')('Recovered');};" });
  await peer.call("export", { sessionId, output: "recovered.pdf" });
  await peer.call("close", { sessionId });
  await errorCode(peer, "inspect", { sessionId }, "invalid_target");
}));

test("cancelled callbacks retain their session queue until they actually finish", async () => fixture(async (peer, cwd) => {
  const { sessionId } = await peer.call("execute", { code: pdf });
  const controller = new AbortController();
  const running = peer.raw("execute", { sessionId, code: `import {writeFile} from 'node:fs/promises'; export default async({state,signal})=>{
    await writeFile('started','ready');await new Promise(resolve=>signal.addEventListener('abort',resolve,{once:true}));
    await new Promise(resolve=>setTimeout(resolve,100));state.set('finished',true);
  };` }, controller.signal);
  const rejected = assert.rejects(running);
  await waitFor(join(cwd, "started"));
  controller.abort();
  const next = await peer.call("execute", { sessionId, code: "export default ({state})=>{if(!state.get('finished'))throw Error('queue released before callback completed');};" });
  assert.equal(next.sessionId, sessionId);
  await rejected;
  await peer.call("export", { sessionId, output: "after-cancel.pdf" });
}));

test("cancelled creation disposes a late returned session instead of registering it", async () => fixture(async (peer, cwd) => {
  const controller = new AbortController();
  const running = peer.raw("execute", { code: `import {createSession,Format} from '@delino/react-forge';import {writeFile} from 'node:fs/promises';
    export default async({signal})=>{const session=createSession(Format.Pdf);const dispose=session.dispose.bind(session);session.dispose=async()=>{await dispose();await writeFile('disposed','yes');};
      await writeFile('started','yes');await new Promise(resolve=>signal.addEventListener('abort',resolve,{once:true}));return session;};` }, controller.signal);
  const rejected = assert.rejects(running);
  await waitFor(join(cwd, "started"));controller.abort();await rejected;
  await waitFor(join(cwd, "disposed"));
  assert.equal((await peer.call("sessions")).total, 0);
}));

async function figmaFixture(peer: Awaited<ReturnType<typeof connect>>, cwd: string) {
  const raw = await readFile(new URL("./figma/fake.ts", import.meta.url), "utf8");
  await writeFile(join(cwd, "fake.ts"), raw.replace(/import[^;]+;/, "const CallSafety={Read:'read',Write:'write'};"));
  return peer.call("execute", { code: `import {FigmaSession} from '@delino/react-forge';import {Document,Page,Text} from '@delino/react-forge/figma';import {FakeConnection} from './fake.js';
    export default async({state})=>{const connection=new FakeConnection();state.set('connection',connection);const session=new FigmaSession({fileName:'MCP fixture',planKey:'team::1'},connection);
      state.set('render',async text=>session.render(<Document><Page name='Screens'><Text name='Title'>{text}</Text></Page></Document>));
      await state.get('render')('PRIVATE-DESIGN');return session;};` });
}

test("Figma publish, receipt, refresh and no-op publication preserve the native outcome", async () => fixture(async (peer, cwd) => {
  const { sessionId } = await figmaFixture(peer, cwd);
  await writeFile(join(cwd, "result.figma.json"), "existing");
  await errorCode(peer, "publish", { sessionId, receiptPath: "result.figma.json" }, "conflict");
  await peer.call("execute", { sessionId, code: "export default({state})=>{if(state.get('connection').writes!==0)throw Error('published before checking output');};" });
  const published = await peer.call("publish", { sessionId, receiptPath: "result.figma.json", overwrite: true });
  assert.equal(published.receipt.status, "complete");
  assert.ok(published.receipt.fileKey);
  assert.ok(!(await readFile(join(cwd, "result.figma.json"), "utf8")).includes("PRIVATE-DESIGN"));
  const receipt = await peer.call("inspect", { sessionId, view: "receipt" });
  assert.equal(receipt.receipt.revision, published.receipt.revision);
  await peer.call("execute", { sessionId, code: "export default({state})=>{state.set('writes',state.get('connection').writes);};" });
  await peer.call("publish", { sessionId });
  await peer.call("execute", { sessionId, code: "export default({state})=>{if(state.get('writes')!==state.get('connection').writes)throw Error('duplicate publication');};" });
  const targets = await peer.call("inspect", { sessionId });
  const page = targets.targets.find((target: any) => target.kind === "PAGE");
  assert.ok(page);
  await peer.call("refresh", { sessionId, pageId: page.remoteId });
  const refreshed = await peer.call("inspect", { sessionId, kind: "TEXT" });
  assert.ok(refreshed.targets.length);
  await peer.call("measure", { sessionId, nodeId: refreshed.targets[0].nodeId, revision: published.receipt.revision });
  await errorCode(peer, "export", { sessionId, output: "file.pdf" }, "unsupported_edit");
}));

test("Figma partial and unknown outcomes retain receipts without automatic retry", async () => fixture(async (peer, cwd) => {
  const partial = await figmaFixture(peer, cwd);
  await peer.call("execute", { sessionId: partial.sessionId, code: "export default({state})=>{state.get('connection').canvas.failProperty='fontSize';};" });
  const result = await errorCode(peer, "publish", { sessionId: partial.sessionId }, "remote");
  assert.equal((result.receipt as any).status, "partial");
  assert.deepEqual((await peer.call("inspect", { sessionId: partial.sessionId, view: "receipt" })).receipt, result.receipt);
  const completed = await peer.call("publish", { sessionId: partial.sessionId });
  assert.equal(completed.receipt.status, "complete");
  await peer.call("execute", { sessionId: partial.sessionId, code: "export default({state})=>{const titles=[...state.get('connection').canvas.nodes.values()].filter(n=>n.name==='Title');if(titles.length!==1)throw Error('duplicate node');};" });
  const unknown = await figmaFixture(peer, cwd);
  await peer.call("execute", { sessionId: unknown.sessionId, code: "export default({state})=>{state.get('connection').loseResponse=true;};" });
  const lost = await errorCode(peer, "publish", { sessionId: unknown.sessionId }, "unknown_outcome");
  assert.equal((lost.receipt as any).status, "unknown");
  await peer.call("execute", { sessionId: unknown.sessionId, code: "export default({state})=>{state.set('writes',state.get('connection').writes);};" });
  await errorCode(peer, "publish", { sessionId: unknown.sessionId }, "unknown_outcome");
  await peer.call("execute", { sessionId: unknown.sessionId, code: "export default({state})=>{if(state.get('writes')!==state.get('connection').writes)throw Error('blind retry');};" });
}));

test("React hook state and pending Suspense work survive across MCP calls", async () => fixture(async peer => {
  const { sessionId } = await peer.call("execute", { code: `import {useState,use,Suspense} from 'react';import {createSession,Format} from '@delino/react-forge';import {Document,Page,Text} from '@delino/react-forge/pdf';
    export default async({state})=>{const session=createSession(Format.Pdf);const ready=new Promise(resolve=>setTimeout(()=>resolve('ready'),80));
      function Body(){const label=use(ready);const [count,setCount]=useState(0);state.set('bump',()=>setCount(value=>value+1));state.set('count',count);return <Text>{label} {count}</Text>;}
      await session.render(<Document language='en-US'><Page><Suspense fallback={<Text>Pending</Text>}><Body /></Suspense></Page></Document>);return session;};` });
  const initial = await peer.call("inspect", { sessionId });
  await peer.call("execute", { sessionId, code: "export default({state})=>{if(state.get('count')!==0)throw Error('Suspense not settled');state.get('bump')();};" });
  const updated = await peer.call("inspect", { sessionId });
  assert.ok(updated.revision > initial.revision);
  await peer.call("execute", { sessionId, code: "export default({state})=>{if(state.get('count')!==1)throw Error('State lost');state.get('bump')();};" });
  await peer.call("inspect", { sessionId });
  await peer.call("execute", { sessionId, code: "export default({state})=>{if(state.get('count')!==2)throw Error('State lost on repeated calls');};" });
}));

test("source plus JSON data is bounded for both inline and file inputs", async () => fixture(async (peer, cwd) => {
  const limit = 16 * 1024 * 1024;
  await errorCode(peer, "execute", { code: "export default()=>{};", data: "x".repeat(limit) }, "resource_limit");
  await writeFile(join(cwd, "large.tsx"), " ".repeat(limit));
  await errorCode(peer, "execute", { entry: "large.tsx", data: { value: "beyond source limit" } }, "resource_limit");
  assert.equal((await peer.call("sessions")).total, 0);
}));

test("connection closure disposes sessions without forwarding task cleanup output", async () => fixture(async (peer, cwd) => {
  await peer.call("execute", { code: `import {createSession,Format} from '@delino/react-forge';import {writeFile} from 'node:fs/promises';
    export default()=>{const session=createSession(Format.Pdf);const dispose=session.dispose.bind(session);session.dispose=async()=>{await dispose();console.log('PRIVATE-CLEANUP');await writeFile('disposed','yes');};return session;};` });
  await peer.close();
  await waitFor(join(cwd, "disposed"));
  assert.ok(!peer.stderr().includes("PRIVATE-CLEANUP"));
}));

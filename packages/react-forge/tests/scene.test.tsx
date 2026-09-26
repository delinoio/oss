import { fileURLToPath } from "node:url";
import { test } from "node:test";
import assert from "node:assert/strict";
import React, { createElement, createRef, Suspense, use, useLayoutEffect, useState } from "react";
import { mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { createRequire } from "node:module";
import { createSession, Format, ErrorCode, Stage, type Diagnostic, type NodeHandle } from "../src/index.js";
import { Scene, Group, Mesh, PointLight, PerspectiveCamera, AlphaMode, type GeometryInput } from "../src/glb.js";
const sceneTest = process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const validator = createRequire(import.meta.url)("gltf-validator") as { validateBytes(bytes: Uint8Array): Promise<{ issues: { numErrors: number; messages: unknown[] } }> };
const geometry = (): GeometryInput => ({ positions: new Float32Array([0,0,0,1,0,0,0,1,0]), normals: new Float32Array([0,0,1,0,0,1,0,0,1]), indices: new Uint32Array([0,1,2]), uv: new Float32Array([0,0,1,0,0,1]), tangents: new Float32Array([1,0,0,1,1,0,0,1,1,0,0,1]) });
const decode = (b: Buffer) => JSON.parse(b.subarray(20,20+b.readUInt32LE(12)).toString());
for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(`${format}: concurrent renders commit in order around an intervening export`, async () => {
    const session = createSession(format);
    const g = await session.registerGeometry(geometry());
    const commits: string[] = [];
    function Model({ name, x }: { name: string; x: number }) {
      useLayoutEffect(() => { commits.push(name); }, [name]);
      return <Scene><Mesh geometry={g} name={name} translation={[x, 0, 0]}/></Scene>;
    }
    try {
      const first = session.render(<Model name="first" x={0}/>);
      const intermediate = session.export();
      const second = session.render(<Model name="second" x={5}/>);
      const [, bytes] = await Promise.all([first, intermediate, second]);
      assert.deepEqual(commits, ["first", "second"]);
      const latest = await session.export();
      assert.notDeepEqual(bytes, latest);
      if (format === Format.Glb) {
        assert.equal(decode(bytes).nodes[1].name, "first");
        assert.equal(decode(latest).nodes[1].name, "second");
      }
    } finally { await session.dispose(); }
  });
  for (const contended of [false, true]) {
    sceneTest(`${format}: file export pins its invocation position with directory contention=${contended}`, async () => {
      const session = createSession(format), blocker = createSession(format);
      const directory = await mkdtemp(join(tmpdir(), "forge-scene-file-order-"));
      let release!: () => void;
      const pending = new Promise<void>(resolve => { release = resolve; });
      function Pending() { use(pending); return null; }
      try {
        const g = await session.registerGeometry(geometry());
        await session.render(<Scene><Mesh geometry={g} name="first"/></Scene>);
        const expected = await session.export();
        const revision = session.revision;
        await blocker.render(<Scene><Suspense fallback={null}><Pending/></Suspense></Scene>);
        const blocking = contended ? blocker.exportFile(join(directory, `blocker.${format}`)) : Promise.resolve();
        const path = join(directory, `scene.${format}`);
        const exported = session.exportFile(path);
        const rendered = session.render(<Scene><Mesh geometry={g} name="second" translation={[5, 0, 0]}/></Scene>);
        release();
        const [result] = await Promise.all([exported, rendered, blocking]);
        assert.equal(result.revision, revision);
        assert.deepEqual(await readFile(path), expected);
        assert.notDeepEqual(await session.export(), expected);
      } finally {
        release();
        await Promise.all([session.dispose(), blocker.dispose()]);
        await rm(directory, { recursive: true, force: true });
      }
    });
  }
  sceneTest(`${format}: failed file reservations retain the preceding render's queue slot`, async () => {
    const session = createSession(format);
    const directory = await mkdtemp(join(tmpdir(), "forge-scene-reservation-failure-"));
    const commits: string[] = [];
    function Model({ name }: { name: string }) {
      useLayoutEffect(() => { commits.push(name); }, [name]);
      return <Scene name={name}/>;
    }
    try {
      const first = session.render(<Model name="first"/>);
      const failed = assert.rejects(session.exportFile(join(directory, "missing", `scene.${format}`)), { code: ErrorCode.Io });
      const last = session.render(<Model name="last"/>);
      await Promise.all([first, failed, last]);
      assert.deepEqual(commits, ["first", "last"]);
      assert.ok((await session.export()).length > 0);
    } finally {
      await session.dispose();
      await rm(directory, { recursive: true, force: true });
    }
  });
  sceneTest(`${format}: a queued recovery does not hide an earlier render failure`, async () => {
    const session = createSession(format);
    const g = await session.registerGeometry(geometry());
    function Broken(): React.ReactNode { throw Error("Synthetic queued render failure"); }
    try {
      const failed = assert.rejects(session.render(<Broken/>), { code: ErrorCode.Render });
      const blocked = assert.rejects(session.export(), { code: ErrorCode.Render });
      const recovered = session.render(<Scene><Mesh geometry={g}/></Scene>);
      await Promise.all([failed, blocked, recovered]);
      assert.ok((await session.export()).length > 0);
    } finally { await session.dispose(); }
  });
  sceneTest(`${format}: native measurement and export diagnostics retain their stages`, async () => {
    const session = createSession(format);
    const events: Diagnostic[] = [];
    session.onDiagnostic(event => events.push(event));
    try {
      const mesh = geometry(); delete mesh.uv;
      const registered = await session.registerGeometry(mesh);
      await session.render(<Scene><Mesh geometry={registered}/></Scene>);
      const snapshot = await session.snapshot();
      const target = snapshot.targets.find(target => target.kind === "mesh")!;
      events.length = 0;
      await session.measure(target, { revision: snapshot.revision });
      assert.deepEqual(events.filter(event => event.source === "native").map(event => [event.stage, event.status]),
        [[Stage.Layout, "started"], [Stage.Layout, "completed"]]);
      assert.ok(events.every(event => event.stage === Stage.Layout && event.revision === snapshot.revision));
      events.length = 0;
      await session.export();
      assert.deepEqual(events.filter(event => event.source === "native").map(event => [event.stage, event.status]),
        [[Stage.Export, "started"], [Stage.Export, "completed"]]);

      const texture = await session.registerTexture({ path: fileURLToPath(new URL("../examples/sample.png", import.meta.url)) });
      await session.render(<Scene><Mesh geometry={registered} material={{ baseColorTexture: texture }}/></Scene>);
      const invalid = await session.snapshot();
      events.length = 0;
      await assert.rejects(session.measure(invalid.targets.find(target => target.kind === "mesh")!, { revision: invalid.revision }),
        { code: ErrorCode.MalformedInput, context: { stage: Stage.Layout, format, revision: invalid.revision, location: "geometry/uv" } });
      assert.deepEqual(events.filter(event => event.source === "native").map(event => [event.stage, event.status]),
        [[Stage.Layout, "started"], [Stage.Layout, "failed"]]);
    } finally { await session.dispose(); }
  });
  sceneTest(`${format}: native scene, immutable geometry, world bounds and atomic output`, async () => {
    const s = createSession(format); const dir = await mkdtemp(join(tmpdir(),"forge-scene-"));
    try {
      assert.throws(()=>s.registerGeometry(null as never),{code:ErrorCode.MalformedInput});
      assert.throws(()=>s.registerTexture(null as never),{code:ErrorCode.MalformedInput});
      const input = geometry(); const registered = s.registerGeometry(input); input.positions.fill(9); const g = await registered;
      const ref = createRef<NodeHandle>();
      await s.render(<Scene><Group translation={[2,3,4]} scale={[-2,3,1]} ref={ref}><Mesh geometry={g} material={{ metallic: 0.8, roughness: 0.3 }} /></Group><PerspectiveCamera yfov={0.8}/><PointLight intensity={50}/></Scene>);
      const snapshot = await s.snapshot(); const bounds = await s.measure(ref.current!, { revision: snapshot.revision });
      assert.equal(bounds.coordinateSpace,"world"); assert.deepEqual(bounds.min,[0,3,4]); assert.deepEqual(bounds.max,[2,6,4]);
      const buffer = await s.export();
      if (format === Format.Glb) { const result = await validator.validateBytes(buffer); assert.equal(result.issues.numErrors,0,JSON.stringify(result.issues.messages)); assert.equal(decode(buffer).extensions.KHR_lights_punctual.lights.length,1); }
      else { assert.equal(buffer.subarray(0,18).toString(),"Kaydara FBX Binary"); assert.equal(buffer.readUInt32LE(23),7400); }
      const file = join(dir,`scene.${format}`); const result = await s.exportFile(file); assert.equal(result.revision,snapshot.revision); assert.deepEqual(await readFile(file),buffer);
      await assert.rejects(s.exportFile(file), { code: ErrorCode.Conflict });
      await s.render(<Scene><Mesh geometry={g}/></Scene>); await assert.rejects(s.measure(ref.current ?? snapshot.targets[1]!,{revision:snapshot.revision}), {code:ErrorCode.Conflict});
      assert.equal((await readdir(dir)).length,1);
    } finally { await s.dispose(); await rm(dir,{recursive:true,force:true}); }
  });
  sceneTest(`${format}: rejects invalid arrays, references, properties and texture requirements`, async () => {
    const s=createSession(format), other=createSession(format);
    try {
      const bad=geometry();bad.indices[2]=3;await assert.rejects(s.registerGeometry(bad),{code:ErrorCode.MalformedInput});
      const nan=geometry();nan.positions[0]=NaN;await assert.rejects(s.registerGeometry(nan),{code:ErrorCode.MalformedInput});
      const g=await s.registerGeometry(geometry());const foreign=await other.registerGeometry(geometry());
      const texture = await s.registerTexture({path:fileURLToPath(new URL('../examples/sample.png',import.meta.url))});
      const withoutUv=geometry();delete withoutUv.uv; const noUv=await s.registerGeometry(withoutUv);
      await s.render(<Scene><Mesh geometry={noUv} material={{baseColorTexture:texture}}/></Scene>);await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
      const withoutTangents=geometry();delete withoutTangents.tangents;const noTangents=await s.registerGeometry(withoutTangents);
      await s.render(<Scene><Mesh geometry={noTangents} material={{normalTexture:texture}}/></Scene>);await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
      await s.render(<Scene><Mesh geometry={foreign} material={{baseColorTexture:texture}}/></Scene>);await assert.rejects(s.export(),{code:ErrorCode.InvalidTarget});
      await s.render(<Scene><Mesh geometry={g} material={{baseColorTexture:texture,metallicTexture:texture,roughnessTexture:texture,normalTexture:texture,emissiveTexture:texture,emissive:[.2,.3,.4],alphaMode:AlphaMode.Blend}}/></Scene>);
      const textured=await s.export();if(format===Format.Glb)assert.equal((await validator.validateBytes(textured)).issues.numErrors,0);
      await s.render(<Scene><Mesh geometry={foreign}/></Scene>);await assert.rejects(s.export(),{code:ErrorCode.InvalidTarget});
      await s.render(createElement(Scene,{},createElement(Mesh,{geometry:g, unknown:true} as never)));await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
      await s.render(<Scene><Mesh geometry={g} material={{baseColor:[1,1,1,0.5]}}/></Scene>);await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
      await s.render(<Scene><Mesh geometry={g} material={{baseColor:[1,1,1,0.5],alphaMode:AlphaMode.Blend}}/></Scene>);assert.ok((await s.export()).length>0);
      await s.render(<Scene><Mesh geometry={g}><Group/></Mesh></Scene>);await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
      await s.render(<Scene><Mesh geometry={g} scale={[0,1,1]}/></Scene>);await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
      await s.render(<Scene><Mesh geometry={g}/></Scene>);assert.ok((await s.export()).length>0);
    } finally {await Promise.all([s.dispose(),other.dispose()]);}
  });
  sceneTest(`${format}: React state, pending Suspense cancellation and disposal`, async () => {
    const s=createSession(format);const g=await s.registerGeometry(geometry());let update!:(n:number)=>void;
    function Model(){const [n,set]=useState(0);update=set;return <Mesh geometry={g} translation={[n,0,0]}/>;}
    try {
      await s.render(<Scene><Model/></Scene>);const first=await s.snapshot();update(4);const next=await s.snapshot();assert.ok(next.revision>first.revision);
      const mesh=next.targets.find(t=>t.kind==="mesh")!;assert.deepEqual((await s.measure(mesh,{revision:next.revision})).min,[4,0,0]);
      const forever=new Promise<void>(()=>{});function Pending(){use(forever);return null;}
      await s.render(<Scene><Suspense fallback={<Mesh geometry={g}/>}><Pending/></Suspense></Scene>);
      const controller=new AbortController();const task=s.export({signal:controller.signal});controller.abort();await assert.rejects(task,{code:ErrorCode.Cancelled});
      const pending=s.export();const check=assert.rejects(pending,e=>[ErrorCode.Cancelled,ErrorCode.Disposed].includes((e as {code:ErrorCode}).code));const close=s.dispose();assert.equal(s.dispose(),close);await close;await check;
    } finally {await s.dispose();}
  });
}
for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(`${format}: file exports preserve cross-session order behind an earlier render`, async () => {
    const first = createSession(format), last = createSession(format);
    const directory = await mkdtemp(join(tmpdir(), "forge-scene-order-"));
    try {
      const a = await first.registerGeometry(geometry()), b = await last.registerGeometry(geometry());
      await last.render(<Scene><Mesh geometry={b} name="last"/></Scene>);
      const expected = await last.export();
      const render = first.render(<Scene><Mesh geometry={a} name="first"/></Scene>);
      const path = join(directory, `test.${format}`);
      await Promise.all([render, first.exportFile(path), last.exportFile(path, { overwrite: true })]);
      assert.deepEqual(await readFile(path), expected);
    } finally {
      await Promise.all([first.dispose(), last.dispose()]);
      await rm(directory, { recursive: true, force: true });
    }
  });
}

sceneTest("scene registrations reserve aggregate bytes before concurrent validation", async()=>{
  const s=createSession(Format.Glb);const bytes=Buffer.alloc(64*1024*1024);
  try {
    await assert.rejects(s.registerTexture(Buffer.alloc(64*1024*1024+1)),{code:ErrorCode.ResourceLimit});
    // Invalid images deliberately remain queued until all four reservations are
    // made. The fifth registration must hit the aggregate cap before decoding.
    const pending=Array.from({length:4},()=>s.registerTexture(bytes));
    const settled=Promise.allSettled(pending);
    await assert.rejects(s.registerTexture(Buffer.alloc(1)),{code:ErrorCode.ResourceLimit});
    assert.ok((await settled).every(r=>r.status==='rejected'));
    const valid=await s.registerGeometry(geometry());await s.render(<Scene><Mesh geometry={valid}/></Scene>);assert.ok((await s.exportBuffer()).length>0);
  } finally {await s.dispose();}
});

sceneTest("latest failed React scene blocks export until a successful render",async()=>{
  const s=createSession(Format.Glb);const g=await s.registerGeometry(geometry());
  function Broken():React.ReactNode {throw Error('Synthetic render failure');}
  try {
    await s.render(<Scene><Mesh geometry={g}/></Scene>);await s.export();
    await assert.rejects(s.render(<Broken/>));await assert.rejects(s.export());
    await s.render(<Scene><Mesh geometry={g}/></Scene>);assert.ok((await s.export()).length>0);
  }finally{await s.dispose();}
});

import { test } from "node:test";
import assert from "node:assert/strict";
import React, { createRef } from "react";
import { createRequire } from "node:module";
import { createSession, Format, ErrorCode, capabilities, type NodeHandle } from "../src/index.js";
import { Scene, Group, Joint, Mesh, AnimationClip, AnimationTrack, AnimationPath as Path, AnimationInterpolation as Interpolation, type GeometryInput } from "../src/glb.js";

const sceneTest = process.env.REACT_FORGE_SKIP_SCENE_TESTS === "1" ? test.skip : test;
const validator = createRequire(import.meta.url)("gltf-validator") as { validateBytes(bytes: Uint8Array): Promise<{ issues: { numErrors: number; messages: unknown[] } }> };
const geometry = (): GeometryInput => ({
  positions: new Float32Array([0,0,0, 1,0,0, 0,1,0]), normals: new Float32Array([0,0,1, 0,0,1, 0,0,1]), indices: new Uint32Array([0,1,2]),
  skin: { joints: new Uint16Array(12), weights: new Float32Array([1,0,0,0, 1,0,0,0, 1,0,0,0]) },
  morphTargets: [{ name: "smile", positions: new Float32Array([0,0,0, 0,0,0, 0,1,0]), normals: new Float32Array(9) }],
});
const near = (actual: readonly number[], expected: readonly number[]) => actual.forEach((v,i) => assert.ok(Math.abs(v-expected[i]!)<1e-5, `${actual} != ${expected}`));

for (const format of [Format.Glb, Format.Fbx] as const) {
  sceneTest(`${format}: pending bakes settle before an export pins its ordered render`, async () => {
    const s=createSession(format), group=createRef<NodeHandle>();
    try {
      const input=geometry(); delete input.skin; delete input.morphTargets;
      const g=await s.registerGeometry(input);
      const sampler=await s.registerAnimationSampler({path:Path.Translation,times:new Float32Array([0,1]),values:new Float32Array([0,0,0,1,0,0])});
      const tree=(name:string)=><Scene><Group ref={group}><Mesh geometry={g}/></Group><AnimationClip name={name}><AnimationTrack target={group} sampler={sampler}/></AnimationClip></Scene>;
      await s.render(tree("Before"));
      let callbacks=0;
      const pending=s.bakeAnimationSampler({path:Path.Translation,duration:10,sample:t=>{callbacks++;return [t,0,0];}});
      const exported=s.exportBuffer();
      const rendered=s.render(tree("After"));
      const bytes=await exported;
      assert.equal(callbacks,601); await pending;
      assert.ok(bytes.includes(Buffer.from("Before")));
      assert.ok(!bytes.includes(Buffer.from("After")));
      await rendered;
      assert.ok((await s.exportBuffer()).includes(Buffer.from("After")));
      await assert.rejects(s.bakeAnimationSampler({path:Path.Translation,duration:1,sample:()=>{throw Error("PRIVATE_CALLBACK_DATA");}}), (error: unknown)=>{
        assert.equal((error as {code:string}).code,ErrorCode.Render);
        assert.doesNotMatch(JSON.stringify(error),/PRIVATE_CALLBACK_DATA/); return true;
      });
      assert.ok((await s.exportBuffer()).length>0);
    } finally {await s.dispose();}
  });
  sceneTest(`${format}: refs resolve in one render, clips combine skinning and morphs, bounds clamp time`, async () => {
    assert.equal(capabilities.formats[format].staticOnly, false);
    assert.equal(capabilities.formats[format].animation, true);
    const s = createSession(format); const joint = createRef<NodeHandle>(), mesh = createRef<NodeHandle>(), clip = createRef<NodeHandle>();
    try {
      const input=geometry(); const pending=s.registerGeometry(input); input.morphTargets![0]!.positions.fill(10); input.skin!.weights.fill(0);
      const g=await pending;
      const translation=await s.registerAnimationSampler({ path: Path.Translation, times: new Float32Array([0,1]), values: new Float32Array([0,0,0, 2,0,0]) });
      const weights=await s.registerAnimationSampler({ path: Path.Weights, times: new Float32Array([0,1]), values: new Float32Array([0,1]) });
      await s.render(<Scene><Joint ref={joint}/><Mesh ref={mesh} geometry={g} skin={{joints:[joint]}}/><AnimationClip ref={clip} name="move"><AnimationTrack target={joint} sampler={translation}/><AnimationTrack target={mesh} sampler={weights}/></AnimationClip></Scene>);
      const snapshot=await s.snapshot();
      assert.equal(snapshot.targets.filter(t=>t.kind==="animation_clip").length,1);
      near((await s.measure(mesh.current!,{revision:snapshot.revision})).max,[1,1,0]);
      for (const [time,min,max] of [[-1,[0,0,0],[1,1,0]],[0.5,[1,0,0],[2,1.5,0]],[2,[2,0,0],[3,2,0]]] as const) {
        const bounds=await s.measure(mesh.current!,{revision:snapshot.revision,animation:{clip:clip.current!,time}});
        near(bounds.min,min); near(bounds.max,max);
      }
      const bytes=await s.exportBuffer();
      if (format===Format.Glb) {
        const result=await validator.validateBytes(bytes); assert.equal(result.issues.numErrors,0,JSON.stringify(result.issues.messages));
        const json=JSON.parse(bytes.subarray(20,20+bytes.readUInt32LE(12)).toString());
        assert.equal(json.animations[0].name,"move"); assert.equal(json.skins.length,1); assert.equal(json.meshes[0].extras.targetNames[0],"smile");
      }
    } finally { await s.dispose(); }
  });
  sceneTest(`${format}: STEP, CUBIC and baked tracks retain revision and immutable input`, async () => {
    const s=createSession(format); const mesh=createRef<NodeHandle>(), group=createRef<NodeHandle>(), clip=createRef<NodeHandle>();
    try {
      const input=geometry(); delete input.skin; delete input.morphTargets; const g=await s.registerGeometry(input);
      const times=new Float32Array([0,1]), values=new Float32Array([0,0,0,2,0,0]);
      const registered=s.registerAnimationSampler({path:Path.Translation,times,values,interpolation:Interpolation.Cubic,inTangents:new Float32Array(6),outTangents:new Float32Array(6)});
      times.fill(9); values.fill(9); const cubic=await registered;
      const step=await s.registerAnimationSampler({path:Path.Scale,times:new Float32Array([0,1]),values:new Float32Array([1,1,1,2,2,2]),interpolation:Interpolation.Step});
      const seen:number[]=[];
      const baked=await s.bakeAnimationSampler({path:Path.Rotation,duration:0.3,fps:10,sample:t=>{seen.push(t);return [0,0,Math.sin(t/2),Math.cos(t/2)];}});
      assert.deepEqual(seen,[0,0.1,0.2,0.3]);
      await s.render(<Scene><Group ref={group}><Mesh ref={mesh} geometry={g}/></Group><AnimationClip ref={clip} name="curves"><AnimationTrack target={group} sampler={cubic}/><AnimationTrack target={mesh} sampler={step}/></AnimationClip><AnimationClip name="baked"><AnimationTrack target={group} sampler={baked}/></AnimationClip></Scene>);
      const snapshot=await s.snapshot(); const before=await s.measure(mesh.current!,{revision:snapshot.revision,animation:{clip:clip.current!,time:0.25}});
      near(before.min,[0.3125,0,0]); near(before.max,[1.3125,1,0]);
      const bytes=await s.export(); if(format===Format.Glb){const r=await validator.validateBytes(bytes);assert.equal(r.issues.numErrors,0,JSON.stringify(r.issues.messages));}
      await s.render(<Scene><Mesh geometry={g}/></Scene>);
      await assert.rejects(s.measure(mesh.current ?? snapshot.targets[2]!,{revision:snapshot.revision}),{code:ErrorCode.Conflict});
    } finally {await s.dispose();}
  });
}

sceneTest("animation assets reject malformed, singular, duplicate and foreign data", async () => {
  const s=createSession(Format.Glb), other=createSession(Format.Glb); const joint=createRef<NodeHandle>(), mesh=createRef<NodeHandle>();
  try {
    const good={path:Path.Translation,times:new Float32Array([0,1]),values:new Float32Array([0,0,0,1,0,0])};
    for (const times of [new Float32Array([0,0]),new Float32Array([1,0]),new Float32Array([-1,0]),new Float32Array([0,NaN])]) await assert.rejects(s.registerAnimationSampler({...good,times}),{code:ErrorCode.MalformedInput});
    await assert.rejects(s.registerAnimationSampler({...good,path:Path.Scale,values:new Float32Array([1,1,1,-1,1,1])}),{code:ErrorCode.MalformedInput});
    await assert.rejects(s.registerAnimationSampler({path:Path.Rotation,times:good.times,values:new Float32Array([0,0,0,1,0,0,0,-1]),interpolation:Interpolation.Cubic,inTangents:new Float32Array(8),outTangents:new Float32Array(8)}),{code:ErrorCode.MalformedInput});
    const badGeometry=geometry(); badGeometry.skin!.weights[0]=0.5;
    await assert.rejects(s.registerGeometry(badGeometry),{code:ErrorCode.MalformedInput});
    const g=await s.registerGeometry(geometry()), sampler=await s.registerAnimationSampler(good);
    await s.render(<Scene><Joint ref={joint}/><Mesh geometry={g} skin={{joints:[joint]}}/><AnimationClip name="duplicate"><AnimationTrack target={joint} sampler={sampler}/><AnimationTrack target={joint} sampler={sampler}/></AnimationClip></Scene>);
    await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
    await s.render(<Scene><Joint ref={joint}/><Mesh ref={mesh} geometry={g} skin={{joints:[joint]}}/><AnimationClip name="skin transform"><AnimationTrack target={mesh} sampler={sampler}/></AnimationClip></Scene>);
    await assert.rejects(s.export(),{code:ErrorCode.MalformedInput});
    const foreign=await other.registerAnimationSampler(good);
    await s.render(<Scene><Joint ref={joint}/><AnimationClip name="foreign"><AnimationTrack target={joint} sampler={foreign}/></AnimationClip></Scene>);
    await assert.rejects(s.export(),{code:ErrorCode.InvalidTarget});
    await s.render(<Scene><AnimationClip name="removed"><AnimationTrack target={joint} sampler={sampler}/></AnimationClip></Scene>);
    await assert.rejects(s.export(),{code:ErrorCode.InvalidTarget});
    assert.throws(()=>s.bakeAnimationSampler({path:Path.Translation,duration:1e12,sample:()=>[0,0,0]}),{code:ErrorCode.ResourceLimit});
    assert.throws(()=>createSession(Format.Fbx,{animationBakeFps:241}),{code:ErrorCode.MalformedInput});
  } finally {await Promise.all([s.dispose(),other.dispose()]);}
});

sceneTest("baking cancellation and disposal release reservations and wait for callbacks", async () => {
  const s=createSession(Format.Glb); const controller=new AbortController(); let calls=0;
  try {
    const task=s.bakeAnimationSampler({path:Path.Translation,duration:100,fps:60,sample:()=>{if(++calls===2)controller.abort();return [0,0,0];}},{signal:controller.signal});
    await assert.rejects(task,{code:ErrorCode.Cancelled}); assert.equal(calls,2);
    await s.registerAnimationSampler({path:Path.Translation,times:new Float32Array([0]),values:new Float32Array([0,0,0])});
    const pending=s.bakeAnimationSampler({path:Path.Translation,duration:100,sample:()=>[0,0,0]});
    const rejected=assert.rejects(pending,{code:ErrorCode.Cancelled}); await s.dispose(); await rejected;
  } finally {await s.dispose();}
});

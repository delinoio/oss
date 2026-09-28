// Local acceptance only: export original assets, validate with Khronos, then
// compare a separate Three.js evaluator against revision-pinned native bounds.
import assert from "node:assert/strict";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { resolve, join } from "node:path";
import { createHash } from "node:crypto";
import { createRequire } from "node:module";
import * as THREE from "three";
import { GLTFLoader } from "three/addons/loaders/GLTFLoader.js";
import { Format } from "@delino/react-forge";
import { animatedCharacter } from "../examples/animated-character.tsx";
const directory=resolve(process.argv[2]??"");
if(!process.argv[2]) throw Error("Pass an output directory outside the source tree");
await mkdir(directory,{recursive:true});
const records=[];
for(const format of [Format.Glb,Format.Fbx]) {
  const session=await animatedCharacter(format);
  try {
    const snapshot=await session.snapshot();
    const root=snapshot.targets.find(t=>t.name==="Character");
    const clips=[];
    for(const clip of snapshot.targets.filter(t=>t.kind==="animation_clip")) {
      const samples=[];
      for(const time of [0,0.25,0.5,0.75,1,1.25,1.5,1.75,2]) {
        const bounds=await session.measure(root,{revision:snapshot.revision,animation:{clip,time}});
        samples.push({time,min:bounds.min,max:bounds.max});
      }
      clips.push({name:clip.name,samples});
    }
    const filename=`animated-character.${format}`;
    await session.exportFile(join(directory,filename),{overwrite:true});
    const bytes=await readFile(join(directory,filename));
    records.push({format,filename,sha256:createHash("sha256").update(bytes).digest("hex"),bytes:bytes.length,clips});
  } finally {await session.dispose();}
}
assert.deepEqual(records[0].clips,records[1].clips);
const bytes=await readFile(join(directory,records[0].filename));
const validation=await createRequire(import.meta.url)("gltf-validator").validateBytes(bytes);
assert.equal(validation.issues.numErrors,0,JSON.stringify(validation.issues));
const gltf=await new GLTFLoader().parseAsync(bytes.buffer.slice(bytes.byteOffset,bytes.byteOffset+bytes.byteLength),"");
assert.deepEqual(gltf.animations.map(c=>c.name),["idle","walk","wave"]);
const mixer=new THREE.AnimationMixer(gltf.scene);
let error=0;
for(const reference of records[0].clips) {
  mixer.stopAllAction(); mixer.setTime(0);
  const action=mixer.clipAction(gltf.animations.find(c=>c.name===reference.name));
  action.setLoop(THREE.LoopOnce,1); action.clampWhenFinished=true; action.reset().play();
  for(const sample of reference.samples) {
    mixer.setTime(sample.time); gltf.scene.updateMatrixWorld(true);
    gltf.scene.traverse(object=>{if(object.isSkinnedMesh)object.skeleton.update();});
    const box=new THREE.Box3().setFromObject(gltf.scene,true);
    for(const end of ["min","max"]) box[end].toArray().forEach((v,i)=>{const delta=Math.abs(v-sample[end][i]);error=Math.max(error,delta);assert.ok(delta<0.0002,`${reference.name} ${sample.time} ${end}[${i}] ${v} != ${sample[end][i]}`);});
  }
}
const report={node:process.version,three:THREE.REVISION,gltfValidator:"2.0.0-dev.3.10",gltfIssues:validation.issues,threeMaxBoundsError:error,files:records};
await writeFile(join(directory,"animation-reference.json"),JSON.stringify(report,null,2)+"\n");
console.log(JSON.stringify({event:"react_forge_animation_verified",formats:2,clips:3,samples:54,gltfErrors:validation.issues.numErrors,threeMaxBoundsError:error}));

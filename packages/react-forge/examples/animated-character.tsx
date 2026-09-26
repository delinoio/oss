import React, { createRef } from "react";
import { createSession, Format, type NodeHandle } from "@delino/react-forge";
import { Scene, Group, Joint, Mesh, PerspectiveCamera, PointLight, AnimationClip, AnimationTrack, AnimationPath as Path, AnimationInterpolation as Interpolation, type GeometryInput, type Vec3, type AnimationSamplerHandle } from "@delino/react-forge/glb";

// Original procedural robot. No imported geometry, textures, or motion data.
type Box = { center: Vec3; size: Vec3; joint: number; blend?: number };
function boxes(parts: Box[], smile = false): GeometryInput {
  const positions:number[]=[], normals:number[]=[], indices:number[]=[], joints:number[]=[], weights:number[]=[], deltas:number[]=[];
  const faces=[[[1,0,0],[1,-1,-1],[1,1,-1],[1,1,1],[1,-1,1]], [[-1,0,0],[-1,-1,1],[-1,1,1],[-1,1,-1],[-1,-1,-1]], [[0,1,0],[-1,1,1],[1,1,1],[1,1,-1],[-1,1,-1]], [[0,-1,0],[-1,-1,-1],[1,-1,-1],[1,-1,1],[-1,-1,1]], [[0,0,1],[-1,-1,1],[1,-1,1],[1,1,1],[-1,1,1]], [[0,0,-1],[1,-1,-1],[-1,-1,-1],[-1,1,-1],[1,1,-1]]];
  for (const part of parts) for (const face of faces) {
    const base=positions.length/3;
    for (const point of face.slice(1)) {
      positions.push(...point.map((v,i)=>part.center[i]!+v*part.size[i]!/2)); normals.push(...face[0]!);
      const mixed=part.blend!==undefined && point[1]!>0;
      joints.push(part.joint,mixed?part.blend!:0,0,0); weights.push(mixed?0.8:1,mixed?0.2:0,0,0);
      deltas.push(smile?point[0]!*0.035:0,smile?(point[1]!>0?0.055:-0.015):0,0);
    }
    indices.push(base,base+1,base+2,base,base+2,base+3);
  }
  return {positions:new Float32Array(positions),normals:new Float32Array(normals),indices:new Uint32Array(indices),skin:{joints:new Uint16Array(joints),weights:new Float32Array(weights)},...(smile?{morphTargets:[{name:"smile",positions:new Float32Array(deltas)}]}:{})};
}
const rotation=(axis:"x"|"z",angle:number):readonly number[]=>axis==="x"?[Math.sin(angle/2),0,0,Math.cos(angle/2)]:[0,0,Math.sin(angle/2),Math.cos(angle/2)];

export async function animatedCharacter(format: Format.Glb | Format.Fbx, signal?: AbortSignal) {
  const session=createSession(format,{animationBakeFps:60});
  try {
    const refs=Array.from({length:7},()=>createRef<NodeHandle>()), root=createRef<NodeHandle>(), mouth=createRef<NodeHandle>(), camera=createRef<NodeHandle>(), light=createRef<NodeHandle>();
    const body=await session.registerGeometry(boxes([
      {center:[0,1.45,0],size:[0.65,0.75,0.38],joint:1}, {center:[0,1.08,0],size:[0.53,0.23,0.34],joint:0},
      {center:[0,2.07,0],size:[0.69,0.51,0.45],joint:2},
      {center:[-0.48,1.34,0],size:[0.22,0.72,0.25],joint:3,blend:1}, {center:[0.48,1.34,0],size:[0.22,0.72,0.25],joint:4,blend:1},
      {center:[-0.18,0.59,0],size:[0.24,0.8,0.28],joint:5}, {center:[0.18,0.59,0],size:[0.24,0.8,0.28],joint:6},
      {center:[-0.18,0.16,0.08],size:[0.29,0.18,0.46],joint:5}, {center:[0.18,0.16,0.08],size:[0.29,0.18,0.46],joint:6},
    ]),{signal});
    const eyes=await session.registerGeometry(boxes([{center:[-0.15,2.13,0.235],size:[0.1,0.09,0.04],joint:2},{center:[0.15,2.13,0.235],size:[0.1,0.09,0.04],joint:2}]),{signal});
    const face=await session.registerGeometry(boxes([{center:[0,1.98,0.235],size:[0.26,0.035,0.04],joint:2}],true),{signal});
    const motions:Record<string,{target:typeof root;sampler:AnimationSamplerHandle}[]>={};
    for (const name of ["idle","walk","wave"]) {
      const tracks:{target:typeof root;sampler:AnimationSamplerHandle}[]=[];
      const add=async(target:typeof root,path:Path,sample:(t:number)=>readonly number[],components?:number)=>tracks.push({target,sampler:await session.bakeAnimationSampler({path,duration:2,fps:30,sample,components},{signal})});
      await add(refs[0]!,Path.Translation,t=>[0,1.08+0.035*Math.sin(Math.PI*t*(name==="walk"?2:1)),0]);
      for (const j of [3,4,5,6]) await add(refs[j]!,Path.Rotation,t=>name==="walk"?rotation("x",Math.sin(Math.PI*t*2)*0.55*(j%2===0?1:-1)):name==="wave"&&j===4?rotation("z",2.25+0.3*Math.sin(Math.PI*t*3)):rotation("x",0));
      await add(mouth,Path.Weights,t=>[name==="wave"?0.6+0.4*Math.sin(Math.PI*t/2):0.15],1);
      await add(camera,Path.Translation,t=>[0.25*Math.sin(Math.PI*t),2.8,6]);
      await add(light,Path.Translation,t=>[3,4,3+0.3*Math.sin(Math.PI*t)]);
      const positions=name==="walk"?[0,0,0,0.5,0,0,0,0,0]:Array(9).fill(0);
      tracks.push({target:root,sampler:await session.registerAnimationSampler({path:Path.Translation,times:new Float32Array([0,1,2]),values:new Float32Array(positions),interpolation:Interpolation.Cubic,inTangents:new Float32Array(9),outTangents:new Float32Array(9)},{signal})});
      motions[name]=tracks;
    }
    await session.render(<Scene name="Forge Robot">
      <Group name="Character" ref={root}>
        <Joint name="Hips" ref={refs[0]} translation={[0,1.08,0]}>
          <Joint name="Spine" ref={refs[1]} translation={[0,0.35,0]}>
            <Joint name="Head" ref={refs[2]} translation={[0,0.52,0]}/>
            <Joint name="LeftArm" ref={refs[3]} translation={[-0.48,0.25,0]}/><Joint name="RightArm" ref={refs[4]} translation={[0.48,0.25,0]}/>
          </Joint>
          <Joint name="LeftLeg" ref={refs[5]} translation={[-0.18,-0.09,0]}/><Joint name="RightLeg" ref={refs[6]} translation={[0.18,-0.09,0]}/>
        </Joint>
        <Mesh name="Body" geometry={body} skin={{joints:refs}} material={{baseColor:[0.025,0.38,0.55,1],metallic:0.25,roughness:0.35}}/>
        <Mesh name="Eyes" geometry={eyes} skin={{joints:refs}} material={{baseColor:[0.025,0.03,0.06,1],roughness:0.4}}/>
        <Mesh name="Smile" ref={mouth} geometry={face} skin={{joints:refs}} material={{baseColor:[0.9,0.23,0.04,1],roughness:0.4}}/>
      </Group>
      <PerspectiveCamera name="Camera" ref={camera} translation={[0,2.8,6]} rotation={[Math.sin(-0.12),0,0,Math.cos(-0.12)]} yfov={0.6}/>
      <PointLight name="KeyLight" ref={light} translation={[3,4,3]} intensity={1200}/>
      {Object.entries(motions).map(([name,tracks])=><AnimationClip key={name} name={name}>{tracks.map((track,i)=><AnimationTrack key={i} {...track}/>)}</AnimationClip>)}
    </Scene>);
    return session;
  } catch(error) {await session.dispose();throw error;}
}

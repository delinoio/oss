import React from "react";
import { fileURLToPath } from "node:url";
import { createSession, Format, type SceneSession } from "@delino/react-forge";
import { Scene, Group, Mesh, PerspectiveCamera, PointLight, type Material, type GeometryHandle, type Vec3 } from "@delino/react-forge/glb";
import { roundedBox, cylinder, ring, arc, plane, quaternion as q } from "./audio-studio-assets/geometry.js";

export enum Product { Studio="studio", Headphones="headphones", Dac="dac", Stand="stand" }
type Assets = Awaited<ReturnType<typeof assets>>;
async function assets(session:SceneSession,signal?:AbortSignal) {
  const shapes={
    shell:cylinder(.048,.019),rim:ring(.049,.045,.003),cushion:ring(.044,.028,.019),inner:cylinder(.029,.001),hinge:cylinder(.009,.006),
    headband:arc(.104,.148,.005,.030,.02,Math.PI-.02),padding:arc(.097,.141,.008,.026,.15,Math.PI-.15),
    yoke:arc(.052,.063,.004,.006,0,Math.PI), slider:roundedBox([.009,.050,.006],.002),bridge:roundedBox([.040,.005,.008],.002),
    base:roundedBox([.150,.014,.130],.006),stem:roundedBox([.012,.318,.022],.005),saddle:roundedBox([.074,.012,.045],.006),
    dac:roundedBox([.220,.064,.170],.008),front:roundedBox([.211,.053,.005],.003),foot:cylinder(.012,.008),
    knob:cylinder(.027,.018),knobFace:cylinder(.0235,.0008),knobRidge:roundedBox([.001,.013,.001],.0004,4),
    port:ring(.008,.0055,.004),portInner:cylinder(.0054,.005),screw:cylinder(.002,.001),screwSlot:roundedBox([.0026,.00045,.0002],.0001,4),
    screen:roundedBox([.085,.034,.002],.001),display:plane(.077,.029),wordmark:plane(.042,.010),marker:roundedBox([.0018,.004,.0005],.0002,4),
    led:cylinder(.0016,.001),plinth:roundedBox([.600,.015,.310],.007),
  };
  const entries=await Promise.all(Object.entries(shapes).map(async([key,data])=>[key,await session.registerGeometry(data,{signal})] as const));
  const geometry=Object.fromEntries(entries) as Record<keyof typeof shapes,GeometryHandle>;
  const image=async(name:string)=>session.registerTexture({path:fileURLToPath(new URL(`./audio-studio-assets/${name}.png`,import.meta.url))},{signal});
  const [brush,leather,normal,display,wordmark]=await Promise.all([image("brushed-roughness"),image("leather-roughness"),image("leather-normal"),image("display"),image("wordmark")]);
  const material={
    metal:{baseColor:[.46,.43,.37,1],metallic:1,roughness:.44,roughnessTexture:brush},
    silver:{baseColor:[.72,.74,.77,1],metallic:1,roughness:.32,roughnessTexture:brush},
    graphite:{baseColor:[.025,.029,.034,1],metallic:.45,roughness:.43,roughnessTexture:brush},
    rubber:{baseColor:[.012,.014,.016,1],roughness:.85,roughnessTexture:leather,normalTexture:normal},
    inset:{baseColor:[.006,.009,.012,1],roughness:.75},
    orange:{baseColor:[.85,.23,.047,1],metallic:.25,roughness:.30},
    screen:{baseColor:[.015,.028,.032,1],metallic:.3,roughness:.15},
    display:{baseColorTexture:display,emissiveTexture:display,emissive:[.65,.65,.65],roughness:.6},
    wordmark:{baseColorTexture:wordmark,metallic:.5,roughness:.5},
    led:{baseColor:[.11,.65,.68,1],emissive:[.15,.8,.8],roughness:.3},
    plinth:{baseColor:[.25,.275,.285,1],roughness:.72},
  } satisfies Record<string,Material>;
  return {geometry,material};
}
function Headphones({a}:{a:Assets}) {
  const {geometry:g,material:m}=a;
  return <Group name="AURA H01 headphones">
    <Group translation={[0,.210,0]}><Mesh name="Continuous brushed headband" geometry={g.headband} material={m.metal}/><Mesh name="Padded inner headband" geometry={g.padding} material={m.rubber}/></Group>
    {([-1,1] as const).map(side=><Group key={side} name={side<0?"Left earcup":"Right earcup"} translation={[side*.105,.192,0]}>
      <Mesh name="Fork suspension bridge" geometry={g.bridge} material={m.graphite} translation={[side*.009,.061,0]}/>
      <Mesh name="Adjustable metal slider" geometry={g.slider} material={m.silver} translation={[0,.020,0]}/>
      <Group rotation={q([0,0,1],-side*Math.PI/2)}>
        <Mesh name="Anodized outer housing" geometry={g.shell} material={m.metal} translation={[0,.009,0]} scale={[1,1,1.21]}/>
        <Mesh name="Machined highlight rim" geometry={g.rim} material={m.silver} translation={[0,.020,0]} scale={[1,1,1.21]}/>
        <Mesh name="Soft oval ear cushion" geometry={g.cushion} material={m.rubber} translation={[0,-.018,0]} scale={[1,1,1.25]}/>
        <Mesh name="Acoustic fabric inset" geometry={g.inner} material={m.inset} translation={[0,-.019,0]} scale={[1,1,1.25]}/>
        <Mesh name="Pivot cap" geometry={g.hinge} material={m.graphite} translation={[0,.024,0]}/>
        <Mesh name="Warm accent pivot" geometry={g.led} material={m.orange} translation={[0,.028,0]}/>
      </Group>
      <Mesh name="Suspension fork" geometry={g.yoke} material={m.graphite} rotation={q([0,1,0],Math.PI/2)} translation={[side*.025,0,0]}/>
    </Group>)}
  </Group>;
}
function Stand({a}:{a:Assets}) {
  const {geometry:g,material:m}=a;
  return <Group name="AURA S01 stand">
    <Mesh name="Weighted chamfered base" geometry={g.base} material={m.graphite} translation={[0,.011,0]}/>
    <Mesh name="Upright aluminum spine" geometry={g.stem} material={m.metal} translation={[0,.177,-.029]}/>
    <Mesh name="Protective saddle" geometry={g.saddle} material={m.rubber} translation={[0,.341,0]}/>
    <Mesh name="Base identity mark" geometry={g.wordmark} material={m.wordmark} translation={[0,.0187,.033]} rotation={q([1,0,0],-Math.PI/2)}/>
  </Group>;
}
function Dac({a}:{a:Assets}) {
  const {geometry:g,material:m}=a;const front=q([1,0,0],Math.PI/2);
  return <Group name="AURA D01 desktop DAC" translation={[0,.008,0]}>
    <Mesh name="One-piece anodized chassis" geometry={g.dac} material={m.graphite} translation={[0,.032,0]}/>
    <Mesh name="Brushed front plate" geometry={g.front} material={m.metal} translation={[0,.032,.085]}/>
    <Mesh name="Recessed glass-look display cover" geometry={g.screen} material={m.screen} translation={[-.047,.034,.088]}/>
    <Mesh name="192 kHz studio display" geometry={g.display} material={m.display} translation={[-.047,.034,.0892]}/>
    <Group name="Knurled precision volume control" translation={[.065,.034,.096]} rotation={front}>
      <Mesh geometry={g.knob} material={m.metal}/><Mesh geometry={g.knobFace} material={m.graphite} translation={[0,.0093,0]}/>
      {Array.from({length:64},(_,i)=>{const a=i/64*Math.PI*2;return <Mesh key={i} name="Machined grip flute" geometry={g.knobRidge} material={m.silver} translation={[Math.sin(a)*.0268,0,Math.cos(a)*.0268]} rotation={q([0,1,0],a)}/>;})}
      <Mesh name="Orange volume index" geometry={g.marker} material={m.orange} translation={[0,.010,.018]} rotation={q([1,0,0],Math.PI/2)}/>
    </Group>
    <Mesh name="Headphone socket surround" geometry={g.port} material={m.silver} translation={[.013,.034,.088]} rotation={front}/>
    <Mesh name="Socket cavity" geometry={g.portInner} material={m.inset} translation={[.013,.034,.086]} rotation={front}/>
    <Mesh name="Status LED" geometry={g.led} material={m.led} translation={[-.093,.034,.089]} rotation={front}/>
    {([-1,1] as const).flatMap(x=>([-1,1] as const).map(y=><Group key={`${x}:${y}`} translation={[x*.098,.032+y*.020,.089]}><Mesh name="Recessed fastener" geometry={g.screw} material={m.graphite} rotation={front}/><Mesh name="Fastener slot" geometry={g.screwSlot} material={m.silver} translation={[0,0,.0007]}/></Group>))}
    {([-1,1] as const).flatMap(x=>([-1,1] as const).map(z=><Mesh key={`${x}:${z}`} name="Vibration-isolation foot" geometry={g.foot} material={m.rubber} translation={[x*.084,-.003,z*.06]}/>))}
    {[-.067,-.032,.014,.061].map((x,i)=><Group key={i} translation={[x,.033,-.088]} rotation={q([0,1,0],Math.PI)}><Mesh name="Rear connector ring" geometry={g.port} material={i===2?m.orange:m.silver} rotation={front}/><Mesh name="Rear connector cavity" geometry={g.portInner} material={m.inset} rotation={front}/></Group>)}
    <Mesh name="Top identity mark" geometry={g.wordmark} material={m.wordmark} translation={[0,.0645,-.020]} rotation={q([1,0,0],-Math.PI/2)}/>
  </Group>;
}
export default async function task({data,signal}:{data?:{format?:Format.Glb|Format.Fbx;product?:Product};signal?:AbortSignal}={}) {
  const format=data?.format??Format.Glb,product=data?.product??Product.Studio;
  if(![Format.Glb,Format.Fbx].includes(format)||!Object.values(Product).includes(product))throw new Error("Invalid audio-studio task selection");
  const s=createSession(format);
  try {
    const a=await assets(s,signal);
    await s.render(<Scene name={`AURA / ${product}`}>
      {(product===Product.Headphones||product===Product.Studio)?<Headphones a={a}/>:null}
      {(product===Product.Stand||product===Product.Studio)?<Stand a={a}/>:null}
      {product===Product.Dac?<Dac a={a}/>:product===Product.Studio?<Group translation={[.280,0,.015]} rotation={q([0,1,0],-.12)}><Dac a={a}/></Group>:null}
      {product===Product.Studio?<Mesh name="Studio presentation platform" geometry={a.geometry.plinth} material={a.material.plinth} translation={[.130,-.0075,0]}/>:null}
      <PerspectiveCamera name="Product camera" yfov={.50} aspect={1} near={.01} far={100} translation={[.60,.48,.95]} rotation={q([1,0,0],-.30)} />
      <PointLight name="Soft accent source" color={[.8,.92,1]} intensity={.05} translation={[-.5,.6,.4]}/>
    </Scene>);
    return s;
  } catch(error){await s.dispose();throw error;}
}

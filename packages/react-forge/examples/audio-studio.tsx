import React from "react";
import { fileURLToPath } from "node:url";
import { createSession, Format, type SceneSession } from "@delino/react-forge";
import { Scene, Group, Mesh, PerspectiveCamera, PointLight, AlphaMode, type Material, type GeometryHandle } from "@delino/react-forge/glb";
import { roundedBox, cylinder, ring, arc, plane, machinedCylinder, cushion, torus, flutedGrip, stitches, tube, merge, label, perforatedLid, quaternion as q } from "./audio-studio-assets/geometry.js";

export enum Product { Studio="studio", Headphones="headphones", Dac="dac", Stand="stand" }
type Assets = Awaited<ReturnType<typeof assets>>;
const front=q([1,0,0],Math.PI/2),top=q([1,0,0],-Math.PI/2);

async function assets(session:SceneSession,signal?:AbortSignal) {
  const bandStitch=merge([-1,1].flatMap(side=>Array.from({length:126},(_,i)=>tube(Array.from({length:4},(_,j)=>{const a=.12+(Math.PI-.24)*(i+j/5)/126;return [.1012*Math.cos(a),.1272*Math.sin(a),side*.0165] as const;}),.00014,6))));
  const shapes={
    shell:machinedCylinder(.049,.019,.0035),cap:machinedCylinder(.045,.004,.0018),capInset:machinedCylinder(.0416,.001,.0004),
    rim:torus(.0467,.0007,1.22),capLine:torus(.042,.00020,1.22),cushion:cushion(),padSeam:torus(.047,.00045,1.24),padStitch:stitches(.047,1.24),
    inner:machinedCylinder(.029,.002,.0008),hinge:machinedCylinder(.0062,.003,.0008),hingeInset:machinedCylinder(.004,.0008,.0003),
    headband:arc(.108,.138,.0032,.035,.025,Math.PI-.025,192),padding:arc(.103,.129,.009,.036,.10,Math.PI-.10,192),bandStitch,
    yoke:arc(.048,.064,.0034,.0055,.0,Math.PI,160),slider:roundedBox([.007,.053,.004],.0015,20),sleeve:roundedBox([.011,.025,.007],.0025,20),bridge:roundedBox([.026,.0045,.006],.002,16),
    adjustment:roundedBox([.004,.0005,.00025],.0001,4),
    base:roundedBox([.157,.018,.138],.014,24),baseInset:roundedBox([.140,.002,.122],.013,20),stem:roundedBox([.012,.310,.025],.006,24),stemInlay:roundedBox([.0015,.267,.0006],.0003,8),saddle:roundedBox([.078,.012,.044],.006,24),saddleBase:roundedBox([.080,.005,.043],.0025,20),
    dac:roundedBox([.254,.050,.183],.009,24),front:roundedBox([.250,.048,.005],.005,24),rear:roundedBox([.246,.043,.003],.003,20),lid:perforatedLid(),foot:machinedCylinder(.013,.007,.0015),footRim:torus(.0115,.0004),
    knob:machinedCylinder(.0218,.017,.0015),knobFace:machinedCylinder(.0203,.001,.0004),knobRim:torus(.0208,.00045),knurl:flutedGrip(.0218,.013),knobRing:torus(.024,.00065),
    port:ring(.0055,.0033,.004,128),balanced:ring(.0065,.0048,.004,128),portInner:machinedCylinder(.0048,.004,.0003),smallInner:machinedCylinder(.00325,.004,.0003),pin:machinedCylinder(.00065,.003,.0002,32),
    screw:machinedCylinder(.0022,.0009,.0003,48),screwSlot:roundedBox([.0026,.0004,.0002],.0001,4),
    screen:roundedBox([.092,.034,.0015],.002,24),display:plane(.085,.030),marker:roundedBox([.0012,.0032,.0003],.0002,8),
    led:machinedCylinder(.00075,.0006,.0002,32),button:machinedCylinder(.004,.002,.0006,64),
    ventBacking:plane(.050,.064),fin:roundedBox([.0016,.035,.0018],.0006,12),
    usb:roundedBox([.009,.0036,.003],.0016,16),usbInner:roundedBox([.006,.0009,.0031],.0004,12),
    rca:ring(.0042,.0022,.007,96),rcaSleeve:torus(.0044,.00075),
    power:ring(.005,.003,.004,96),
    brand:label(.029,.006,0),smallBrand:label(.022,.0046,0),headphoneLabel:label(.036,.0025,1),dacLabel:label(.037,.0024,2),standLabel:label(.033,.0025,3),balancedLabel:label(.016,.0019,4),jackLabel:label(.012,.0019,5),usbLabel:label(.012,.0024,6),lineLabel:label(.019,.0024,7),powerLabel:label(.015,.0024,8),tagline:label(.076,.0033,9),serial:label(.034,.0024,10),leftLabel:label(.0026,.0038,11),rightLabel:label(.0026,.0038,12),inputLabel:label(.010,.002,14),
  };
  const entries=await Promise.all(Object.entries(shapes).map(async([key,data])=>[key,await session.registerGeometry(data,{signal})] as const));
  const geometry=Object.fromEntries(entries) as Record<keyof typeof shapes,GeometryHandle>;
  const image=async(name:string)=>session.registerTexture({path:fileURLToPath(new URL(`./audio-studio-assets/${name}.png`,import.meta.url))},{signal});
  const [brush,brushNormal,leather,normal,fabric,display,wordmark]=await Promise.all([image("brushed-roughness"),image("brushed-normal"),image("leather-roughness"),image("leather-normal"),image("fabric-normal"),image("display"),image("wordmark")]);
  const material={
    metal:{baseColor:[.54,.42,.28,1],metallic:1,roughness:.48,roughnessTexture:brush,normalTexture:brushNormal},
    silver:{baseColor:[.67,.69,.71,1],metallic:1,roughness:.23},
    graphite:{baseColor:[.028,.035,.039,1],metallic:.85,roughness:.5,roughnessTexture:brush,normalTexture:brushNormal},
    blackMetal:{baseColor:[.012,.017,.019,1],metallic:.8,roughness:.40},
    rubber:{baseColor:[.009,.012,.014,1],roughness:.80,roughnessTexture:leather,normalTexture:normal},
    fabric:{baseColor:[.015,.019,.022,1],roughness:.9,normalTexture:fabric},
    inset:{baseColor:[.003,.004,.005,1],roughness:.65},
    thread:{baseColor:[.105,.089,.069,1],roughness:.85},
    orange:{baseColor:[.6,.24,.061,1],metallic:.65,roughness:.32},
    screen:{baseColor:[.005,.012,.015,1],metallic:.1,roughness:.18},
    display:{baseColorTexture:display,emissiveTexture:display,emissive:[.38,.38,.38],roughness:.23},
    wordmark:{baseColorTexture:wordmark,metallic:.55,roughness:.48,alphaMode:AlphaMode.Blend},
    led:{baseColor:[.62,.77,.67,1],emissive:[.3,.65,.46],roughness:.3},
    red:{baseColor:[.32,.025,.019,1],roughness:.55},
    white:{baseColor:[.62,.61,.54,1],roughness:.55},
  } satisfies Record<string,Material>;
  return {geometry,material};
}

function Fastener({a}:{a:Assets}) {
  return <Group><Mesh name="Black-oxide countersunk screw" geometry={a.geometry.screw} material={a.material.blackMetal} rotation={front}/><Mesh name="Machined screw drive" geometry={a.geometry.screwSlot} material={a.material.inset} translation={[0,0,.00055]}/></Group>;
}

function Headphones({a}:{a:Assets}) {
  const {geometry:g,material:m}=a;
  return <Group name="AURA H01 headphones">
    <Group name="Stitched suspension headband" translation={[0,.213,0]}>
      <Mesh name="Spring aluminum headband" geometry={g.headband} material={m.metal}/>
      <Mesh name="Leather wrapped memory-foam headband" geometry={g.padding} material={m.rubber}/>
      <Mesh name="Double saddle-stitch rows" geometry={g.bandStitch} material={m.thread}/>
    </Group>
    {([-1,1] as const).map(side=><Group key={side} name={side<0?"Left earcup assembly":"Right earcup assembly"} translation={[side*.105,.193,0]}>
      <Mesh name="Precision height adjustment rail" geometry={g.slider} material={m.metal} translation={[0,.034,0]}/>
      <Mesh name="Slider bearing sleeve" geometry={g.sleeve} material={m.blackMetal} translation={[0,.018,0]}/>
      {Array.from({length:5},(_,i)=><Mesh key={i} name="Adjustment witness line" geometry={g.adjustment} material={m.inset} translation={[0,.041+i*.003,.0021]}/>)}
      <Mesh name="Fork crown bridge" geometry={g.bridge} material={m.metal} translation={[side*.011,.064,0]}/>
      <Mesh name="Cast aluminum suspension fork" geometry={g.yoke} material={m.metal} translation={[side*.024,0,0]} rotation={q([0,1,0],Math.PI/2)}/>
      <Group name="Angled sealed acoustic capsule" rotation={q([0,0,1],-side*Math.PI/2)}>
        <Mesh name="Continuous oval housing" geometry={g.shell} material={m.metal} translation={[0,.005,0]} scale={[1.22,1,1]}/>
        <Mesh name="Satin graphite faceplate" geometry={g.cap} material={m.graphite} translation={[0,.016,0]} scale={[1.22,1,1]}/>
        <Mesh name="Fine concentric outer bezel" geometry={g.rim} material={m.silver} translation={[0,.014,0]}/>
        <Mesh name="Inset faceplate island" geometry={g.capInset} material={m.graphite} translation={[0,.0182,0]} scale={[1.22,1,1]}/>
        <Mesh name="Diamond-cut perimeter hairline" geometry={g.capLine} material={m.metal} translation={[0,.0186,0]}/>
        <Mesh name="Sculpted leather ear cushion" geometry={g.cushion} material={m.rubber} translation={[0,-.018,0]}/>
        <Mesh name="Cushion rolled seam" geometry={g.padSeam} material={m.rubber} translation={[0,-.022,0]}/>
        <Mesh name="Cushion hand-stitch detail" geometry={g.padStitch} material={m.thread} translation={[0,-.026,0]}/>
        <Mesh name="Woven acoustic liner" geometry={g.inner} material={m.fabric} translation={[0,-.015,0]} scale={[1.2,1,1]}/>
      </Group>
      {([-1,1] as const).map(z=><Group key={z} translation={[side*.024,0,z*.047]} rotation={q([0,0,1],-side*Math.PI/2)}><Mesh name="Suspension pivot boss" geometry={g.hinge} material={m.blackMetal}/><Mesh name="Pivot machine-finished cap" geometry={g.hingeInset} material={m.metal} translation={[0,.0017,0]}/></Group>)}
      <Group name="Laser-etched earcup identity" translation={[side*.01885,0,0]} rotation={q([0,1,0],side*Math.PI/2)}>
        <Mesh name="AURA earcup logotype" geometry={g.brand} material={m.wordmark} translation={[0,.009,0]}/>
        <Mesh name="H01 reference engraving" geometry={g.headphoneLabel} material={m.wordmark} translation={[0,-.003,0]}/>
        <Mesh name="Channel marking" geometry={side<0?g.leftLabel:g.rightLabel} material={m.wordmark} translation={[0,-.033,0]}/>
      </Group>
      <Group name="Lower acoustic vent and cable socket" translation={[0,-.057,0]} rotation={q([1,0,0],Math.PI)}>
        <Mesh name="Recessed cable jack" geometry={g.port} material={m.blackMetal} scale={[.6,1,.6]}/><Mesh name="Cable jack opening" geometry={g.smallInner} material={m.inset} scale={[.6,1,.6]} translation={[0,-.002,0]}/>
      </Group>
    </Group>)}
  </Group>;
}

function Stand({a}:{a:Assets}) {
  const {geometry:g,material:m}=a;
  return <Group name="AURA S01 stand">
    <Mesh name="Weighted continuous-radius base" geometry={g.base} material={m.graphite} translation={[0,.013,0]}/>
    <Mesh name="Recessed nonslip base pad" geometry={g.baseInset} material={m.rubber} translation={[0,.003,0]}/>
    <Mesh name="Brushed aluminum structural spine" geometry={g.stem} material={m.metal} translation={[0,.172,-.029]}/>
    <Mesh name="Spine inset accent" geometry={g.stemInlay} material={m.blackMetal} translation={[0,.166,-.0163]}/>
    <Mesh name="Machined saddle cradle" geometry={g.saddleBase} material={m.metal} translation={[0,.328,0]}/>
    <Mesh name="Leather saddle contact pad" geometry={g.saddle} material={m.rubber} translation={[0,.3315,0]}/>
    <Mesh name="Laser-etched base logo" geometry={g.smallBrand} material={m.wordmark} translation={[0,.02215,.035]} rotation={top}/>
    <Mesh name="Stand model engraving" geometry={g.standLabel} material={m.wordmark} translation={[0,.0222,.045]} rotation={top}/>
    {([-1,1] as const).map(x=><Group key={x} translation={[x*.053,.0221,-.044]} rotation={top}><Fastener a={a}/></Group>)}
  </Group>;
}

function Dac({a}:{a:Assets}) {
  const {geometry:g,material:m}=a;
  return <Group name="AURA D01 desktop DAC" translation={[0,.009,0]}>
    <Mesh name="Extruded satin chassis" geometry={g.dac} material={m.graphite} translation={[0,.026,0]}/>
    <Mesh name="Precision brushed front fascia" geometry={g.front} material={m.metal} translation={[0,.026,.091]}/>
    <Mesh name="Perforated lid with 36 beveled through-slots" geometry={g.lid} material={m.graphite} translation={[0,.052,0]}/>
    <Mesh name="Rear connector panel" geometry={g.rear} material={m.blackMetal} translation={[0,.026,-.092]}/>
    <Mesh name="Recessed display perimeter" geometry={g.screen} material={m.blackMetal} translation={[-.012,.027,.094]}/>
    <Mesh name="High-resolution OLED volume readout" geometry={g.display} material={m.display} translation={[-.012,.027,.09485]}/>
    <Group name="112-flute machined volume encoder" translation={[.089,.027,.102]} rotation={front}>
      <Mesh name="Monolithic encoder body" geometry={g.knob} material={m.metal}/>
      <Mesh name="Integral fine knurl" geometry={g.knurl} material={m.metal}/>
      <Mesh name="Flat brushed control face" geometry={g.knobFace} material={m.graphite} translation={[0,.00865,0]}/>
      <Mesh name="Polished face chamfer" geometry={g.knobRim} material={m.silver} translation={[0,.0084,0]}/>
      <Mesh name="Amber index inlay" geometry={g.marker} material={m.orange} translation={[0,.00925,-.014]} rotation={top}/>
    </Group>
    <Mesh name="Encoder shadow reveal" geometry={g.knobRing} material={m.blackMetal} translation={[.089,.027,.094]} rotation={front}/>
    <Group name="Headphone output connectors" translation={[-.102,.027,.094]}>
      <Mesh name="Balanced 4.4 mm socket" geometry={g.balanced} material={m.blackMetal} rotation={front}/>
      <Mesh name="Balanced jack cavity" geometry={g.portInner} material={m.inset} translation={[0,0,-.001]} rotation={front}/>
      <Mesh name="Balanced socket label" geometry={g.balancedLabel} material={m.wordmark} translation={[0,-.014,0]}/>
      <Mesh name="6.35 mm socket" geometry={g.port} material={m.blackMetal} translation={[.024,0,0]} rotation={front}/>
      <Mesh name="6.35 mm jack cavity" geometry={g.smallInner} material={m.inset} translation={[.024,0,-.001]} rotation={front}/>
      <Mesh name="6.35 socket label" geometry={g.jackLabel} material={m.wordmark} translation={[.024,-.014,0]}/>
    </Group>
    <Mesh name="Input selector button" geometry={g.button} material={m.graphite} translation={[.046,.021,.095]} rotation={front}/>
    <Mesh name="Input label" geometry={g.inputLabel} material={m.wordmark} translation={[.046,.010,.094]}/>
    <Mesh name="Soft white status indicator" geometry={g.led} material={m.led} translation={[.046,.037,.094]} rotation={front}/>
    {([-1,1] as const).flatMap(x=>([-1,1] as const).map(z=><Group key={`${x}:${z}`} translation={[x*.099,-.0035,z*.064]}><Mesh name="Elastomer isolation foot" geometry={g.foot} material={m.rubber}/><Mesh name="Foot retaining ring" geometry={g.footRim} material={m.metal} translation={[0,.002,0]}/></Group>))}
    {[-.066,.066].map(x=><Mesh key={x} name="Recessed ventilation dust screen" geometry={g.ventBacking} material={m.fabric} translation={[x,.0511,-.039]} rotation={top}/>)}
    {([-1,1] as const).flatMap(side=>Array.from({length:26},(_,i)=><Mesh key={`${side}:${i}`} name="Chassis heat-dissipation rib" geometry={g.fin} material={m.blackMetal} translation={[side*.1273,.026,-.069+i*.0053]}/>))}
    <Group name="Rear I/O" translation={[0,0,-.094]} rotation={q([0,1,0],Math.PI)}>
      {[-.054,-.026].map((x,i)=><Group key={i} translation={[x,.026,0]}><Mesh name="RCA line-output gold contact" geometry={g.rca} material={m.metal} rotation={front}/><Mesh name="RCA channel identifier" geometry={g.rcaSleeve} material={i?m.red:m.white} rotation={front}/><Mesh name="Line-output cavity" geometry={g.smallInner} material={m.inset} scale={[.65,.65,.65]} rotation={front} translation={[0,0,-.002]}/></Group>)}
      <Mesh name="Line output legend" geometry={g.lineLabel} material={m.wordmark} translation={[-.04,.010,.0003]}/>
      <Mesh name="USB-C receptacle" geometry={g.usb} material={m.inset} translation={[.018,.026,0]}/><Mesh name="USB-C contact tongue" geometry={g.usbInner} material={m.silver} translation={[.018,.026,.0001]}/><Mesh name="USB-C legend" geometry={g.usbLabel} material={m.wordmark} translation={[.018,.012,.0003]}/>
      <Mesh name="DC supply jack" geometry={g.power} material={m.blackMetal} translation={[.063,.026,0]} rotation={front}/><Mesh name="DC opening" geometry={g.smallInner} material={m.inset} translation={[.063,.026,-.002]} rotation={front}/><Mesh name="DC center pin" geometry={g.pin} material={m.silver} translation={[.063,.026,0]} rotation={front}/><Mesh name="Power legend" geometry={g.powerLabel} material={m.wordmark} translation={[.063,.012,.0003]}/>
      {[-.111,.111].map(x=><Group key={x} translation={[x,.026,0]}><Fastener a={a}/></Group>)}
    </Group>
    <Mesh name="Etched top AURA wordmark" geometry={g.brand} material={m.wordmark} translation={[0,.0532,.033]} rotation={top}/>
    <Mesh name="D01 model engraving" geometry={g.dacLabel} material={m.wordmark} translation={[0,.0532,.044]} rotation={top}/>
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
      {product===Product.Dac?<Dac a={a}/>:product===Product.Studio?<Group name="Desktop companion" translation={[.275,0,.052]} rotation={q([0,1,0],-.10)}><Dac a={a}/></Group>:null}
      <PerspectiveCamera name="Product camera" yfov={.50} aspect={1} near={.01} far={100} translation={[.60,.48,.95]} rotation={q([1,0,0],-.30)} />
      <PointLight name="Soft accent source" color={[.8,.92,1]} intensity={.05} translation={[-.5,.6,.4]}/>
    </Scene>);
    return s;
  } catch(error){await s.dispose();throw error;}
}

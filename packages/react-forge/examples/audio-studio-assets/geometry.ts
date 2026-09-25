import type { GeometryInput, Quaternion, Vec3 } from "@delino/react-forge/glb";
const norm=(v:number[]):number[]=>{const n=Math.hypot(...v);return v.map(x=>x/n);};
export function quaternion(axis: Vec3, angle: number): Quaternion {const h=angle/2,s=Math.sin(h);return [axis[0]*s,axis[1]*s,axis[2]*s,Math.cos(h)];}
/** Rounded cuboid with split face UVs, analytical smooth normals and UV tangents. */
export function roundedBox(size: Vec3, radius: number, segments=12): GeometryInput {
  const p:number[]=[],n:number[]=[],t:number[]=[],uv:number[]=[],idx:number[]=[];
  const axes=[[0,1,2,1],[0,2,1,-1],[1,2,0,1],[1,0,2,-1],[2,0,1,1],[2,1,0,-1]];
  for (const [fixed,u,v,sign] of axes as [number,number,number,number][]) {
    const base=p.length/3;
    for(let j=0;j<=segments;j++)for(let i=0;i<=segments;i++) {
      // More samples near the bevel preserve broad planar faces efficiently.
      const sample=(k:number,h:number)=>{const s=k/segments;return s<0.5?-h+radius*(2*s)**2:h-radius*(2*(1-s))**2;};
      const pos=[0,0,0];pos[fixed]=size[fixed]!/2*sign;pos[u]=sample(i,size[u]!/2);pos[v]=sample(j,size[v]!/2);
      const inner=pos.map((x,k)=>Math.max(-size[k]!/2+radius,Math.min(size[k]!/2-radius,x)));
      const normal=norm(pos.map((x,k)=>x-inner[k]!));const vertex=inner.map((x,k)=>x+normal[k]!*radius);
      const tangent=norm(normal.map((x,k)=>(k===u?1:0)-x*normal[u]!));
      p.push(...vertex);n.push(...normal);t.push(...tangent,1);uv.push(pos[u]!/size[u]!+.5,pos[v]!/size[v]!+.5);
    }
    for(let j=0;j<segments;j++)for(let i=0;i<segments;i++){const a=base+j*(segments+1)+i,b=a+1,c=a+segments+1,d=c+1;idx.push(a,b,d,a,d,c);}
  }
  return {positions:new Float32Array(p),normals:new Float32Array(n),tangents:new Float32Array(t),uv:new Float32Array(uv),indices:new Uint32Array(idx)};
}
/** Surface of revolution about +Y; profile is ordered bottom to top around the section. */
export function lathe(profile: readonly (readonly [number,number])[],segments=96):GeometryInput {
  const p:number[]=[],n:number[]=[],t:number[]=[],uv:number[]=[],idx:number[]=[];
  for(let j=0;j<profile.length;j++) {
    const [r,y]=profile[j]!;const prev=profile[Math.max(0,j-1)]!,next=profile[Math.min(profile.length-1,j+1)]!;
    const dr=next[0]-prev[0],dy=next[1]-prev[1];
    for(let i=0;i<=segments;i++){const a=i/segments*Math.PI*2,s=Math.sin(a),c=Math.cos(a);p.push(r*s,y,r*c);n.push(...norm([dy*s,-dr,dy*c]));t.push(c,0,-s,-1);uv.push(i/segments,1-j/(profile.length-1));}
  }
  for(let j=0;j<profile.length-1;j++)for(let i=0;i<segments;i++){const a=j*(segments+1)+i,b=a+1,c=a+segments+1,d=c+1;if(profile[j]![0]>0)idx.push(a,b,d);if(profile[j+1]![0]>0)idx.push(a,d,c);}
  return {positions:new Float32Array(p),normals:new Float32Array(n),tangents:new Float32Array(t),uv:new Float32Array(uv),indices:new Uint32Array(idx)};
}
export function cylinder(radius:number,height:number,segments=96):GeometryInput {
  const b=Math.min(height*.15,radius*.06);
  return lathe([[0,-height/2],[radius-b,-height/2],[radius,-height/2+b],[radius,height/2-b],[radius-b,height/2],[0,height/2]],segments);
}
export function ring(outer:number,inner:number,depth:number,segments=96):GeometryInput {
  const b=Math.min((outer-inner)*.24,depth*.2);
  return lathe([[inner,-depth/2+b],[inner+b,-depth/2],[outer-b,-depth/2],[outer,-depth/2+b],[outer,depth/2-b],[outer-b,depth/2],[inner+b,depth/2],[inner,depth/2-b],[inner,-depth/2+b]],segments);
}
/** Elliptical swept band, with independent radial thickness and front/back width. */
export function arc(rx:number,ry:number,thickness:number,width:number,start=0,end=Math.PI,segments=112):GeometryInput {
  const p:number[]=[],n:number[]=[],t:number[]=[],uv:number[]=[],idx:number[]=[];const sides=16;
  for(let j=0;j<=segments;j++){const a=start+(end-start)*j/segments,c=Math.cos(a),s=Math.sin(a);const radial=norm([c/rx,s/ry,0]);const tangent=norm([-rx*s,ry*c,0]);
    for(let i=0;i<=sides;i++){const b=i/sides*Math.PI*2,cb=Math.cos(b),sb=Math.sin(b);p.push(rx*c+radial[0]!*thickness/2*cb,ry*s+radial[1]!*thickness/2*cb,width/2*sb);const normal=norm([radial[0]!*cb/thickness,radial[1]!*cb/thickness,sb/width]);n.push(...normal);const tan=norm(tangent.map((x,k)=>x-normal[k]!*tangent.reduce((v,x,k)=>v+x*normal[k]!,0)));t.push(...tan,1);uv.push(j/segments,i/sides);}
  }
  for(let j=0;j<segments;j++)for(let i=0;i<sides;i++){const a=j*(sides+1)+i,b=a+1,c=a+sides+1,d=c+1;idx.push(a,d,b,a,c,d);}
  for (const endCap of [false,true]) {
    const a=endCap?end:start,c=Math.cos(a),s=Math.sin(a),radial=norm([c/rx,s/ry,0]),normal=norm([-rx*s,ry*c,0]).map(x=>x*(endCap?1:-1));
    const base=p.length/3;p.push(rx*c,ry*s,0);n.push(...normal);t.push(...radial,1);uv.push(.5,.5);
    for(let i=0;i<=sides;i++){const b=i/sides*Math.PI*2,cb=Math.cos(b),sb=Math.sin(b);p.push(rx*c+radial[0]!*thickness/2*cb,ry*s+radial[1]!*thickness/2*cb,width/2*sb);n.push(...normal);t.push(...radial,1);uv.push(.5+.5*cb,.5+.5*sb);}
    for(let i=0;i<sides;i++){if(endCap)idx.push(base,base+i+2,base+i+1);else idx.push(base,base+i+1,base+i+2);}
  }
  return {positions:new Float32Array(p),normals:new Float32Array(n),tangents:new Float32Array(t),uv:new Float32Array(uv),indices:new Uint32Array(idx)};
}
export function plane(width:number,height:number):GeometryInput {
  return {positions:new Float32Array([-width/2,-height/2,0,width/2,-height/2,0,width/2,height/2,0,-width/2,height/2,0]),normals:new Float32Array([0,0,1,0,0,1,0,0,1,0,0,1]),tangents:new Float32Array([1,0,0,-1,1,0,0,-1,1,0,0,-1,1,0,0,-1]),uv:new Float32Array([0,1,1,1,1,0,0,0]),indices:new Uint32Array([0,1,2,0,2,3])};
}

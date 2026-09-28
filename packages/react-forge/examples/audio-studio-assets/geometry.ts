import type { GeometryInput, Quaternion, Vec3 } from "@delino/react-forge/glb";
const norm=(v:number[]):number[]=>{const n=Math.hypot(...v);return v.map(x=>x/n);};
export function quaternion(axis: Vec3, angle: number): Quaternion {const h=angle/2,s=Math.sin(h);return [axis[0]*s,axis[1]*s,axis[2]*s,Math.cos(h)];}
/** Rounded cuboid with split face UVs, analytical smooth normals and UV tangents. */
export function roundedBox(size: Vec3, radius: number, segments=12): GeometryInput {
  radius=Math.min(radius,...size.map(v=>v*.499));
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

type Point = readonly [number, number, number];
const unit = (v: Point): Point => { const d = Math.hypot(...v); return [v[0]/d,v[1]/d,v[2]/d]; };
const cross = (a: Point, b: Point): Point => [a[1]*b[2]-a[2]*b[1],a[2]*b[0]-a[0]*b[2],a[0]*b[1]-a[1]*b[0]];

/** Sample the actual curved surface, including its displaced normals and tangent frame. */
function surface(point: (u:number,v:number)=>Point, columns:number, rows:number):GeometryInput {
  const positions:number[]=[],normals:number[]=[],tangents:number[]=[],uv:number[]=[],indices:number[]=[];
  const h=0.00001;
  for(let j=0;j<=rows;j++)for(let i=0;i<=columns;i++) {
    const u=i/columns,v=j/rows,p=point(u,v),l=point(u-h,v),r=point(u+h,v),b=point(u,v-h),a=point(u,v+h);
    const du=unit([r[0]-l[0],r[1]-l[1],r[2]-l[2]]),dv=unit([a[0]-b[0],a[1]-b[1],a[2]-b[2]]),n=unit(cross(du,dv));
    positions.push(...p);normals.push(...n);tangents.push(...du,-1);uv.push(u,1-v);
  }
  for(let j=0;j<rows;j++)for(let i=0;i<columns;i++){const a=j*(columns+1)+i,b=a+1,c=a+columns+1,d=c+1;indices.push(a,b,d,a,d,c);}
  return {positions:new Float32Array(positions),normals:new Float32Array(normals),tangents:new Float32Array(tangents),uv:new Float32Array(uv),indices:new Uint32Array(indices)};
}

/** Quarter-circle bevels meet broad planar caps with tangent continuity. */
export function machinedCylinder(radius:number,height:number,bevel:number,segments=192):GeometryInput {
  const profile:[number,number][]=[[0,-height/2],[radius-bevel-.00001,-height/2]];
  for(let i=0;i<=12;i++){const a=i/12*Math.PI/2;profile.push([radius-bevel+Math.sin(a)*bevel,-height/2+bevel-Math.cos(a)*bevel]);}
  for(let i=0;i<=12;i++){const a=i/12*Math.PI/2;profile.push([radius-bevel+Math.cos(a)*bevel,height/2-bevel+Math.sin(a)*bevel]);}
  profile.push([radius-bevel-.00001,height/2],[0,height/2]);
  return lathe(profile,segments);
}

/** Soft, oval leather pad with small gathered folds near its sewn perimeter. */
export function cushion():GeometryInput {
  const soft=(v:number,p:number)=>Math.sign(v)*Math.abs(v)**p;
  return surface((u,v)=>{
    const a=u*Math.PI*2,b=v*Math.PI*2;
    const fold=(Math.sin(a*61+.5*Math.sin(a*13))+.32*Math.sin(a*113))*.00018*(.3+.7*Math.abs(Math.cos(b))**6);
    const r=.038+.0105*soft(Math.cos(b),.72)+fold;
    return [r*Math.sin(a)*1.24,.0118*soft(Math.sin(b),.72),r*Math.cos(a)];
  },256,64);
}

export function torus(radius:number,tube:number,oval=1,segments=192):GeometryInput {
  return surface((u,v)=>{const a=u*Math.PI*2,b=v*Math.PI*2,r=radius+tube*Math.cos(b);return [r*Math.sin(a)*oval,tube*Math.sin(b),r*Math.cos(a)];},segments,12);
}

/** Rounded flute ridges are part of the metal surface, rather than floating bars. */
export function flutedGrip(radius:number,height:number,flutes=112):GeometryInput {
  return surface((u,v)=>{
    const a=u*Math.PI*2,r=radius+.00022*(.5+.5*Math.cos(a*flutes));
    return [r*Math.sin(a),(v-.5)*height,r*Math.cos(a)];
  },flutes*8,2);
}

/** A capped tube follows a sampled centerline; useful for stitching and cables. */
export function tube(path:readonly Point[],radius:number,sides=8):GeometryInput {
  const p:number[]=[],n:number[]=[],t:number[]=[],uv:number[]=[],idx:number[]=[];
  for(let j=0;j<path.length;j++) {
    const prev=path[Math.max(0,j-1)]!,next=path[Math.min(path.length-1,j+1)]!,center=path[j]!;
    const tangent=unit([next[0]-prev[0],next[1]-prev[1],next[2]-prev[2]]);
    const reference:Point=Math.abs(tangent[2])>.95?[0,1,0]:[0,0,1];
    const axis=unit(cross(tangent,reference)),binormal=cross(axis,tangent);
    for(let i=0;i<=sides;i++){const a=i/sides*Math.PI*2,normal=axis.map((x,k)=>x*Math.cos(a)+binormal[k]!*Math.sin(a)) as unknown as Point;p.push(...center.map((x,k)=>x+radius*normal[k]!));n.push(...normal);t.push(...tangent,1);uv.push(j/(path.length-1),i/sides);}
  }
  for(let j=0;j<path.length-1;j++)for(let i=0;i<sides;i++){const a=j*(sides+1)+i,b=a+1,c=a+sides+1,d=c+1;idx.push(a,d,b,a,c,d);}
  for(const end of [0,path.length-1]) {
    const center=path[end]!,neighbor=path[end===0?1:end-1]!,normal=unit([center[0]-neighbor[0],center[1]-neighbor[1],center[2]-neighbor[2]]),tangent=unit(cross(normal,Math.abs(normal[2])>.95?[0,1,0]:[0,0,1])),base=p.length/3;
    p.push(...center);n.push(...normal);t.push(...tangent,1);uv.push(.5,.5);
    for(let i=0;i<=sides;i++){const at=(end*(sides+1)+i)*3;p.push(p[at]!,p[at+1]!,p[at+2]!);n.push(...normal);t.push(...tangent,1);uv.push(.5+.5*Math.cos(i/sides*Math.PI*2),.5+.5*Math.sin(i/sides*Math.PI*2));}
    for(let i=0;i<sides;i++){if(end===0)idx.push(base,base+i+1,base+i+2);else idx.push(base,base+i+2,base+i+1);}
  }
  return {positions:new Float32Array(p),normals:new Float32Array(n),tangents:new Float32Array(t),uv:new Float32Array(uv),indices:new Uint32Array(idx)};
}

export function merge(parts:readonly GeometryInput[]):GeometryInput {
  const p:number[]=[],n:number[]=[],t:number[]=[],uv:number[]=[],idx:number[]=[];
  for(const part of parts){const base=p.length/3;for(const x of part.positions)p.push(x);for(const x of part.normals)n.push(x);for(const x of part.tangents!)t.push(x);for(const x of part.uv!)uv.push(x);for(const x of part.indices)idx.push(base+x);}
  return {positions:new Float32Array(p),normals:new Float32Array(n),tangents:new Float32Array(t),uv:new Float32Array(uv),indices:new Uint32Array(idx)};
}

export function stitches(radius:number,oval=1,count=144):GeometryInput {
  return merge(Array.from({length:count},(_,i)=>tube(Array.from({length:5},(_,k)=>{const a=(i+(k/4)*.54)/count*Math.PI*2;return [radius*Math.sin(a)*oval,0,radius*Math.cos(a)] as Point;}),.00014,6)));
}

export function label(width:number,height:number,row:number,rows=16):GeometryInput {
  const labels=['AURA','H01 / REFERENCE','D01 / DESKTOP DAC','S01 / SUPPORT','BALANCED','6.35 MM','USB-C','LINE OUT','DC 12V','DESIGNED FOR LISTENING','SERIAL / 0001','L','R','POWER','INPUT','AURA / AUDIO LAB'];
  const size=Math.min(7,1700/(labels[row]!.length*10)),span=(labels[row]!.length*10-4)*size+size;
  const left=.5-span/4096,right=.5+span/4096,top=row/rows+(128-11*size)/4096,bottom=top+11*size/2048;
  const g=plane(width,height);g.uv=new Float32Array([left,bottom,right,bottom,right,top,left,top]);return g;
}

/** Closed aluminum lid with real rounded ventilation apertures and beveled walls. */
export function perforatedLid():GeometryInput {
  const p:number[]=[],n:number[]=[],t:number[]=[],uv:number[]=[],idx:number[]=[];
  type Vertex={p:Point;n:Point};
  const vertex=(x:number,y:number,z:number,normal:Point):Vertex=>({p:[x,y,z],n:normal});
  function quad(v:Vertex[]){
    const base=p.length/3;
    for(const a of v){p.push(...a.p);n.push(...a.n);const ref:Point=Math.abs(a.n[0])>.9?[0,0,1]:[1,0,0],dot=ref[0]*a.n[0]+ref[1]*a.n[1]+ref[2]*a.n[2];t.push(...unit([ref[0]-dot*a.n[0],ref[1]-dot*a.n[1],ref[2]-dot*a.n[2]]),1);uv.push(a.p[0]/.241+.5,a.p[2]/.169+.5);}
    for(const triangle of [[0,1,2],[0,2,3]]) {
      const a=v[triangle[0]!]!,b=v[triangle[1]!]!,c=v[triangle[2]!]!,ab=b.p.map((x,k)=>x-a.p[k]!) as unknown as Point,ac=c.p.map((x,k)=>x-a.p[k]!) as unknown as Point,face=cross(ab,ac);
      if(Math.hypot(...face)<1e-18)continue;
      const dot=face.reduce((sum,x,k)=>sum+x*(a.n[k]!+b.n[k]!+c.n[k]!),0);
      const [i,j,k]=triangle.map(i=>base+i);
      if(dot>=0)idx.push(i!,j!,k!);else idx.push(i!,k!,j!);
    }
  }
  const rect=(x0:number,x1:number,z0:number,z1:number)=>{for(const sign of [-1,1])quad([[x0,z0],[x1,z0],[x1,z1],[x0,z1]].map(([x,z])=>vertex(x!,sign*.001,z!,[0,sign,0])));};
  const xs=[-.1145,-.09,-.042,.042,.09,.1145],zs=[-.0785,-.068,-.0104,.0785];
  for(let xi=0;xi<5;xi++)for(let zi=0;zi<3;zi++){if(zi===1&&(xi===1||xi===3))continue;rect(xs[xi]!,xs[xi+1]!,zs[zi]!,zs[zi+1]!);}
  const outline=(w:number,d:number,r:number,steps=12)=>Array.from({length:4*(steps+1)},(_,i)=>{const corner=Math.floor(i/(steps+1)),a=corner*Math.PI/2+(i%(steps+1))/steps*Math.PI/2,c=Math.cos(a),s=Math.sin(a),sx=corner===0||corner===3?1:-1,sz=corner<2?1:-1;return {x:sx*(w/2-r)+r*c,z:sz*(d/2-r)+r*s,nx:c,nz:s,sx,sz};});
  for(const cx of [-.066,.066])for(let row=0;row<18;row++){
    const cz=-.0664+row*.0032,edge=outline(.042,.0014,.0007);
    for(let i=0;i<edge.length;i++){
      const a=edge[i]!,b=edge[(i+1)%edge.length]!;
      for(const sign of [-1,1]){
        const outer=(v:typeof a)=>vertex(cx+v.sx*.024,sign*.001,cz+v.sz*.0016,[0,sign,0]);
        const rim=(v:typeof a)=>vertex(cx+v.x+v.nx*.0002,sign*.001,cz+v.z+v.nz*.0002,[0,sign,0]);
        const wall=(v:typeof a)=>vertex(cx+v.x,sign*.0008,cz+v.z,[-v.nx,0,-v.nz]);
        quad([outer(a),outer(b),rim(b),rim(a)]);quad([rim(a),rim(b),wall(b),wall(a)]);
      }
      quad([vertex(cx+a.x,-.0008,cz+a.z,[-a.nx,0,-a.nz]),vertex(cx+b.x,-.0008,cz+b.z,[-b.nx,0,-b.nz]),vertex(cx+b.x,.0008,cz+b.z,[-b.nx,0,-b.nz]),vertex(cx+a.x,.0008,cz+a.z,[-a.nx,0,-a.nz])]);
    }
  }
  const edge=outline(.241,.169,.006,24);
  for(let i=0;i<edge.length;i++){
    const a=edge[i]!,b=edge[(i+1)%edge.length]!;
    for(const sign of [-1,1]){
      const inner=(v:typeof a)=>vertex(v.sx*.1145,sign*.001,v.sz*.0785,[0,sign,0]);
      const rim=(v:typeof a)=>vertex(v.x-v.nx*.00035,sign*.001,v.z-v.nz*.00035,[0,sign,0]);
      const wall=(v:typeof a)=>vertex(v.x,sign*.00065,v.z,[v.nx,0,v.nz]);
      quad([inner(a),inner(b),rim(b),rim(a)]);quad([rim(a),rim(b),wall(b),wall(a)]);
    }
    quad([vertex(a.x,-.00065,a.z,[a.nx,0,a.nz]),vertex(b.x,-.00065,b.z,[b.nx,0,b.nz]),vertex(b.x,.00065,b.z,[b.nx,0,b.nz]),vertex(a.x,.00065,a.z,[a.nx,0,a.nz])]);
  }
  return {positions:new Float32Array(p),normals:new Float32Array(n),tangents:new Float32Array(t),uv:new Float32Array(uv),indices:new Uint32Array(idx)};
}

/** Planar end-cap UVs keep woven acoustic fabric from pinching at the lathe pole. */
export function planarCaps(g:GeometryInput,radius:number,height:number):GeometryInput {
  for(let i=0;i<g.positions.length/3;i++) {
    if(Math.abs(Math.abs(g.positions[i*3+1]!)-height/2)>1e-8)continue;
    const nx=g.normals[i*3]!,ny=g.normals[i*3+1]!,nz=g.normals[i*3+2]!;
    g.uv![i*2]=.5+g.positions[i*3]!/(2*radius);
    g.uv![i*2+1]=.5+g.positions[i*3+2]!/(2*radius);
    g.tangents!.set([...unit([1-nx*nx,-nx*ny,-nx*nz]),ny>0?-1:1],i*4);
  }
  return g;
}

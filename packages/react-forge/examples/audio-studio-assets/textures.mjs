// Original deterministic texture authoring. No fetched imagery or external fonts.
import { deflateSync } from 'node:zlib';
import { writeFileSync } from 'node:fs';
import { fileURLToPath } from 'node:url';
function crc(bytes) {let c=0xffffffff;for(const b of bytes){c^=b;for(let i=0;i<8;i++)c=(c>>>1)^((c&1)?0xedb88320:0);}return (c^0xffffffff)>>>0;}
function chunk(name,data){const type=Buffer.from(name),len=Buffer.alloc(4),sum=Buffer.alloc(4);len.writeUInt32BE(data.length);sum.writeUInt32BE(crc(Buffer.concat([type,data])));return Buffer.concat([len,type,data,sum]);}
function png(name,w,h,pixel){const rows=Buffer.alloc((w*4+1)*h);for(let y=0;y<h;y++)for(let x=0;x<w;x++){const p=pixel(x,y),at=y*(w*4+1)+1+x*4;for(let i=0;i<4;i++)rows[at+i]=Math.max(0,Math.min(255,Math.round(p[i]??255)));}const header=Buffer.alloc(13);header.writeUInt32BE(w);header.writeUInt32BE(h,4);header[8]=8;header[9]=6;writeFileSync(fileURLToPath(new URL(name,import.meta.url)),Buffer.concat([Buffer.from([137,80,78,71,13,10,26,10]),chunk('IHDR',header),chunk('IDAT',deflateSync(rows,{level:9})),chunk('IEND',Buffer.alloc(0))]));}
const noise=(x,y)=>{let n=Math.imul(x,374761393)+Math.imul(y,668265263);n=Math.imul(n^(n>>>13),1274126177);return ((n^(n>>>16))>>>0)/4294967295;};
const periodic=(x,y)=>noise((x+2048)%2048,(y+2048)%2048);
png('brushed-roughness.png',2048,2048,(x,y)=>{const v=207+7*periodic(0,y)+3*periodic(x,y);return [v,v,v,255];});
png('brushed-normal.png',2048,2048,(x,y)=>[128,128+1.5*(periodic(0,y+1)-periodic(0,y-1)),255,255]);
// Periodic cellular grain gives leather irregular pores instead of white noise.
function hide(x,y){const cell=11,gx=Math.floor(x/cell),gy=Math.floor(y/cell);let d=99;for(let j=-1;j<=1;j++)for(let i=-1;i<=1;i++){const cx=gx+i,cy=gy+j,px=cx*cell+noise((cx+187)%187,(cy+187)%187)*cell,py=cy*cell+noise((cy+187)%187,(cx+187)%187)*cell;d=Math.min(d,Math.hypot(x-px,y-py));}return Math.min(1,d/5)**.55+.11*periodic(x,y);}
const hideSize=2048,heights=new Float32Array(hideSize*hideSize);
for(let y=0;y<hideSize;y++)for(let x=0;x<hideSize;x++)heights[y*hideSize+x]=hide(x,y);
const height=(x,y)=>heights[((y+hideSize)%hideSize)*hideSize+(x+hideSize)%hideSize];
png('leather-roughness.png',hideSize,hideSize,(x,y)=>{const v=177+40*height(x,y);return [v,v,v,255];});
png('leather-normal.png',hideSize,hideSize,(x,y)=>{const dx=(height(x+1,y)-height(x-1,y))*.30,dy=(height(x,y+1)-height(x,y-1))*.30,n=Math.hypot(dx,dy,1);return [128-dx/n*127,128+dy/n*127,128+127/n,255];});
const weave=(x,y)=>Math.sin(x*Math.PI/5)*Math.cos(y*Math.PI/5)*.2+Math.sin(x*Math.PI/2.5)*.025;
png('fabric-normal.png',1024,1024,(x,y)=>[128+(weave(x-1,y)-weave(x+1,y))*30,128+(weave(x,y+1)-weave(x,y-1))*30,254,255]);
// A small original monoline alphabet. Paths use a 6 by 10 design grid and
// distance-field coverage, retaining smooth edges at close viewing distances.
const glyph={
 A:[[[0,10],[3,0],[6,10]],[[1.2,6.3],[4.8,6.3]]],B:[[[0,10],[0,0],[3.7,0],[5.7,1.5],[5.7,3.4],[3.8,5],[0,5]],[[3.8,5],[6,6.7],[6,8.4],[4,10],[0,10]]],
 C:[[[6,1],[4.5,0],[1.5,0],[0,1.5],[0,8.5],[1.5,10],[4.5,10],[6,9]]],D:[[[0,0],[3.7,0],[6,2.4],[6,7.6],[3.7,10],[0,10],[0,0]]],E:[[[6,0],[0,0],[0,10],[6,10]],[[0,5],[4.5,5]]],F:[[[6,0],[0,0],[0,10]],[[0,5],[4.5,5]]],
 G:[[[6,1],[4.5,0],[1.5,0],[0,1.5],[0,8.5],[1.5,10],[4.5,10],[6,8.5],[6,5.4],[3.3,5.4]]],H:[[[0,0],[0,10]],[[6,0],[6,10]],[[0,5],[6,5]]],I:[[[3,0],[3,10]]],J:[[[6,0],[6,8.5],[4.5,10],[1.5,10],[0,8]]],K:[[[0,0],[0,10]],[[6,0],[0,5],[6,10]]],L:[[[0,0],[0,10],[6,10]]],M:[[[0,10],[0,0],[3,5],[6,0],[6,10]]],N:[[[0,10],[0,0],[6,10],[6,0]]],
 O:[[[1.5,0],[4.5,0],[6,1.5],[6,8.5],[4.5,10],[1.5,10],[0,8.5],[0,1.5],[1.5,0]]],P:[[[0,10],[0,0],[4,0],[6,1.5],[6,3.5],[4,5],[0,5]]],Q:[[[1.5,0],[4.5,0],[6,1.5],[6,8.5],[4.5,10],[1.5,10],[0,8.5],[0,1.5],[1.5,0]],[[3.5,7.5],[6.5,11]]],R:[[[0,10],[0,0],[4,0],[6,1.5],[6,3.5],[4,5],[0,5]],[[3.2,5],[6,10]]],
 S:[[[6,1],[4.5,0],[1.5,0],[0,1.5],[0,3.5],[1.5,5],[4.5,5],[6,6.5],[6,8.5],[4.5,10],[1.5,10],[0,9]]],T:[[[0,0],[6,0]],[[3,0],[3,10]]],U:[[[0,0],[0,8.5],[1.5,10],[4.5,10],[6,8.5],[6,0]]],V:[[[0,0],[3,10],[6,0]]],W:[[[0,0],[1,10],[3,5],[5,10],[6,0]]],X:[[[0,0],[6,10]],[[6,0],[0,10]]],Y:[[[0,0],[3,5],[6,0]],[[3,5],[3,10]]],Z:[[[0,0],[6,0],[0,10],[6,10]]],
 '0':[[[1.5,0],[4.5,0],[6,1.5],[6,8.5],[4.5,10],[1.5,10],[0,8.5],[0,1.5],[1.5,0]]],'1':[[[1,2],[3,0],[3,10]]],'2':[[[0,1.5],[1.5,0],[4.5,0],[6,1.5],[6,3.5],[0,10],[6,10]]],'3':[[[0,0],[6,0],[3,4.5],[5,5],[6,6.5],[6,8.5],[4.5,10],[1,10],[0,9]]],'4':[[[4.5,10],[4.5,0],[0,7],[6,7]]],'5':[[[6,0],[0,0],[0,4.7],[4.5,4.7],[6,6.2],[6,8.5],[4.5,10],[1,10],[0,9]]],'6':[[[5.5,0],[2,0],[0,3],[0,8.5],[1.5,10],[4.5,10],[6,8.5],[6,6],[4.5,4.5],[0,4.5]]],'7':[[[0,0],[6,0],[1,10]]],'8':[[[1.5,0],[4.5,0],[6,1.5],[6,3.5],[4.5,5],[1.5,5],[0,3.5],[0,1.5],[1.5,0]],[[1.5,5],[0,6.5],[0,8.5],[1.5,10],[4.5,10],[6,8.5],[6,6.5],[4.5,5]]],'9':[[[6,5.5],[1.5,5.5],[0,4],[0,1.5],[1.5,0],[4.5,0],[6,1.5],[6,7],[4,10],[.5,10]]],
 '-':[[[.5,5],[5.5,5]]],'/':[[[0,10],[6,0]]],'.':[[[3,9.7],[3,10]]],':':[[[3,3],[3,3.3]],[[3,7],[3,7.3]]],'+':[[[0,5],[6,5]],[[3,2],[3,8]]]
};
function graphics(w,h,bg){const pixels=new Uint8ClampedArray(w*h*4);for(let i=0;i<w*h;i++)pixels.set(bg,i*4);return {w,h,pixels};}
function line(g,x1,y1,x2,y2,width,color){const dx=x2-x1,dy=y2-y1,len=dx*dx+dy*dy;for(let y=Math.max(0,Math.floor(Math.min(y1,y2)-width));y<Math.min(g.h,Math.ceil(Math.max(y1,y2)+width));y++)for(let x=Math.max(0,Math.floor(Math.min(x1,x2)-width));x<Math.min(g.w,Math.ceil(Math.max(x1,x2)+width));x++){const t=len?Math.max(0,Math.min(1,((x-x1)*dx+(y-y1)*dy)/len)):0,d=Math.hypot(x-x1-t*dx,y-y1-t*dy),alpha=Math.max(0,Math.min(1,width/2+.5-d)),at=(y*g.w+x)*4;for(let c=0;c<4;c++)g.pixels[at+c]=g.pixels[at+c]*(1-alpha)+color[c]*alpha;}}
function text(g,value,x,y,size,color,width=size*.55,spacing=9){for(const ch of value){for(const path of glyph[ch]??[])for(let i=1;i<path.length;i++)line(g,x+path[i-1][0]*size,y+path[i-1][1]*size,x+path[i][0]*size,y+path[i][1]*size,width,color);x+=spacing*size;}}
function save(name,g){png(name,g.w,g.h,(x,y)=>g.pixels.subarray((y*g.w+x)*4,(y*g.w+x)*4+4));}
const display=graphics(2048,768,[4,9,12,255]),white=[218,232,224,255],muted=[95,137,136,255],accent=[218,164,101,255];
text(display,'USB / PCM',105,70,9,muted,4);text(display,'-24.5',95,230,31,white,12,8);text(display,'DB',1350,410,9,muted,4);
text(display,'192 KHZ / 24 BIT',105,628,7,muted,3);text(display,'AURA',1570,72,10,accent,4,10);
for(let i=0;i<32;i++){const x=1500+i*13,top=310+80*Math.sin(i*.25)+30*Math.sin(i*.71);line(display,x,top,x,550,5,i>25?accent:muted);}
line(display,100,590,1948,590,1.5,[34,55,57,255]);save('display.png',display);
const atlas=graphics(2048,2048,[0,0,0,0]);
const labels=['AURA','H01 / REFERENCE','D01 / DESKTOP DAC','S01 / SUPPORT','BALANCED','6.35 MM','USB-C','LINE OUT','DC 12V','DESIGNED FOR LISTENING','SERIAL / 0001','L','R','POWER','INPUT','AURA / AUDIO LAB'];
for(let i=0;i<labels.length;i++){const value=labels[i],s=Math.min(7,1700/(value.length*10)),x=(2048-(value.length*10-4)*s)/2;text(atlas,value,x,i*128+(128-10*s)/2,s,[193,172,137,255],s*.48,10);}
save('wordmark.png',atlas);
console.log(JSON.stringify({event:'aura_texture_sources',textures:7,largestDimension:2048}));

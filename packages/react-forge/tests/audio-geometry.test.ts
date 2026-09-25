import assert from 'node:assert/strict';
import test from 'node:test';
import { cushion, machinedCylinder, roundedBox, stitches, tube, flutedGrip, perforatedLid, planarCaps } from '../examples/audio-studio-assets/geometry.js';

test('curved audio geometry has outward winding and orthonormal tangent frames', () => {
  const shapes = {
    cushion: cushion(),
    acousticLiner: planarCaps(machinedCylinder(.029,.002,.0008),.029,.002),
    ventedLid: perforatedLid(),
    encoder: machinedCylinder(.022, .017, .0015),
    thinPlate: roundedBox([.25, .002, .17], .007),
    stitch: stitches(.047, 1.24, 12),
    bentTube: tube([[0,0,0],[.005,.01,0],[.012,.017,.003]], .0002),
    grip: flutedGrip(.022,.013),
  };
  for (const [name, g] of Object.entries(shapes)) {
    for (const array of [g.positions,g.normals,g.tangents!,g.uv!]) assert.ok(array.every(Number.isFinite), name);
    for (let i=0;i<g.normals.length/3;i++) {
      const n=g.normals.subarray(i*3,i*3+3),t=g.tangents!.subarray(i*4,i*4+3);
      assert.ok(Math.abs(Math.hypot(...n)-1)<1e-5, `${name}: normal length`);
      assert.ok(Math.abs(Math.hypot(...t)-1)<1e-5, `${name}: tangent length`);
      assert.ok(Math.abs(n[0]!*t[0]!+n[1]!*t[1]!+n[2]!*t[2]!)<1e-5, `${name}: tangent orthogonality`);
    }
    for(let i=0;i<g.indices.length;i+=3){
      const ids=[g.indices[i]!,g.indices[i+1]!,g.indices[i+2]!];
      const [a,b,c]=ids.map(id=>g.positions.subarray(id*3,id*3+3));
      const ab=[b![0]!-a![0]!,b![1]!-a![1]!,b![2]!-a![2]!],ac=[c![0]!-a![0]!,c![1]!-a![1]!,c![2]!-a![2]!];
      const cross=[ab[1]!*ac[2]!-ab[2]!*ac[1]!,ab[2]!*ac[0]!-ab[0]!*ac[2]!,ab[0]!*ac[1]!-ab[1]!*ac[0]!];
      const dot=ids.reduce((sum,id)=>sum+cross.reduce((v,x,k)=>v+x*g.normals[id*3+k]!,0),0);
      assert.ok(dot>=-1e-12, `${name}: reversed triangle ${i/3}`);
    }
  }
});

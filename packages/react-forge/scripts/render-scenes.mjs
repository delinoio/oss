import { spawnSync } from 'node:child_process';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { resolve, join } from 'node:path';
import { fileURLToPath } from 'node:url';
const root=fileURLToPath(new URL('../../../',import.meta.url));
const pkg=fileURLToPath(new URL('../',import.meta.url));
const at=process.argv.indexOf('--output');
if(at<0||!process.argv[at+1])throw Error('Use --output <directory>');
const output=resolve(process.argv[at+1]);mkdirSync(output,{recursive:true});
const blender=process.env.REACT_FORGE_BLENDER??'blender';
const deviceAt=process.argv.indexOf('--device');const device=deviceAt<0?'CPU':process.argv[deviceAt+1];
if(!['CPU','METAL'].includes(device))throw Error('Expected CPU or METAL device');
function integerOption(name,fallback,min,max){const i=process.argv.indexOf(name),value=i<0?fallback:Number(process.argv[i+1]);if(!Number.isInteger(value)||value<min||value>max)throw Error(`Invalid ${name}`);return String(value);}
const resolution=integerOption('--resolution',2048,2048,8192);
const samples=integerOption('--samples',96,1,4096);
const heroResolution=integerOption('--hero-resolution',0,0,8192);
if(Number(heroResolution)>0&&Number(heroResolution)<3072)throw Error('Hero resolution must preserve a 2048-pixel short edge');
function run(command,args,options={}) {const r=spawnSync(command,args,{cwd:pkg,stdio:'inherit',...options});if(r.error||r.status!==0)throw Error('Scene verification subprocess failed');return r;}
const version=run(blender,['--version'],{encoding:'utf8',stdio:'pipe'}).stdout;
if(!version.includes('Blender 4.5.14'))throw Error('Scene evidence requires Blender 4.5.14 LTS');
run(process.execPath,['--import','tsx','scripts/generate-scene-examples.mjs','--output',output]);
run(process.execPath,['--import','tsx','scripts/generate-scene-profile.mjs',join(output,'profile')]);
run(blender,['--background','--factory-startup','--python-exit-code','1','--python',join(pkg,'scripts/inspect-scene-profile.py'),'--',join(output,'profile')]);
const fixtures=[...['studio','headphones','dac','stand'].map(p=>join(output,`aura-${p}.fbx`)),join(output,'profile/profile.fbx')];
const independent=run('cargo',['run','--locked','--quiet','-p','forge-fbx','--example','inspect','--',...fixtures],{cwd:root,stdio:['ignore','pipe','inherit'],encoding:'utf8'}).stdout;
writeFileSync(join(output,'ufbx-report.json'),independent);
run(blender,['--background','--factory-startup','--python-exit-code','1','--python',join(pkg,'scripts/inspect-scenes.py'),'--','--input',output]);
if(process.argv.includes('--prepare-only')) {
  // CI distributes these validated inputs to independent CPU render jobs.
  // Preparation is deliberately not reported as completed visual evidence.
  console.log(JSON.stringify({event:'react_forge_scene_prepared',imports:8,blender:'4.5.14'}));
} else {
  run(blender,['--background','--factory-startup','--python-exit-code','1','--python',join(pkg,'scripts/render-scenes.py'),'--','--input',output,'--output',join(output,'renders'),'--device',device,'--resolution',resolution,'--samples',samples,'--hero-resolution',heroResolution]);
  const report=JSON.parse(readFileSync(join(output,'renders/render-report.json'),'utf8'));
  if(report.imports.length!==8||report.imports.some(i=>i.renders.length!==4))throw Error('Incomplete visual evidence');
  console.log(JSON.stringify({event:'react_forge_scene_verification',imports:8,renders:32,blender:'4.5.14'}));
}

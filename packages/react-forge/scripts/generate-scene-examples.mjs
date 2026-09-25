import { mkdir, writeFile, readFile } from 'node:fs/promises';
import { resolve, join } from 'node:path';
import { createHash } from 'node:crypto';
import validator from 'gltf-validator';
import task, { Product } from '../examples/audio-studio.tsx';
const at=process.argv.indexOf('--output');
if(at<0||!process.argv[at+1])throw new Error('Use --output <directory>');
const output=resolve(process.argv[at+1]);await mkdir(output,{recursive:true});
const report={node:process.version,artifacts:[]};
for(const product of Object.values(Product))for(const format of ['glb','fbx']){
  const session=await task({data:{format,product}});
  try{
    const snapshot=await session.snapshot();const name=`aura-${product}.${format}`;const start=performance.now();
    await session.exportFile(join(output,name),{overwrite:true});const bytes=await readFile(join(output,name));
    const validation=format==='glb'?await validator.validateBytes(bytes):undefined;
    if(validation?.issues.numErrors)throw new Error(JSON.stringify(validation.issues));
    const bounds=await session.measure(snapshot.targets[0],{revision:snapshot.revision});
    report.artifacts.push({file:name,bytes:bytes.length,sha256:createHash('sha256').update(bytes).digest('hex'),revision:snapshot.revision,nodes:snapshot.targets.length,meshes:snapshot.targets.filter(t=>t.kind==='mesh').length,bounds,exportMs:performance.now()-start,validator:validation?.issues});
    console.log(JSON.stringify({product,format,bytes:bytes.length,meshes:snapshot.targets.filter(t=>t.kind==='mesh').length,validatorErrors:validation?.issues.numErrors}));
  }finally{await session.dispose();}
}
await writeFile(join(output,'generation.json'),JSON.stringify(report,null,2)+'\n');

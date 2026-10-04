// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from 'node:child_process';
import { createReadStream, copyFileSync, existsSync, lstatSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { createHash } from 'node:crypto';
import { basename, dirname, join, resolve } from 'node:path';
import { tmpdir } from 'node:os';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { targets, selectTarget, cefCredits, verifyNativePayload, verifyNotices, packageResources } from './native-package.mjs';
import { nativeEnvironment } from './bundle-native-dry-run.mjs';
import { artifactName } from '../../../scripts/release/generate-delidev-updater.mjs';
export function updaterTarget(selected) {return `${selected.platform==='win32'?'windows':selected.platform}-${selected.arch==='x64'?'amd64':'arm64'}`;}
export async function main(args) {
 if(args.length!==2||args[0]!=='--target')throw new Error('Select one exact native target.');
 const selected=selectTarget(args[1],process.platform,process.arch),target=updaterTarget(selected),app=fileURLToPath(new URL('..',import.meta.url)),root=resolve(app,'../..');
 const env=nativeEnvironment(process.env,process.platform);
 const run=(program,args,inherit=false,cwd=app)=>{const result=spawnSync(program,args,{cwd,env,encoding:'utf8',timeout:120*60*1000,...(inherit?{stdio:'inherit'}:{})});if(result.error||result.status!==0)throw new Error('DeliDev updater input build failed.');return inherit?'':result.stdout+result.stderr;};
 const revision=run('git',['rev-parse','HEAD']).trim();if(!/^[a-f0-9]{40}$/.test(revision)||run('git',['status','--porcelain','--untracked-files=normal']).trim())throw new Error('Commit reconciled source before building updater inputs.');
 const version=JSON.parse(readFileSync(join(app,'src-tauri/tauri.conf.json'),'utf8')).version;
 const output=join(root,'target/delidev-updater-input',selected.target,revision);if(existsSync(output))throw new Error('Retain the original updater input; do not replace it.');mkdirSync(dirname(output),{recursive:true});
 // Existing dry-run verification preserves all native code, original notices,
 // WidgetKit resources and matching architecture before update-format assembly.
 const prior=join(root,'target/delidev-dry-run',selected.target,revision);if(!existsSync(prior))run(process.execPath,[join(app,'scripts/bundle-native-dry-run.mjs'),'--target',selected.target],true);
 const stage=mkdtempSync(join(dirname(output),'.pending-')),scratch=mkdtempSync(join(tmpdir(),'delidev-update-input-'));
 try {
 const worker=join(stage,artifactName('worker',target));copyFileSync(join(app,`src-tauri/binaries/delidev-${selected.target}${selected.platform==='win32'?'.exe':''}`),worker);
 const desktop=join(stage,artifactName('desktop',target));
 if(selected.platform==='darwin'){
   const bundle=join(root,'target/release/bundle/macos/DeliDev.app');run('/usr/bin/ditto',[bundle,join(scratch,'DeliDev.app')]);run('/usr/bin/hdiutil',['create','-fs','HFS+','-format','UDZO','-volname','DeliDev','-srcfolder',scratch,desktop]);
 }else{
   const kind=selected.platform==='win32'?'nsis':'appimage',credits=cefCredits(selected,env);
   const config=JSON.stringify({bundle:{resources:{[credits]:'notices/Chromium-CREDITS.html'}}});
   run('cargo',['run','--locked','--manifest-path','src-tauri/Cargo.toml','--features','cli','--bin','delidev-tauri-cli','--','build','--target',selected.target,'--bundles',kind,'--features','desktop-host,custom-protocol,tauri/cef','--config',config],true);
   const directory=join(root,'target',selected.target,'release/bundle',kind),extension=selected.platform==='win32'?'.exe':'.AppImage';
   const files=readdirSync(directory).filter(v=>v.endsWith(extension));if(files.length!==1)throw new Error('One unambiguous native updater artifact is required.');copyFileSync(join(directory,files[0]),desktop);
   if(selected.platform==='linux'){run(desktop,['--appimage-extract'],false,scratch);const payload=join(scratch,'squashfs-root');verifyNativePayload(payload,selected);verifyNotices(join(payload,'usr/lib/DeliDev'),packageResources(app,root,credits));}
 }
 const artifacts=[];for(const [component,path] of [['desktop',desktop],['worker',worker]]){const stat=lstatSync(path);if(!stat.isFile()||stat.size<1||stat.size>2*1024**3)throw new Error('Invalid updater artifact.');const hash=createHash('sha256');for await(const bytes of createReadStream(path))hash.update(bytes);artifacts.push({component,target,name:basename(path),size:stat.size,sha256:hash.digest('hex')});}
 writeFileSync(join(stage,'input.json'),JSON.stringify({schemaVersion:1,version,sourceRevision:revision,protocolVersion:1,artifacts,platformSigning:'keyless-dry-run',nativeAcceptance:'unverified',publication:'not-requested'},null,2)+'\n',{flag:'wx',mode:0o600});
 renameSync(stage,output);
 }finally{rmSync(stage,{recursive:true,force:true});rmSync(scratch,{recursive:true,force:true});}
}
if(process.argv[1]&&pathToFileURL(resolve(process.argv[1])).href===import.meta.url)await main(process.argv.slice(2));

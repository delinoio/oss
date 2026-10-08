// SPDX-License-Identifier: Apache-2.0
import { spawnSync } from 'node:child_process';
import { copyFileSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, renameSync, rmSync, writeFileSync } from 'node:fs';
import { basename, dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { tmpdir } from 'node:os';
import { selectTarget, acquireNativeBuildLock, verifyPackageRevision, verifyNotices, packageResources } from './native-package.mjs';
import { verifyBundle } from './bundle-macos-dry-run.mjs';
import { nativeEnvironment } from './bundle-native-dry-run.mjs';
import { signMacOS } from '../../../scripts/release/delidev-macos-sign.mjs';
import { verifySigningInput } from '../../../scripts/release/delidev-signing-input.mjs';
import { artifactName } from '../../../scripts/release/generate-delidev-updater.mjs';
import { digest, identity, requireValue, updaterTarget } from '../../../scripts/release/delidev-release.mjs';

export async function main(args) {
  requireValue(args.length === 2 && args[0] === '--target', 'Choose one DeliDev native release target');
  const selected = selectTarget(args[1],process.platform,process.arch);
  requireValue(selected.platform !== 'win32','Windows signing is not implemented');
  const app = fileURLToPath(new URL('..',import.meta.url)), root = resolve(app,'../..');
  const env = nativeEnvironment(process.env,process.platform);
  const run = (program,args,cwd=app,input) => {
    const result = spawnSync(program,args,{cwd,env,input,encoding:'utf8',timeout:120*60*1000,maxBuffer:8*1024**2});
    requireValue(!result.error && result.status === 0, `DeliDev release packaging failed: ${basename(program)}`);
    return program === 'codesign' && args.includes('--display') ? result.stdout + result.stderr : result.stdout;
  };
  const revision = run('git',['rev-parse','HEAD']).trim();
  verifyPackageRevision(process.env.RELEASE_REVISION,revision,run('git',['status','--porcelain','--untracked-files=normal']));
  const version = JSON.parse(readFileSync(join(app,'src-tauri/tauri.conf.json'),'utf8')).version;
  requireValue(version === process.env.RELEASE_VERSION, 'DeliDev release version mismatch');
  const input = selected.platform === 'darwin' ? join(root,'target/delidev-signing-input',selected.target) : join(root,'target/delidev-updater-input',selected.target,revision);
  const credits = selected.platform === 'darwin' ? await verifySigningInput(input,selected.target,identity(version,revision)) : null;
  const report = JSON.parse(readFileSync(join(input,'input.json'),'utf8'));
  requireValue(report.sourceRevision === revision && report.version === version && report.platformSigning === 'keyless-dry-run' && report.artifacts.length === 2, 'Original updater build identity mismatch');
  const output = join(root,'target/delidev-release-candidate',selected.target);
  requireValue(!existsSync(output),'Retain the original signed DeliDev candidate');
  mkdirSync(dirname(output),{recursive:true});
  const unlock = acquireNativeBuildLock(root), scratch = mkdtempSync(join(tmpdir(),'delidev-release-'));
  let stage;
  try {
    stage = mkdtempSync(join(dirname(output),'.pending-'));
    const target = updaterTarget(selected);
    for (const a of report.artifacts) {
      requireValue(a.name === artifactName(a.component,target), 'Unexpected updater artifact name');
      const bytes = await digest(join(input,a.name));
      requireValue(bytes.size === a.size && bytes.sha256 === a.sha256, 'Original updater bytes changed');
      copyFileSync(join(input,a.name),join(stage,a.name));
    }
    if (selected.platform === 'darwin') {
      // Consume the byte-pinned input DMG, not mutable target build outputs.
      const volume = join(scratch,'volume'), bundle = join(scratch,'DeliDev.app');
      mkdirSync(volume);
      let attached = false;
      try {
        const xml = run('/usr/bin/hdiutil',['attach',join(input,artifactName('desktop',target)),'-readonly','-nobrowse','-mountpoint',volume,'-plist']);
        attached = true;
        const mounted = JSON.parse(run('/usr/bin/python3',['-c','import sys,plistlib,json\np=plistlib.loads(sys.stdin.buffer.read())\nprint(json.dumps([{ "device":e.get("dev-entry"), "mount":e.get("mount-point") } for e in p.get("system-entities",[]) if e.get("mount-point")]))'],app,xml));
        requireValue(mounted.length === 1 && mounted[0].mount === volume && /^\/dev\/disk[0-9]+s[0-9]+$/.test(mounted[0].device), 'Original DMG mount identity mismatch');
        run('/usr/bin/ditto',[join(volume,'DeliDev.app'),bundle]);
      } finally { if(attached) run('/usr/bin/hdiutil',['detach',volume]); }
      verifyBundle(bundle,(program,args) => run(program,args),selected.arch === 'arm64' ? 'arm64' : 'x86_64');
      const resources = packageResources(app,root,credits);
      verifyNotices(join(bundle,'Contents/Resources'),resources);
      const desktop = join(stage,artifactName('desktop',target)); rmSync(desktop);
      await signMacOS({bundle,worker:join(stage,artifactName('worker',target)),desktop,version});
      verifyNotices(join(bundle,'Contents/Resources'),resources);
    } else {
      const directory = join(root,'target/delidev-dry-run',selected.target,revision);
      const verification = JSON.parse(readFileSync(join(directory,'verification.json'),'utf8'));
      requireValue(verification.sourceRevision === revision && verification.target === selected.target && verification.signature === 'unsigned' && verification.artifact.endsWith('.deb') && basename(verification.artifact) === verification.artifact, 'Original Debian package identity mismatch');
      const bytes = await digest(join(directory,verification.artifact));
      requireValue(bytes.size === verification.bytes && bytes.sha256 === verification.sha256, 'Original Debian package changed');
      copyFileSync(join(directory,verification.artifact),join(stage,`delidev-desktop-${target}.deb`));
    }
    const artifacts = [];
    for (const name of readdirSync(stage).sort()) artifacts.push({name,...await digest(join(stage,name))});
    verifyPackageRevision(revision,run('git',['rev-parse','HEAD']),run('git',['status','--porcelain','--untracked-files=normal']));
    writeFileSync(join(stage,'release-input.json'),JSON.stringify({schemaVersion:1,version,sourceRevision:revision,target:selected.target,artifacts,platformSigning:selected.platform === 'darwin' ? 'developer-id-notarized' : 'linux-digests',nativeAcceptance:'unverified'},null,2)+'\n',{flag:'wx'});
    renameSync(stage,output);
    console.log(JSON.stringify({component:'delidev.release.package',target:selected.target,revision,outcome:'verified',nativeAcceptance:'unverified'}));
  } finally { try { if(stage) rmSync(stage,{recursive:true,force:true}); rmSync(scratch,{recursive:true,force:true}); } finally { unlock(); } }
}
if(process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) main(process.argv.slice(2)).catch(error => { console.error(JSON.stringify({component:'delidev.release.package',outcome:'failed',code:error.code ?? 'invalid_package_or_signing_data'})); process.exitCode=1; });

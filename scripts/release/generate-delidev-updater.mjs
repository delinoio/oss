// SPDX-License-Identifier: Apache-2.0
import { createHash, createPrivateKey, createPublicKey, sign } from 'node:crypto';
import { closeSync, lstatSync, openSync, readFileSync, readSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';

export const targets = Object.freeze(['darwin-amd64','darwin-arm64','windows-amd64','windows-arm64','linux-amd64','linux-arm64']);
const rootPath = fileURLToPath(new URL('../../cmds/delidev-cli/internal/updates/trust-root.json', import.meta.url));
const maxSize = 2 * 1024 ** 3;
export function canonical(value) {
  if (Array.isArray(value)) return `[${value.map(canonical).join(',')}]`;
  if (value !== null && typeof value === 'object') return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${canonical(value[key])}`).join(',')}}`;
  return JSON.stringify(value);
}
export function artifactName(component,target) {
  const extension = component === 'worker' ? (target.startsWith('windows-') ? '.exe' : '') : target.startsWith('darwin-') ? '.dmg' : target.startsWith('windows-') ? '.exe' : '.AppImage';
  return `delidev-${component}-${target}${extension}`;
}
function hashFile(path,size) {
  const file=openSync(path,'r');const hash=createHash('sha256');const buffer=Buffer.alloc(1024*1024);let count=0;
  try { for (;;) {const n=readSync(file,buffer,0,buffer.length,null);if (!n) break;count+=n;if(count>size) throw new Error('Artifact changed while signing.');hash.update(buffer.subarray(0,n));} }
  finally {closeSync(file);}
  if(count!==size) throw new Error('Artifact changed while signing.');return hash.digest('hex');
}
export function signingRoot() {
  const root=JSON.parse(readFileSync(rootPath,'utf8'));
  if(root.schemaVersion!==1 || root.keyId!=='delidev-release-root-v1' || root.productionReady!==true || typeof root.publicKey!=='string' || Buffer.from(root.publicKey,'base64').length!==32 || Buffer.from(root.publicKey,'base64').toString('base64')!==root.publicKey) throw new Error('The compiled DeliDev production signing declaration is not ready.');
  return root;
}
// This pure signer permits an explicitly supplied root for isolated unit tests.
// The CLI always obtains its sole authority from signingRoot() above.
export function buildManifest(input,key,root) {
  if(key.asymmetricKeyType!=='ed25519' || createPublicKey(key).export({format:'der',type:'spki'}).subarray(-32).toString('base64')!==root.publicKey) throw new Error('Signing key does not match the declared public root.');
  if(!/^\d+\.\d+\.\d+$/.test(input.version) || input.version.split('.').some(part=>String(Number(part))!==part||Number(part)>0xffffffff) || !/^[a-f0-9]{40}$/.test(input.sourceRevision) || input.protocolVersion!==1 || !Number.isFinite(Date.parse(input.publishedAt)) || !Array.isArray(input.artifacts) || input.artifacts.length!==12) throw new Error('Invalid release input.');
  const seen=new Set();const artifacts=input.artifacts.map(item=> {
    const identity=`${item.component}/${item.target}`;if(!['desktop','worker'].includes(item.component)||!targets.includes(item.target)||seen.has(identity)) throw new Error('The full unique desktop/Worker target inventory is required.');seen.add(identity);
    const path=resolve(item.path);const before=lstatSync(path);if(!before.isFile()||before.isSymbolicLink()||before.size<1||before.size>maxSize) throw new Error('Invalid artifact file.');
    const sha256=hashFile(path,before.size);const after=lstatSync(path);if(before.dev!==after.dev||before.ino!==after.ino||before.mtimeMs!==after.mtimeMs||before.size!==after.size) throw new Error('Artifact changed while signing.');
    const name=artifactName(item.component,item.target);
    return {component:item.component,target:item.target,name,size:before.size,sha256,url:`https://github.com/delinoio/oss/releases/download/delidev-v${input.version}/${name}`};
  });
  const payload={schemaVersion:1,version:input.version,sourceRevision:input.sourceRevision,protocolVersion:1,publishedAt:input.publishedAt,artifacts};
  const bytes=canonical(payload);const signature=sign(null,Buffer.concat([Buffer.from('delidev-update-manifest-v1\0'),Buffer.from(bytes)]),key).toString('base64');
  return `{"keyId":${JSON.stringify(root.keyId)},"payload":${bytes},"signature":${JSON.stringify(signature)}}\n`;
}
export function main(args) {
  const options=new Map();for(let i=0;i<args.length;i+=2){if(!['--input','--key-file','--output'].includes(args[i])||!args[i+1]||options.has(args[i])) throw new Error('Use --input FILE --key-file FILE --output FILE.');options.set(args[i],args[i+1]);}if(options.size!==3)throw new Error('All release inputs are required.');
  const root=signingRoot();const path=resolve(options.get('--key-file'));const mode=lstatSync(path);if(!mode.isFile()||mode.isSymbolicLink()||mode.size>4096||(process.platform!=='win32'&&(mode.mode&0o077)!==0))throw new Error('Use a private regular signing-key file.');
  const bytes=readFileSync(path);let manifest;try{const key=createPrivateKey(bytes);manifest=buildManifest(JSON.parse(readFileSync(options.get('--input'),'utf8')),key,root);}finally{bytes.fill(0);}
  writeFileSync(resolve(options.get('--output')),manifest,{mode:0o600,flag:'wx'});
}
if(process.argv[1] && pathToFileURL(resolve(process.argv[1])).href===import.meta.url){try{main(process.argv.slice(2));}catch{process.stderr.write('DeliDev release manifest generation failed; no release was published.\n');process.exitCode=1;}}

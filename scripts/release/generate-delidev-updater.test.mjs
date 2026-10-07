// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import { generateKeyPairSync,verify } from 'node:crypto';
import { mkdtempSync,rmSync,writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import { buildManifest,canonical,signingRoot,targets } from './generate-delidev-updater.mjs';
test('production signing remains closed without owner declaration',()=>assert.throws(signingRoot));
test('all six desktop and Worker artifacts are signed with an isolated key',()=>{
 const root=mkdtempSync(join(tmpdir(),'delidev-signing-'));try{
 const {privateKey,publicKey}=generateKeyPairSync('ed25519');const declaration={keyId:'fixture',publicKey:publicKey.export({type:'spki',format:'der'}).subarray(-32).toString('base64')};
 const path=join(root,'artifact');writeFileSync(path,'fixture-binary');const input={version:'0.2.0',sourceRevision:'a'.repeat(40),protocolVersion:2,publishedAt:'2026-10-04T00:00:00Z',artifacts:['desktop','worker'].flatMap(component=>targets.map(target=>({component,target,path})))};
 const manifest=JSON.parse(buildManifest(input,privateKey,declaration));assert.equal(manifest.payload.artifacts.length,12);assert.ok(verify(null,Buffer.concat([Buffer.from('delidev-update-manifest-v1\0'),Buffer.from(canonical(manifest.payload))]),publicKey,Buffer.from(manifest.signature,'base64')));
 for(const change of [value=>value.artifacts.pop(),value=>value.artifacts[1]=value.artifacts[0],value=>value.artifacts[0].target='linux-386',value=>value.version='01.2.0',value=>value.sourceRevision='main']){const copy=structuredClone(input);change(copy);assert.throws(()=>buildManifest(copy,privateKey,declaration));}
 assert.throws(()=>buildManifest(input,generateKeyPairSync('ed25519').privateKey,declaration));
 }finally{rmSync(root,{recursive:true,force:true});}
});

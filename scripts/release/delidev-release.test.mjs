// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync } from 'node:fs';
import { join } from 'node:path';
import { tmpdir } from 'node:os';
import { assemble, digest, identity, sourceIdentity, releaseTargets, expectedNames, updaterTarget, verifyDirectory, validateIndex, findRelease, reconcile, releaseNotes } from './delidev-release.mjs';
import { restoreCandidate, verifyCandidate } from './delidev-candidate.mjs';
import { signingRoot } from './generate-delidev-updater.mjs';

const expected = identity('0.1.1','a'.repeat(40));
async function fixture(t) {
  const directory = mkdtempSync(join(tmpdir(),'delidev-release-fixture-'));
  t.after(() => rmSync(directory,{recursive:true,force:true}));
  const inputs = join(directory,'inputs'), output = join(directory,'output');
  for (const target of releaseTargets) {
    const folder = join(inputs,target.target); mkdirSync(folder,{recursive:true});
    const artifacts = [];
    for (const name of expectedNames().filter(n => n.includes(updaterTarget(target)))) {
      writeFileSync(join(folder,name),`fixture ${name}`); artifacts.push({name,...await digest(join(folder,name))});
    }
    writeFileSync(join(folder,'release-input.json'),JSON.stringify({schemaVersion:1,version:expected.version,sourceRevision:expected.sourceRevision,target:target.target,artifacts,nativeAcceptance:'unverified',platformSigning:target.platform === 'darwin' ? 'developer-id-notarized' : 'linux-digests'}));
  }
  await assemble(inputs,output,expected);
  const index = await verifyDirectory(output,expected);
  return {directory,inputs,output,index};
}
function remote(state, overrides = {}) {
  const events = [], assets = [], buffers = new Map();
  let release = null, nextId = 100;
  const api = async (route, options = {}) => {
    events.push([route,options.method ?? 'GET']);
    if (route.startsWith('releases?')) return release ? [release] : [];
    if (route === 'releases' && options.method === 'POST') {
      release = {id:1,...options.body};
      if(overrides.createLost) throw new Error('creation response lost');
      return release;
    }
    if (route.includes('/assets?')) return assets;
    if (options.method === 'PATCH') { release.draft = false; if(overrides.publishLost) throw new Error('publication response lost'); return release; }
    return release;
  };
  const readAsset = async asset => {
    if(asset === 'local-index' || asset === 'local-sums') return digest(join(state.output,asset === 'local-index' ? 'delidev-release-index.json' : 'SHA256SUMS'));
    const data = buffers.get(asset.id);
    const file = join(state.directory,`read-${asset.id}`); writeFileSync(file,data); return digest(file);
  };
  const upload = async (id,name) => {
    assert.equal(id,1); const bytes=readFileSync(join(state.output,name));
    const asset = {id:nextId++,name,size:bytes.length}; assets.push(asset); buffers.set(asset.id,bytes);
    if(overrides.uploadLost) throw new Error('upload response lost');
  };
  return {api,readAsset,upload,events,assets,buffers,get release(){return release;}};
}
test('four native targets assemble ten exact download assets without an updater manifest',async t => {
  const state = await fixture(t);
  assert.equal(releaseTargets.length,4); assert.equal(state.index.artifacts.length,12);
  assert.equal(state.index.channel,'download-only'); assert.throws(signingRoot,/not ready/);
  writeFileSync(join(state.output,'delidev-update-manifest.json'),'{}');
  await assert.rejects(verifyDirectory(state.output,expected),/Unexpected/);
});
test('candidate revision, architecture target, inventory and final bytes cannot drift',async t => {
  const state=await fixture(t), target=releaseTargets[0], directory=join(state.inputs,target.target);
  await verifyCandidate(directory,target.target,expected);
  const reportFile=join(directory,'release-input.json'), original=JSON.parse(readFileSync(reportFile));
  for(const change of [{sourceRevision:'b'.repeat(40)},{version:'9.0.0'},{target:releaseTargets[1].target},{platformSigning:'keyless-dry-run'},{artifacts:original.artifacts.slice(1)}]) {
    writeFileSync(reportFile,JSON.stringify({...original,...change})); await assert.rejects(verifyCandidate(directory,target.target,expected));
  }
  writeFileSync(reportFile,JSON.stringify(original)); writeFileSync(join(directory,original.artifacts[0].name),'changed after signing');
  await assert.rejects(verifyCandidate(directory,target.target,expected),/bytes conflict/);
  assert.throws(()=>validateIndex({...state.index,artifacts:state.index.artifacts.slice(1)},expected),/Incomplete/);
});
for(const scenario of ['normal','createLost','uploadLost','publishLost']) test(`draft publication reconciles ${scenario} and never mutates a public release`,async t => {
  const state=await fixture(t), r=remote(state,{[scenario]:true});
  const options={...r,index:state.index,expected,existing:null};
  assert.equal(await reconcile(options),'published'); assert.equal(r.assets.length,14);
  const before=r.events.length;
  assert.equal(await reconcile({...options,existing:r.release}),'reused');
  assert.ok(r.events.slice(before).every(([,method])=>method==='GET'));
});
test('owned partial draft uploads only missing files; corrupt or incomplete public assets fail',async t => {
  const state=await fixture(t), r=remote(state);
  const options={...r,index:state.index,expected};
  await reconcile({...options,existing:null}); r.release.draft=true;
  const removed=r.assets.pop();r.buffers.delete(removed.id);
  assert.equal(await reconcile({...options,existing:r.release}),'published');
  r.buffers.set(r.assets[0].id,Buffer.from('corrupt'));
  await assert.rejects(reconcile({...options,existing:r.release}),/bytes conflict/);
});
test('public partial release cannot gain files or change source/channel',async t => {
  const state=await fixture(t), r=remote(state), options={...r,index:state.index,expected};
  await reconcile({...options,existing:null});
  const asset=r.assets.pop();r.buffers.delete(asset.id);
  await assert.rejects(reconcile({...options,existing:r.release}),/Incomplete public/);
  await assert.rejects(reconcile({...options,existing:{...r.release,target_commitish:'b'.repeat(40)}}),/ownership/);
  await assert.rejects(reconcile({...options,existing:{...r.release,body:'different channel'}}),/ownership/);
});
test('unknown writes do not blindly retry creation or upload',async t => {
  const state=await fixture(t), r=remote(state);
  const api=async(route,options)=>{if(options?.method==='POST')throw new Error('unknown');return r.api(route,options);};
  await assert.rejects(reconcile({...r,api,index:state.index,expected,existing:null}),/creation outcome/);
  assert.equal(r.events.filter(([route])=>route==='releases').length,0);
  const existing={id:1,tag_name:expected.tag,target_commitish:expected.sourceRevision,prerelease:false,draft:true,body:releaseNotes(expected)};
  let writes=0;
  await assert.rejects(reconcile({...r,index:state.index,expected,existing,upload:async()=>{writes++;throw new Error('unknown');}}),/upload outcome/);
  assert.equal(writes,1);
});
test('release discovery includes drafts on later pages and rejects duplicates',async()=>{
  const filler=Array.from({length:100},(_,id)=>({id:id+1,tag_name:'other'}));
  const record={id:200,tag_name:expected.tag,draft:true};
  assert.equal(await findRelease(async route=>route.endsWith('page=1')?filler:[record],expected.tag),record);
  await assert.rejects(findRelease(async()=>[record,record],expected.tag),/Duplicate/);
  await assert.rejects(findRelease(async()=>null,expected.tag),/listing/);
});
test('same-run candidates restore without signing again; absent is distinct from expired',async t=>{
  const state=await fixture(t), target=releaseTargets[0], name=`delidev-release-${target.target}-${expected.sourceRevision}-7`;
  const options={target:target.target,expected,runId:'7',directory:join(state.inputs,target.target),download:async()=>{}};
  assert.equal(await restoreCandidate({...options,list:async()=>[]}),false);
  assert.equal(await restoreCandidate({...options,list:async()=>[{id:1,name,expired:false}]}),true);
  await assert.rejects(restoreCandidate({...options,list:async()=>[{id:1,name,expired:true}]}),/expired/);
  await assert.rejects(restoreCandidate({...options,list:async()=>[{id:1,name,expired:false},{id:2,name,expired:false}]}),/ambiguous/);
});

test('tag source identity rejects foreign events, malformed versions and source conflicts', () => {
  const input = { repository: 'delinoio/oss', event: 'push', ref: 'refs/tags/delidev-v0.1.1', revision: 'a'.repeat(40), head: 'a'.repeat(40), tagRevision: 'a'.repeat(40), version: '0.1.1' };
  assert.deepEqual(sourceIdentity(input), { version: '0.1.1', revision: input.revision });
  for (const changes of [
    { repository: 'fork/oss' }, { event: 'workflow_dispatch' },
    { ref: 'refs/heads/main' }, { ref: 'refs/tags/delidev-v01.1.1' },
    { ref: 'refs/tags/delidev-v0.1.1-next.1' }, { ref: 'refs/tags/delidev-v0.1.1junk' },
    { ref: 'refs/tags/delidev-v4294967296.1.1' }, { revision: 'invalid' },
    { head: 'b'.repeat(40) }, { tagRevision: 'b'.repeat(40) }, { version: '0.1.2' },
  ]) assert.throws(() => sourceIdentity({ ...input, ...changes }));
});

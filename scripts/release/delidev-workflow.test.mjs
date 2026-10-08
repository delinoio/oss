// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import yaml from 'js-yaml';
const source = name => readFileSync(new URL(`../../.github/workflows/${name}`,import.meta.url),'utf8');
const workflow=yaml.load(source('release-delidev.yml'));
test('release starts independently from an immutable tag push',()=>{
  const coordinator=yaml.load(source('release-project.yml'));
  assert.equal(coordinator.jobs.delidev,undefined);
  assert.deepEqual(coordinator.jobs.summary.needs,['prepare','registry','tag']);
  assert.deepEqual(workflow.on,{push:{tags:['delidev-v*']}});
  assert.equal(workflow.jobs.inspect.if,"github.repository == 'delinoio/oss' && startsWith(github.ref, 'refs/tags/delidev-v') && github.event_name == 'push'");
  const inspect=workflow.jobs.inspect;
  assert.equal(inspect.steps[0].with.ref,'${{ github.sha }}');
  assert.equal(inspect.steps[0].with['fetch-depth'],0);
  assert.equal(inspect.steps.find(s=>s.id==='source').run,'node scripts/release/delidev-release.mjs source');
  for(const name of ['version','revision']) assert.equal(inspect.outputs[name],'${{ steps.source.outputs.'+name+' }}');
  for(const name of ['preflight','package','sign','publish','result']) {
    const job=workflow.jobs[name];
    assert.equal(job.env.RELEASE_VERSION,'${{ needs.inspect.outputs.version }}');
    assert.equal(job.env.RELEASE_REVISION,'${{ needs.inspect.outputs.revision }}');
    for(const step of job.steps) if(step.uses?.startsWith('actions/checkout')) assert.equal(step.with.ref,'${{ needs.inspect.outputs.revision }}');
  }
  assert.doesNotMatch(source('release-delidev.yml'),/inputs\.(version|revision)|workflow_call/);
});
test('only Environment-owned signing jobs read the required secret references',()=>{
  const names = [
    'DELIDEV_MACOS_CERTIFICATE_BASE64', 'DELIDEV_MACOS_CERTIFICATE_PASSWORD',
    'DELIDEV_MACOS_APP_PROFILE_BASE64', 'DELIDEV_MACOS_WIDGET_PROFILE_BASE64',
    'DELIDEV_MACOS_SELECTION_PROFILE_BASE64', 'DELIDEV_APPLE_NOTARY_KEY_BASE64',
    'DELIDEV_APPLE_NOTARY_KEY_ID', 'DELIDEV_APPLE_NOTARY_ISSUER_ID',
  ];
  assert.doesNotMatch(source('release-project.yml'),/secrets\.DELIDEV_/);
  const references = [...new Set([...source('release-delidev.yml').matchAll(/secrets\.(DELIDEV_[A-Z0-9_]+)/g)].map(match=>match[1]))].sort();
  assert.deepEqual(references,[...names].sort());
  for(const name of ['preflight','sign']) {
    assert.equal(workflow.jobs[name].environment,'delidev-release');
    const secretSteps=workflow.jobs[name].steps.filter(s=>JSON.stringify(s.env ?? {}).includes('secrets.DELIDEV_'));
    assert.equal(secretSteps.length,1);
    for(const secret of names) assert.equal(secretSteps[0].env[secret],'${{ secrets.'+secret+' }}');
  }
  assert.equal(workflow.jobs.package.environment,undefined);
});
test('publication is behind four-native matrix, signing preflight and original candidate validation',()=>{
  assert.deepEqual(workflow.permissions,{contents:'read',actions:'read'});
  assert.equal(workflow.jobs.package.strategy.matrix,'${{ fromJSON(needs.inspect.outputs.matrix) }}');
  assert.deepEqual(workflow.jobs.package.needs,['inspect','preflight']);
  assert.deepEqual(workflow.jobs.publish.needs,['inspect','package','sign']);
  assert.equal(workflow.jobs.sign.environment,'delidev-release');
  assert.equal(workflow.jobs.package.environment,undefined);
  assert.equal(workflow.jobs.publish.permissions.contents,'write');
  assert.equal(workflow.concurrency['cancel-in-progress'],false);
  assert.deepEqual(workflow.jobs.sign.needs,['inspect','preflight','package']);
  assert.equal(workflow.jobs.sign.strategy.matrix,'${{ fromJSON(needs.inspect.outputs.macos_matrix) }}');
  const steps=workflow.jobs.package.steps;
  assert.equal(steps[0].with.lfs,true);assert.equal(steps[0].with['persist-credentials'],false);
  const restore=steps.findIndex(s=>s.id==='retained'), build=steps.findIndex(s=>s.name==='Build keyless verified inputs');
  assert.ok(restore<build);
  assert.ok(steps.filter(s=>s.uses?.startsWith('actions/upload-artifact')).every(s=>s.with.overwrite===undefined));
  for(const step of steps.filter(s=>s.env && Object.keys(s.env).some(n=>n.includes('CERTIFICATE'))))assert.equal(step.name,'Sign and notarize macOS release files');
  assert.doesNotMatch(JSON.stringify(workflow),/DELIDEV_UPDATE.*KEY|SIGNING_PRIVATE_KEY|--clobber|delete-asset/);
  const publish=workflow.jobs.publish.steps;
  assert.ok(publish.findIndex(s=>s.run?.includes('delidev-release.mjs assemble'))<publish.findIndex(s=>s.run?.includes('delidev-release.mjs publish')));
});
test('result fails when any original phase fails and accepts immutable public reuse only after inspection',()=>{
  const step=workflow.jobs.result.steps[0];
  const js=step.run.match(/^node <<'JS'\n([\s\S]*)\nJS\n?$/)[1];
  for(const [published,statuses,expected] of [['false',['success','success','success','success','success'],0],['false',['success','success','failure','skipped','skipped'],1],['true',['success','skipped','skipped','skipped','skipped'],0],['true',['failure','skipped','skipped','skipped','skipped'],1]]){
    const results=Object.fromEntries(['inspect','preflight','package','sign','publish'].map((name,i)=>[name,{result:statuses[i],outputs:{published}}]));
    // Avoid file writes: capture the summary through a minimal fs stub.
    const script=js.replace("const fs = require('node:fs');","const fs = {appendFileSync: (_, value) => process.stdout.write(value)};");
    const result=spawnSync(process.execPath,['-e',script],{env:{RESULTS:JSON.stringify(results),RELEASE_VERSION:'0.1.1'},encoding:'utf8'});
    assert.equal(result.status,expected,result.stderr);assert.match(result.stdout,/Windows x64\/arm64: skipped/);assert.match(result.stdout,/no update manifest/);
  }
});

test('credential jobs use immutable actions and a clean signer without package managers or build setup',()=>{
  for(const file of ['release-delidev.yml','release-project.yml']) {
    const document = yaml.load(source(file));
    for(const job of Object.values(document.jobs)) for(const step of job.steps ?? []) if(step.uses && !step.uses.startsWith('./')) assert.match(step.uses,/@[a-f0-9]{40}$/);
  }
  const packageJob = JSON.stringify(workflow.jobs.package);
  assert.doesNotMatch(packageJob,/secrets\.|Sign and notarize/);
  const signer = workflow.jobs.sign;
  assert.equal(signer.permissions,undefined);
  assert.doesNotMatch(JSON.stringify(signer),/pnpm|setup-go|rust-toolchain|setup-prebuilt|install|bundle:updater/);
  const sign = signer.steps.find(s=>s.name==='Sign and notarize macOS release files');
  assert.equal(sign.run,'node apps/delidev/scripts/bundle-release.mjs --target ${{ matrix.target }}');
  assert.equal(sign.if,"steps.retained.outputs.reused != 'true'");
  for(const [name,job] of Object.entries(workflow.jobs)) for(const step of job.steps ?? []) if(JSON.stringify(step.env ?? {}).includes('secrets.DELIDEV_')) assert.ok(name==='sign' || name==='preflight');
  assert.ok(signer.steps.findIndex(s=>s.id==='retained') < signer.steps.findIndex(s=>s.name==='Download original macOS signing inputs'));
  assert.ok(workflow.jobs.package.steps.some(s=>s.run==='node scripts/release/delidev-candidate.mjs --signing-input'));
});

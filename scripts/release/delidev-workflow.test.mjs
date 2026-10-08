// SPDX-License-Identifier: Apache-2.0
import assert from 'node:assert/strict';
import test from 'node:test';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import yaml from 'js-yaml';
const source = name => readFileSync(new URL(`../../.github/workflows/${name}`,import.meta.url),'utf8');
const workflow=yaml.load(source('release-delidev.yml'));
test('manual coordinator calls release at the prepared immutable identity without an extra dispatch',()=>{
  const coordinator=yaml.load(source('release-project.yml'));
  assert.equal(coordinator.jobs.delidev.uses,'./.github/workflows/release-delidev.yml');
  assert.deepEqual(coordinator.jobs.delidev.needs,['prepare','tag']);
  assert.deepEqual(coordinator.jobs.delidev.with,{version:'${{ needs.prepare.outputs.version }}',revision:'${{ needs.prepare.outputs.revision }}'});
  assert.deepEqual(Object.keys(workflow.on),['workflow_call']);
  assert.equal(workflow.jobs.inspect.if,"github.repository == 'delinoio/oss' && github.ref == 'refs/heads/main' && github.event_name == 'workflow_dispatch'");
});
test('publication is behind four-native matrix, signing preflight and original candidate validation',()=>{
  assert.deepEqual(workflow.permissions,{contents:'read',actions:'read'});
  assert.equal(workflow.jobs.package.strategy.matrix,'${{ fromJSON(needs.inspect.outputs.matrix) }}');
  assert.deepEqual(workflow.jobs.package.needs,['inspect','preflight']);
  assert.deepEqual(workflow.jobs.publish.needs,['inspect','package']);
  assert.equal(workflow.jobs.package.environment,'delidev-release');
  assert.equal(workflow.jobs.publish.permissions.contents,'write');
  assert.equal(workflow.concurrency['cancel-in-progress'],false);
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
  for(const [published,statuses,expected] of [['false',['success','success','success','success'],0],['false',['success','success','failure','skipped'],1],['true',['success','skipped','skipped','skipped'],0],['true',['failure','skipped','skipped','skipped'],1]]){
    const results=Object.fromEntries(['inspect','preflight','package','publish'].map((name,i)=>[name,{result:statuses[i],outputs:{published}}]));
    // Avoid file writes: capture the summary through a minimal fs stub.
    const script=js.replace("const fs = require('node:fs');","const fs = {appendFileSync: (_, value) => process.stdout.write(value)};");
    const result=spawnSync(process.execPath,['-e',script],{env:{RESULTS:JSON.stringify(results),RELEASE_VERSION:'0.1.1'},encoding:'utf8'});
    assert.equal(result.status,expected,result.stderr);assert.match(result.stdout,/Windows x64\/arm64: skipped/);assert.match(result.stdout,/no update manifest/);
  }
});

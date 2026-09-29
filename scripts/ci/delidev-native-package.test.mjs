import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import yaml from 'js-yaml';
import { targets } from '../../apps/delidev/scripts/native-package.mjs';

test('DeliDev package dry runs cannot obtain publication or production signing authority', () => {
  const source = readFileSync('.github/workflows/delidev-native-dry-run.yml', 'utf8');
  const workflow = yaml.load(source);
  assert.deepEqual(Object.keys(workflow.on), ['workflow_dispatch']);
  assert.deepEqual(workflow.permissions, { contents: 'read' });
  assert.equal(workflow.concurrency['cancel-in-progress'], false);
  assert.equal(targets.length, 6);
  for (const job of Object.values(workflow.jobs)) {
    assert.equal(job.environment, undefined);
    assert.equal(job.permissions, undefined);
    for (const step of job.steps) {
      if (step.uses?.startsWith('actions/checkout@')) assert.equal(step.with['persist-credentials'], false);
      assert.doesNotMatch(step.run ?? '', /\b(?:gh release|gh api|git push|notarytool|signtool)\b/);
    }
  }
  assert.doesNotMatch(source, /secrets\.|id-token:|packages: write|contents: write/);
  const packaging = workflow.jobs.package;
  assert.equal(packaging['runs-on'], '${{ matrix.runner }}');
  assert.equal(packaging.strategy['fail-fast'], false);
  assert.ok(packaging.steps.some(step => step.uses?.startsWith('actions/checkout@') && step.with.lfs === true));
  const verification = packaging.steps.findIndex(step => step.run?.includes('bundle:dry-run'));
  const upload = packaging.steps.findIndex(step => step.uses?.startsWith('actions/upload-artifact@'));
  assert.ok(verification >= 0 && upload > verification);
  assert.equal(packaging.steps[upload].if, undefined);
  assert.equal(packaging.steps[upload].with['if-no-files-found'], 'error');
  assert.equal(packaging.steps[upload].with.path, 'target/delidev-dry-run/${{ matrix.target }}/${{ github.sha }}/');
});

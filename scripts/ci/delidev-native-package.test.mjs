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
  assert.equal(packaging.steps.find(step => step.uses?.startsWith('dtolnay/rust-toolchain@')).with.toolchain, readFileSync('rust-toolchain', 'utf8').trim());
  assert.ok(packaging.steps.some(step => step.run === 'rustup set default-host ${{ matrix.target }}'));
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

test('workspace investigation is explicit and excludes package assembly', () => {
  const workflow = yaml.load(readFileSync('.github/workflows/delidev-native-dry-run.yml', 'utf8'));
  assert.deepEqual(workflow.on.workflow_dispatch.inputs.workspace_fixture_only, {
    description: 'Run only the Windows workspace removal regressions', type: 'boolean', default: false,
  });
  assert.equal(workflow.jobs.plan.if, '${{ !inputs.workspace_fixture_only }}');
  assert.equal(workflow.concurrency.group, "delidev-native-dry-run-${{ github.ref }}-${{ inputs.workspace_fixture_only && 'workspace-fixtures' || 'package' }}");
  assert.equal(workflow.jobs.package.if, '${{ !inputs.workspace_fixture_only }}');
  const fixtures = workflow.jobs['workspace-fixtures'];
  assert.equal(fixtures.if, '${{ inputs.workspace_fixture_only }}');
  assert.equal(fixtures['runs-on'], 'windows-latest');
  assert.equal(fixtures['timeout-minutes'], 60);
  assert.equal(fixtures.steps.length, 3);
  assert.ok(fixtures.steps.some(step => step.with?.lfs === true && step.with['persist-credentials'] === false));
  assert.equal(fixtures.steps.at(-1).run, "go test ./cmds/delidev-cli/internal/workspace -run '^(TestClaimedRemovalPreservesUncapturedWritesDuringUnlink|TestSnapshotMaximumInventoryRemainsDeletable|TestSnapshotCreatePublicationFailureRetainsOriginalRecovery|TestRemovalJournalCapacityCompactionRetainsActiveProofAcrossRestart|TestSnapshotObservationSharesBudgetBeforeHashing|TestSnapshotObservationStopsAtAggregateGitInventory|TestSnapshotAdmissionReservesPrivatePathHeadroom)$' -count=1 -timeout=45m -v");
});

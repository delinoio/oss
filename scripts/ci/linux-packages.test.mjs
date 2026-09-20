import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import test from 'node:test';
import yaml from 'js-yaml';
const read = (file) => readFileSync(new URL(`../../${file}`, import.meta.url), 'utf8');
const workflow = yaml.load(read('.github/workflows/release-linux-packages.yml'));
test('native publication is gated by installation and confined to its environment', () => {
  assert.deepEqual(workflow.permissions, { contents: 'read' });
  assert.equal(workflow.on.workflow_dispatch.inputs.mode.default, 'dry-run');
  assert.deepEqual(workflow.jobs.publish.concurrency, { group: 'linux-package-repository', 'cancel-in-progress': false, queue: 'max' });
  assert.equal(workflow.jobs.publish.environment, 'linux-packages');
  assert.equal(workflow.jobs.publish.needs, 'install');
  assert.equal(workflow.jobs.publish.if, "inputs.mode == 'publish'");
  for (const [name, job] of Object.entries(workflow.jobs)) if (name !== 'publish') assert.doesNotMatch(JSON.stringify(job), /secrets\.|LINUX_PACKAGES_R2_SECRET|SIGNING_SUBKEY/u);
  assert.equal(workflow.jobs.install.strategy.matrix.image.length, 13);
  assert.deepEqual(workflow.jobs.install.strategy.matrix, workflow.jobs['public-install'].strategy.matrix);
  assert.equal(workflow.jobs['public-install'].needs, 'publish');
});
test('all seven release workflows wait on native packages without changing release identity', () => {
  for (const project of ['binpm', 'cargo-mono', 'nodeup', 'with-watch', 'derun', 'runmoor', 'clibox']) {
    const source = read(`.github/workflows/release-${project}.yml`);
    const release = yaml.load(source);
    const job = release.jobs['linux-packages'];
    assert.equal(job.uses, './.github/workflows/release-linux-packages.yml');
    assert.ok(job.needs.includes(project === 'clibox' ? 'publish-release' : 'publish'));
    assert.equal(job.with.project, project);
    assert.equal(job.with.mode, 'publish');
    assert.equal(job.with.revision, '${{ github.sha }}');
    assert.doesNotMatch(JSON.stringify(job), /secrets:|id-token/u);
    if (!['derun', 'runmoor'].includes(project)) assert.match(source, /scripts\/release\/linux-packages\/build-rust\.sh/u);
  }
  const derun = yaml.load(read('.github/workflows/release-derun.yml'));
  assert.ok(derun.jobs.build.strategy.matrix.include.some((item) => item.goos === 'linux' && item.goarch === 'arm64'));
});
test('tool and build image pins include immutable checksums', () => {
  const pins = JSON.parse(read('packaging/linux/pins.json'));
  assert.equal(pins.origin, 'https://pkgs.oss.delino.io');
  for (const key of ['rust_image', 'tools_image']) assert.match(pins[key], /@sha256:[a-f0-9]{64}$/u);
  for (const tool of ['nfpm', 'aptly']) for (const arch of ['amd64', 'arm64']) assert.match(pins.tools[tool].assets[arch].sha256, /^[a-f0-9]{64}$/u);
  assert.match(pins.tools.createrepo_c.source.sha256, /^[a-f0-9]{64}$/u);
  for (const target of ['x86_64-unknown-linux-gnu', 'aarch64-unknown-linux-gnu']) {
    assert.equal(pins.rustup.assets[target].url, `https://static.rust-lang.org/rustup/archive/${pins.rustup.version}/${target}/rustup-init`);
    assert.match(pins.rustup.assets[target].sha256, /^[a-f0-9]{64}$/u);
  }
  const bootstrap = read('scripts/release/linux-packages/build-rust.sh');
  assert.doesNotMatch(bootstrap, /sh\.rustup\.rs/u);
  assert.ok(bootstrap.indexOf('sha256sum --check --strict') < bootstrap.indexOf('/tmp/delino-bootstrap/rustup-init -y'));
  assert.match(read('scripts/ci/workflows.mjs'), /Remove this compatibility adapter/u);
});

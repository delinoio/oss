import { spawn, execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { parseArgs } from 'node:util';
import { identity, origin, pins, requireValue, architectures, log } from './model.mjs';
const { values } = parseArgs({ options: { fixture: { type: 'string' }, image: { type: 'string' }, architecture: { type: 'string' }, project: { type: 'string' }, version: { type: 'string' }, revision: { type: 'string' } } });
requireValue(architectures.includes(values.architecture), 'INVALID_ARCHITECTURE');
const allowedImages = ['ubuntu:22.04', 'ubuntu:24.04', 'ubuntu:26.04', 'debian:12', 'debian:13', 'fedora:43', 'fedora:44', 'registry.access.redhat.com/ubi9/ubi:latest', 'registry.access.redhat.com/ubi10/ubi:latest', 'rockylinux:9', 'rockylinux/rockylinux:10', 'almalinux:9', 'almalinux:10'];
requireValue(allowedImages.includes(values.image), 'INVALID_TEST_IMAGE');
const suffix = randomUUID();
const network = `delino-package-test-${suffix}`;
const server = `delino-package-server-${suffix}`;
const root = process.cwd();
const run = (args) => execFileSync('docker', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'inherit'] });
const asyncRun = (args) => new Promise((resolve, reject) => {
  const child = spawn('docker', args, { stdio: 'inherit' });
  child.once('error', reject); child.once('exit', (code) => code === 0 ? resolve() : reject(new Error('PACKAGE_INSTALLATION_FAILED')));
});
let started = false;
try {
  let projects;
  if (values.fixture) {
    const fixture = path.resolve(values.fixture);
    const descriptor = JSON.parse(readFileSync(path.join(fixture, 'fixture.json'), 'utf8'));
    projects = descriptor.projects ?? [descriptor.identity];
    run(['network', 'create', network]); started = true;
    run(['run', '-d', '--rm', '--name', server, '--network', network, '--network-alias', 'registry', '-v', `${fixture}:/repository:ro`, '-v', `${root}:/workspace:ro`, '-w', '/workspace', pins.tools_image, 'node', 'scripts/release/linux-packages/serve-fixture.mjs']);
  } else projects = [identity(values)];
  for (const project of projects) {
    const context = { project: project.project, version: project.version, image: values.image, architecture: values.architecture, mode: values.fixture ? 'fixture' : 'live' };
    log('install-test', context);
    await asyncRun(['run', '--rm', '--platform', `linux/${values.architecture}`, ...(values.fixture ? ['--network', network] : []), '-v', `${root}/scripts/release/linux-packages/install-test.sh:/install-test.sh:ro`, values.image, 'bash', '/install-test.sh', project.project, project.version, project.channel, values.architecture, values.fixture ? 'http://registry:8080' : origin, context.mode]);
    log('install-test-complete', context);
  }
} finally {
  if (started) { try { run(['rm', '-f', server]); } finally { run(['network', 'rm', network]); } }
}

import { createHash } from 'node:crypto';
import { execFileSync } from 'node:child_process';
import { readFileSync, writeFileSync, mkdirSync, mkdtempSync, rmSync, readdirSync, copyFileSync, chmodSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';

const pins = JSON.parse(readFileSync(process.argv[2], 'utf8'));
const arch = { x64: 'amd64', arm64: 'arm64' }[process.arch];
if (!arch) throw new Error('Unsupported packaging host');
const temporary = mkdtempSync(path.join(tmpdir(), 'delino-tools-'));
const run = (cmd, args, cwd) => execFileSync(cmd, args, { cwd, stdio: 'inherit' });
async function download(pin, destination) {
  const response = await fetch(pin.url, { signal: AbortSignal.timeout(120000) });
  if (!response.ok) throw new Error('Tool download failed');
  const bytes = Buffer.from(await response.arrayBuffer());
  if (createHash('sha256').update(bytes).digest('hex') !== pin.sha256) throw new Error('Tool checksum mismatch');
  writeFileSync(destination, bytes);
}
try {
  for (const tool of ['nfpm', 'aptly']) {
    const directory = path.join(temporary, tool);
    mkdirSync(directory);
    const archive = path.join(temporary, `${tool}.archive`);
    await download(pins.tools[tool].assets[arch], archive);
    run(tool === 'nfpm' ? 'tar' : 'unzip', tool === 'nfpm' ? ['-xzf', archive, '-C', directory] : ['-q', archive, '-d', directory]);
    const files = readdirSync(directory, { recursive: true });
    const executable = files.find((name) => path.basename(name) === tool);
    if (!executable) throw new Error('Missing pinned executable');
    copyFileSync(path.join(directory, executable), `/usr/local/bin/${tool}`);
    chmodSync(`/usr/local/bin/${tool}`, 0o755);
  }
  const archive = path.join(temporary, 'createrepo.tar.gz');
  await download(pins.tools.createrepo_c.source, archive);
  const source = path.join(temporary, 'createrepo');
  mkdirSync(source);
  run('tar', ['-xzf', archive, '-C', source, '--strip-components=1']);
  run('cmake', ['-S', source, '-B', `${source}/build`, '-DENABLE_PYTHON=OFF', '-DENABLE_DRPM=OFF', '-DWITH_ZCHUNK=OFF', '-DWITH_LIBMODULEMD=OFF', '-DBUILD_DOC_C=OFF', '-DCREATEREPO_C_INSTALL_MANPAGES=OFF']);
  run('cmake', ['--build', `${source}/build`, '--parallel', '2']);
  run('cmake', ['--install', `${source}/build`]);
} finally { rmSync(temporary, { recursive: true, force: true }); }

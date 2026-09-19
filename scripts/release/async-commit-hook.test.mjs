import assert from 'node:assert/strict';
import {readFileSync, writeFileSync, mkdtempSync, mkdirSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import test from 'node:test';
import {load} from 'js-yaml';
const root = new URL('../../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
const workflow = load(read('.github/workflows/release-async-commit-hook.yml'));
test('manual release fails closed with an unsigned nonpublishing dry run', () => {
  assert.deepEqual(Object.keys(workflow.on), ['workflow_dispatch']);
  assert.equal(workflow.on.workflow_dispatch.inputs.dry_run.default, true);
  for (const job of ['publish','deploy']) assert.equal(workflow.jobs[job].if, '${{ !inputs.dry_run }}');
  assert.equal(workflow.jobs.publish.environment, 'async-commit-hook-release');
  assert.match(JSON.stringify(workflow.jobs.validate), /refs\/heads\/main/);
  assert.doesNotMatch(JSON.stringify(workflow.jobs.validate), /gh release create|git push|pages deploy|sign-blob/);
  assert.match(JSON.stringify(workflow.jobs.publish), /REQUIRE_COSIGN=1/);
  for (const step of workflow.jobs.validate.steps.filter((step) => step.uses?.startsWith('actions/upload-artifact@'))) {
    assert.equal(step.if, '${{ !inputs.dry_run }}');
  }
});
test('source versions and six target contract agree', () => {
 const result=spawnSync('python3',['scripts/release/build-async-commit-hook.py','--validate'],{cwd:root,encoding:'utf8'});
 assert.equal(result.status,0,result.stderr);
 const metadata=JSON.parse(result.stdout);assert.equal(metadata.targets.length,6);assert.equal(metadata.executable,'ach');
});
test('installers verify signatures and checksum before extraction and preserve existing installs', () => {
 for(const path of ['install.sh','install.ps1']) {
  const text=read(`apps/async-commit-hook/public/${path}`);
  assert.match(text,/verify-blob/);assert.match(text,/SHA256SUMS.sigstore.json/);
  assert.match(text,/release-async-commit-hook.yml@refs\/heads\/main/);
  assert.match(text,/token.actions.githubusercontent.com/);
  assert.match(text,/self-update/);assert.match(text,/Checksum mismatch/);
 }
 const shell=spawnSync('sh',['-n','apps/async-commit-hook/public/install.sh'],{cwd:root,encoding:'utf8'});
 assert.equal(shell.status,0,shell.stderr);
});
test('archives reject extra paths and formula supports four Unix targets', () => {
 const code=`import importlib.util, io, tarfile, zipfile, sys
sys.dont_write_bytecode=True
spec=importlib.util.spec_from_file_location('build','scripts/release/build-async-commit-hook.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
for windows in [True,False]:
 name='ach.exe' if windows else 'ach'
 b=m.archive_bytes(b'fixture',name,windows)
 a=zipfile.ZipFile(io.BytesIO(b)) if windows else tarfile.open(fileobj=io.BytesIO(b),mode='r:gz')
 names=a.namelist() if windows else a.getnames()
 assert names==[name],names
hashes={f'ach-{o}-{a}.tar.gz':'a'*64 for o in ['darwin','linux'] for a in ['amd64','arm64']}
f=m.formula('1.0.0',hashes)
assert f.count('sha256')==4 and 'bin.install "ach"' in f
`;
 const result=spawnSync('python3',['-c',code],{cwd:root,encoding:'utf8'});
 assert.equal(result.status,0,result.stderr);
});

test('shell installer rejects invalid signature and checksum before publishing an executable', () => {
 const directory=mkdtempSync(join(tmpdir(),'ach-installer-'));
 try {
  const bin=join(directory,'tools'), assets=join(directory,'assets');mkdirSync(bin);mkdirSync(assets);
  writeFileSync(join(bin,'cosign'),'#!/bin/sh\nexit "$TEST_SIGNATURE_EXIT"\n',{mode:0o755});
  writeFileSync(join(bin,'curl'),'#!/bin/sh\nwhile [ "$#" -gt 0 ]; do case "$1" in https://*) name=${1##*/};; -o) shift; output=$1;; esac; shift; done\ncp "$TEST_ASSETS/$name" "$output"\n',{mode:0o755});
  const platform=process.platform==='darwin'?'darwin':'linux', arch=process.arch==='arm64'?'arm64':'amd64';
  const asset=`ach-${platform}-${arch}.tar.gz`;
  const archive=spawnSync('python3',['-c',`import io,tarfile,sys; a=tarfile.open(sys.argv[1],'w:gz'); i=tarfile.TarInfo('ach'); i.size=7; i.mode=0o755; a.addfile(i,io.BytesIO(b'fixture')); a.close()`,join(assets,asset)]);
  assert.equal(archive.status,0);
  for (const name of [asset+'.sigstore.json','SHA256SUMS.sigstore.json']) writeFileSync(join(assets,name),'fixture');
  const digest=createHash('sha256').update(readFileSync(join(assets,asset))).digest('hex');
  for(const [signatureExit,checksum,success] of [['1',digest,false],['0','0'.repeat(64),false],['0',digest,true]]) {
   writeFileSync(join(assets,'SHA256SUMS'),`${checksum}  ${asset}\n`);
   const destination=join(directory,`install-${signatureExit}-${checksum.slice(0,8)}`);
   const result=spawnSync('sh',['apps/async-commit-hook/public/install.sh'],{cwd:root,env:{...process.env,PATH:bin+':'+process.env.PATH,ACH_INSTALL_DIR:destination,TEST_ASSETS:assets,TEST_SIGNATURE_EXIT:signatureExit},encoding:'utf8'});
   assert.equal(result.status===0,success,result.stderr);
   if(success) assert.equal(readFileSync(join(destination,'ach'),'utf8'),'fixture');
  }
 } finally {rmSync(directory,{recursive:true,force:true});}
});

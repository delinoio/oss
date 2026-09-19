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
  for (const job of ['publish','homebrew','deploy']) assert.equal(workflow.jobs[job].if, '${{ !inputs.dry_run }}');
  assert.equal(workflow.jobs.publish.environment, 'async-commit-hook-release');
  assert.match(JSON.stringify(workflow.jobs.validate), /refs\/heads\/main/);
  assert.doesNotMatch(JSON.stringify(workflow.jobs.validate), /gh release create|git push|pages deploy|sign-blob/);
  assert.match(JSON.stringify(workflow.jobs.publish), /REQUIRE_COSIGN=1/);
  for (const step of workflow.jobs.validate.steps.filter((step) => step.uses?.startsWith('actions/upload-artifact@'))) {
    assert.equal(step.if, '${{ !inputs.dry_run }}');
  }
});
test('downstream publication retries preserve the successful immutable GitHub release', () => {
 assert.equal(workflow.jobs.homebrew.needs,'publish');
 assert.equal(workflow.jobs.deploy.needs,'homebrew');
 assert.deepEqual(workflow.jobs.homebrew.permissions,{contents:'read'});
 assert.doesNotMatch(JSON.stringify(workflow.jobs.publish),/HOMEBREW|git push origin HEAD:main|pages deploy/);
 assert.doesNotMatch(JSON.stringify([workflow.jobs.homebrew,workflow.jobs.deploy]),/gh release create|sign-blob|id-token/);
 const directory=mkdtempSync(join(tmpdir(),'ach-homebrew-'));
 try {
  const bin=join(directory,'tools'), remote=join(directory,'tap.git'), seed=join(directory,'seed'), release=join(directory,'release');
  mkdirSync(bin);mkdirSync(release);
  const env={...process.env,HOME:directory,GIT_CONFIG_NOSYSTEM:'1',GIT_CONFIG_GLOBAL:'/dev/null',GIT_AUTHOR_NAME:'Fixture',GIT_AUTHOR_EMAIL:'fixture@example.invalid',GIT_COMMITTER_NAME:'Fixture',GIT_COMMITTER_EMAIL:'fixture@example.invalid',GH_TOKEN:'fixture',ACH_TEST_TAP:remote};
  const git=(...args)=>{const r=spawnSync('git',args,{env,encoding:'utf8'});assert.equal(r.status,0,r.stderr);return r.stdout.trim();};
  git('init','--bare','--initial-branch=main',remote);git('clone',remote,seed);
  git('-C',seed,'-c','commit.gpgsign=false','commit','--allow-empty','-m','initial');git('-C',seed,'push','origin','main');
  writeFileSync(join(release,'async-commit-hook.rb'),'fixture formula\n');
  writeFileSync(join(bin,'gh'),'#!/bin/sh\nif [ "$1 $2" = "repo clone" ]; then exec git clone "$ACH_TEST_TAP" "$4"; fi\nif [ "$1 $2" = "auth setup-git" ]; then exit 0; fi\nexit 99\n',{mode:0o755});
  const script=workflow.jobs.homebrew.steps.find((step)=>step.name==='Update Homebrew formula').run;
  let first;
  for(const attempt of [1,2]) {
   const temp=join(directory,`attempt-${attempt}`);mkdirSync(temp);
   const result=spawnSync('sh',['-eu','-c',script],{cwd:directory,env:{...env,PATH:bin+':'+process.env.PATH,RUNNER_TEMP:temp},encoding:'utf8'});
   assert.equal(result.status,0,result.stderr);
   const head=git('--git-dir',remote,'rev-parse','main');
   if(first) assert.equal(head,first,'retry rewrote an already published formula');
   first=head;
  }
 } finally {rmSync(directory,{recursive:true,force:true});}
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
f=m.formula('0.1.0',hashes)
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

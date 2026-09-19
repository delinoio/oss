import assert from 'node:assert/strict';
import {readFileSync, writeFileSync, mkdtempSync, mkdirSync, rmSync, existsSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {createHash} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import test from 'node:test';
const root = new URL('../../', import.meta.url);
const read = (path) => readFileSync(new URL(path, root), 'utf8');
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

test('shell installer selects the requested version and rejects invalid arguments before downloads', () => {
 const directory=mkdtempSync(join(tmpdir(),'ach-installer-version-'));
 try {
  const bin=join(directory,'tools'), requests=join(directory,'requests');mkdirSync(bin);
  writeFileSync(join(bin,'cosign'),'#!/bin/sh\nexit 0\n',{mode:0o755});
  writeFileSync(join(bin,'curl'),'#!/bin/sh\nprintf "%s\\n" "$@" > "$TEST_REQUESTS"\nexit 77\n',{mode:0o755});
  const defaultVersion=JSON.parse(read('packaging/async-commit-hook/release-metadata.json')).version;
  for(const [args,environment,version] of [
   [[],undefined,defaultVersion],
   [[], '0.2.0','0.2.0'],
   [['--version','0.3.0'], '0.2.0','0.3.0'],
   [['--version','0.4.0'], 'invalid','0.4.0'],
   [['--version'],undefined,null],
   [['--version',''],undefined,null],
   [['--version','0.1'],undefined,null],
   [['--version','01.2.3'],undefined,null],
   [['--version','0.1.0;touch'],undefined,null],
   [['--unknown'],undefined,null],
   [['0.2.0'],undefined,null],
   [['--version','0.2.0','extra'],undefined,null],
  ]) {
   rmSync(requests,{force:true});
   const result=spawnSync('sh',['apps/async-commit-hook/public/install.sh',...args],{
    cwd:root,encoding:'utf8',env:{...process.env,PATH:bin+':'+process.env.PATH,ACH_VERSION:environment,TEST_REQUESTS:requests},
   });
   assert.equal(result.status,version?77:2,`${JSON.stringify(args)}: ${result.stderr}`);
   if(version) assert.ok(readFileSync(requests,'utf8').includes(`/async-commit-hook@v${version}/ach-`));
   else assert.equal(existsSync(requests),false,'invalid arguments started a download');
  }
 } finally {rmSync(directory,{recursive:true,force:true});}
});

test('release tag verification peels tags and rejects wrong revisions before publication', () => {
 const code=`import importlib.util, os, subprocess, tempfile, sys
sys.dont_write_bytecode=True
spec=importlib.util.spec_from_file_location('build','scripts/release/build-async-commit-hook.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
with tempfile.TemporaryDirectory(prefix='ach-tags-') as directory:
 env=dict(os.environ, HOME=directory, GIT_CONFIG_NOSYSTEM='1', GIT_CONFIG_GLOBAL=os.devnull, GIT_AUTHOR_NAME='Fixture', GIT_AUTHOR_EMAIL='fixture@example.invalid', GIT_COMMITTER_NAME='Fixture', GIT_COMMITTER_EMAIL='fixture@example.invalid')
 os.environ.update(env)
 def git(*args):
  return subprocess.run(['git','-C',directory,'-c','commit.gpgsign=false','-c','tag.gpgsign=false',*args],check=True,capture_output=True,text=True,env=env).stdout.strip()
 git('init','--bare','--quiet')
 tree=git('mktree')
 first=git('commit-tree',tree,'-m','first')
 second=git('commit-tree',tree,'-m','second')
 tag='async-commit-hook@v0.1.0'
 assert not m.verify_tag('0.1.0',first,directory)['exists']
 assert not git('tag','--list'), 'verification created a tag'
 for kind in ['lightweight','annotated','nested']:
  for revision in [first,second]:
   if kind == 'lightweight': git('tag',tag,revision)
   else:
    target=revision
    if kind == 'nested':
     git('tag','-a','inner','-m','inner',revision)
     target='inner'
    git('tag','-a',tag,'-m','release',target)
   try:
    result=m.verify_tag('0.1.0',first,directory)
   except ValueError:
    assert revision != first, (kind,revision)
   else:
    assert revision == first and result['exists'], (kind,revision)
   assert git('rev-parse',tag+'^{}') == revision, 'verification changed the tag'
   git('tag','-d',tag)
   if kind == 'nested': git('tag','-d','inner')
 try: m.verify_tag('0.1.0',first,directory+'/missing')
 except subprocess.CalledProcessError: pass
 else: raise AssertionError('unavailable remote accepted')
`;
 const result=spawnSync('python3',['-c',code],{cwd:root,encoding:'utf8'});
 assert.equal(result.status,0,result.stderr);
});

test('release archive bytes and checksums ignore the wall clock', () => {
 const code=`import importlib.util, io, hashlib, tarfile, zipfile, sys
from unittest.mock import patch
sys.dont_write_bytecode=True
spec=importlib.util.spec_from_file_location('build','scripts/release/build-async-commit-hook.py')
m=importlib.util.module_from_spec(spec);spec.loader.exec_module(m)
payload=bytes(range(256))*1024
for windows in [False, True]:
 name='ach.exe' if windows else 'ach'
 with patch('time.time',return_value=1700000000): first=m.archive_bytes(payload,name,windows)
 with patch('time.time',return_value=1800000000): later=m.archive_bytes(payload,name,windows)
 assert first==later, 'clock changed archive bytes'
 assert hashlib.sha256(first).digest()==hashlib.sha256(later).digest()
 assert first!=m.archive_bytes(payload+b'changed',name,windows)
 if windows:
  with zipfile.ZipFile(io.BytesIO(first)) as archive:
   assert archive.namelist()==[name] and archive.read(name)==payload
 else:
  assert int.from_bytes(first[4:8],'little')==0, 'gzip mtime is not fixed'
  assert first[3]&8==0, 'gzip filename depends on output path'
  with tarfile.open(fileobj=io.BytesIO(first),mode='r:gz') as archive:
   member=archive.getmembers()[0]
   assert member.name==name and member.mtime==0 and member.mode==0o755
   assert member.uid==member.gid==0 and member.uname==member.gname==''
   assert archive.extractfile(member).read()==payload
`;
 const result=spawnSync('python3',['-c',code],{cwd:root,encoding:'utf8'});
 assert.equal(result.status,0,result.stderr);
});

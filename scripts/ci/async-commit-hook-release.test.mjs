import assert from 'node:assert/strict';
import {readFileSync, writeFileSync, mkdtempSync, mkdirSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import test from 'node:test';
import {load} from 'js-yaml';
const root = new URL('../../', import.meta.url);
const workflow = load(readFileSync(new URL('.github/workflows/release-async-commit-hook.yml', root), 'utf8'));
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

test('release tag identity is checked before signing and again before creation', () => {
 const steps=workflow.jobs.publish.steps;
 const check=steps.findIndex((step)=>step.name==='Verify existing release tag before signing');
 const sign=steps.findIndex((step)=>step.name==='Sign every artifact and checksum manifest');
 const command='python3 scripts/release/build-async-commit-hook.py --verify-tag "$GITHUB_SHA"';
 assert.ok(check>=0 && check<sign,'tag verification must precede all artifact signing');
 assert.equal(steps[check].run,command);
 const publish=steps.find((step)=>step.name==='Publish exact version').run;
 assert.equal(publish.trim(),'python3 scripts/release/publish-async-commit-hook.py --assets release --version "$VERSION" --revision "$GITHUB_SHA" --run-id "$GITHUB_RUN_ID"');
 const publisher=readFileSync(new URL('scripts/release/publish-async-commit-hook.py',root),'utf8');
 assert.ok(publisher.lastIndexOf('build.verify_tag(version, revision)')<publisher.indexOf('published = api(endpoint, {"draft": False})'));
 assert.doesNotMatch(JSON.stringify(workflow.jobs.validate),/publish-async-commit-hook\.py --assets/);
});


test('release embeds the UI before Go validation and deploys only Rspress documentation', () => {
 const validate = workflow.jobs.validate.steps.map((step) => step.run ?? '').join('\n');
 assert.ok(validate.indexOf('pnpm --filter async-commit-hook build:embedded') < validate.indexOf('go test ./...'));
 assert.match(validate, /pnpm --filter async-commit-hook-docs test/);
 const artifact = workflow.jobs.validate.steps.find((step) => step.with?.name === 'ach-site');
 assert.equal(artifact.with.path, 'apps/async-commit-hook-docs/doc_build');
 const builder = readFileSync(new URL('scripts/release/build-async-commit-hook.py', root), 'utf8');
 assert.ok(builder.indexOf('"build:embedded"') < builder.indexOf('for target in m["targets"]'));
});

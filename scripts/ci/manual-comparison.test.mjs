// SPDX-License-Identifier: Apache-2.0
// Pure policy and disposable Git fixtures only. No package install or app starts.
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, renameSync, rmSync, readFileSync, symlinkSync } from "node:fs";
import { join, dirname } from "node:path";
import { tmpdir } from "node:os";
import test from "node:test";
import { fileURLToPath } from "node:url";
import { changedFiles, comparisonMode, Event, jobPaths, matricesForEvent, planJobs } from "./plan.mjs";
import { validateResults } from "./result.mjs";
import { selectionAudit } from "./selection-audit.mjs";
import { freshValidation } from "./cache-context.mjs";
function fixture(t) {
 const cwd=mkdtempSync(join(tmpdir(),"ci-manual-comparison-")); t.after(()=>rmSync(cwd,{recursive:true,force:true}));
 const git=(...args)=>execFileSync("git",args,{cwd,encoding:"utf8",stdio:["ignore","pipe","pipe"]}).trim();
 git("init");git("config","user.email","fixture@example.invalid");git("config","user.name","Fixture");
 const write=(path,value)=>{mkdirSync(join(cwd,path,".."),{recursive:true});writeFileSync(join(cwd,path),value)};
 const commit=()=>{git("add","--all");git("-c","core.hooksPath=/dev/null","commit","-m","fixture");return git("rev-parse","HEAD")};
 write("apps/delidev/src/old\nname.ts","fixture");const base=commit();
 renameSync(join(cwd,"apps/delidev/src/old\nname.ts"),join(cwd,"apps/delidev/src/new\nname.ts")); const head=commit();
 return {cwd,git,write,commit,base,head};
}
function context(f) {
 const event={inputs:{comparison_base:f.base}}, range=changedFiles(Event.Manual,event,f.head,f.cwd);
 return {event:Event.Manual,mode:comparisonMode(Event.Manual,event),range};
}
function needsFor(comparison) {
 const {jobs,forced}=planJobs(Event.Manual,comparison.range.paths,jobPaths,{comparedManual:true}), matrices=matricesForEvent(Event.Manual);
 const outputs={event:Event.Manual,mode:comparison.mode,base:comparison.range.base,head:comparison.range.head,jobs:JSON.stringify(jobs),forced:JSON.stringify(forced),rust_packages:JSON.stringify(jobs["rust-test"]||jobs["rust-clippy"]?["fixture-package"]:[]),go_test_matrix:JSON.stringify(matrices.goTestMatrix),desktop_matrix:JSON.stringify(matrices.desktopMatrix),react_forge_matrix:JSON.stringify(matrices.reactForgeMatrix),delidev_frontend_matrix:JSON.stringify(matrices.delidevFrontendMatrix)};
 return {changes:{result:"success",outputs},"ci-contracts":{result:"success"},...Object.fromEntries(Object.entries(jobs).map(([id,selected])=>[id,{result:selected?"success":"skipped"}]))};
}
test("manual comparison preserves deleted and added newline paths and validates ancestry",t=>{
 const f=fixture(t),range=context(f).range;
 assert.deepEqual(range.paths,["apps/delidev/src/new\nname.ts","apps/delidev/src/old\nname.ts"]);
 for(const base of [null," ","0".repeat(40),"f".repeat(40),f.base.toUpperCase(),"HEAD^",123]) assert.throws(()=>changedFiles(Event.Manual,{inputs:{comparison_base:base}},f.head,f.cwd));
 f.git("checkout","--detach",f.base);f.write("side.ts","sibling");const sibling=f.commit();
 assert.throws(()=>changedFiles(Event.Manual,{inputs:{comparison_base:sibling}},f.head,f.cwd),/ancestor/);
});
test("empty and absent manual inputs retain full selection and full matrices",t=>{
 const f=fixture(t);
 for(const event of [{},{inputs:{comparison_base:""}}]) {assert.equal(comparisonMode(Event.Manual,event),"manual-full");assert.deepEqual(changedFiles(Event.Manual,event,f.head,f.cwd),{base:f.head,head:f.head,paths:[]});assert(Object.values(planJobs(Event.Manual,[]).jobs).every(Boolean));}
 assert(matricesForEvent(Event.Manual).goTestMatrix.include.some(row=>row.os==="windows-latest"));
 assert(matricesForEvent(Event.Manual).desktopMatrix.include.some(row=>row.os==="macos"));
 assert(matricesForEvent(Event.Manual).reactForgeMatrix.include.some(row=>row.platform==="darwin"));
});
test("compared manual selects owning jobs, forces complete checks and keeps iOS eligibility",()=>{
 const plan=paths=>planJobs(Event.Manual,paths,jobPaths,{comparedManual:true});
 const delidev=plan(["apps/delidev/src/App.tsx"]);
 assert.equal(delidev.jobs["delidev-frontend"],true);assert.equal(delidev.jobs["devhud-ios-simulator"],false);
 for(const [id,selected] of Object.entries(delidev.jobs)) if(selected) assert.equal(delidev.forced[id],true);
 assert.equal(plan(["apps/devhud/src/App.tsx"]).jobs["devhud-ios-simulator"],true);
 assert(Object.values(plan([".github/workflows/CI.yml"]).jobs).every(Boolean));
 assert.equal(freshValidation({GITHUB_EVENT_NAME:Event.Manual}),true);assert.equal(freshValidation({GITHUB_EVENT_NAME:Event.Push}),false);
 const runner=readFileSync(new URL("./run-affected.mjs",import.meta.url),"utf8");assert(runner.includes('if (freshValidation()) args.push("--force")'));
 const workflow=readFileSync(new URL("../../.github/workflows/CI.yml",import.meta.url),"utf8");assert(workflow.includes("CI_GO_MODE:"));assert(workflow.includes("'affected' || 'full'"));
 const go=readFileSync(new URL("./go-test.mjs",import.meta.url),"utf8");assert(go.includes('"-count=1"'));
});
test("aggregate admits only verified unselected skips and rejects selected skips or identity changes",t=>{
 const f=fixture(t),comparison=context(f),needs=needsFor(comparison);
 assert.equal(validateResults(needs,comparison),true);assert.throws(()=>validateResults(needs),/verified comparison/);
 for(const field of ["base","head","mode"]) {const altered=structuredClone(needs);altered.changes.outputs[field]="wrong";assert.throws(()=>validateResults(altered,comparison),/identity/)}
 for(const result of ["skipped","failure",undefined]) {const altered=structuredClone(needs);altered["delidev-frontend"].result=result;assert.throws(()=>validateResults(altered,comparison),/delidev-frontend/)}
 const altered=structuredClone(needs);delete altered["go-test"];assert.throws(()=>validateResults(altered,comparison),/inventory/);
 const forged=structuredClone(needs),jobs=JSON.parse(forged.changes.outputs.jobs);jobs["delidev-frontend"]=false;forged.changes.outputs.jobs=JSON.stringify(jobs);assert.throws(()=>validateResults(forged,comparison),/owning job paths/);
 const cached=structuredClone(needs);cached.changes.outputs.forced="{}";assert.throws(()=>validateResults(cached,comparison),/complete workspace/);
 const matrix=structuredClone(needs);matrix.changes.outputs.go_test_matrix=JSON.stringify(matricesForEvent(Event.PullRequest).goTestMatrix);assert.throws(()=>validateResults(matrix,comparison),/go_test_matrix/);
});
test("selection audit records exact mode/range/final decisions and rejects tampering",t=>{
 const f=fixture(t),comparison=context(f),needs=needsFor(comparison),outputs=needs.changes.outputs;
 const audit=selectionAudit(Event.Manual,{inputs:{comparison_base:f.base}},f.head,JSON.parse(outputs.jobs),JSON.parse(outputs.forced),JSON.parse(outputs.rust_packages),f.cwd);
 assert.equal(audit.mode,"manual-compared");assert.equal(audit.base,f.base);assert.equal(audit.head,f.head);assert.equal(audit.selectionFinalized,true);assert.equal(audit.freshWorkspaceTests,true);
 const jobs={...audit.jobs,"delidev-frontend":false};assert.throws(()=>selectionAudit(Event.Manual,{inputs:{comparison_base:f.base}},f.head,jobs,audit.forced,audit.rustPackages,f.cwd));
});

// This disposable workspace runs one tiny file-counter task, never an app,
// package build, network server or native acceptance suite.
test("hosted manual validation executes again despite a warmed Turbo result",t=>{
 const f=fixture(t), root=fileURLToPath(new URL("../..",import.meta.url));
 const manager=JSON.parse(readFileSync(join(root,"package.json"),"utf8")).packageManager;
 f.write("package.json",JSON.stringify({name:"manual-cache-fixture",private:true,packageManager:manager}));
 f.write("pnpm-workspace.yaml","packages:\n  - apps/*\n");
 f.write("pnpm-lock.yaml","lockfileVersion: '9.0'\nimporters:\n  .: {}\n  apps/check: {}\n");
 f.write("turbo.json",JSON.stringify({tasks:{check:{outputs:[]}}}));
 f.write("apps/check/package.json",JSON.stringify({name:"manual-check",scripts:{check:"node count.cjs"}}));
 f.write("apps/check/count.cjs",'const fs=require("node:fs");fs.writeFileSync("count",String(Number(fs.existsSync("count")?fs.readFileSync("count","utf8"):0)+1));');
 f.write(".gitignore","node_modules\n.turbo\napps/check/count\n");
 f.commit();
 symlinkSync(join(root,"node_modules"),join(f.cwd,"node_modules"),process.platform==="win32"?"junction":"dir");
 const run=event=>spawnSync(process.execPath,[join(root,"scripts/ci/run-affected.mjs"),"manual-check","check"],{cwd:f.cwd,encoding:"utf8",env:{...process.env,CI:"true",GITHUB_EVENT_NAME:event,FORCE_RUN:"true",TURBO_REMOTE_CACHE_AUTH:"false",TURBO_TOKEN:"",TURBO_TEAM:"",TURBO_TELEMETRY_DISABLED:"1"}});
 for(const event of [Event.Push,Event.Push,Event.Manual]) {const result=run(event);assert.equal(result.status,0,result.stdout+result.stderr);}
 assert.equal(readFileSync(join(f.cwd,"apps/check/count"),"utf8"),"2");
});

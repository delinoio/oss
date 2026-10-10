// SPDX-License-Identifier: Apache-2.0
// Configuration/evidence regressions only; no browser, server or build starts.
import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { browserChecks, browserEnvironment, completedEvidence } from "./run-delidev-browser.mjs";
const source={DELIDEV_BROWSER_PLAYWRIGHT_MODULE:"/tmp/browser-host/node_modules/playwright/index.mjs",DELIDEV_BROWSER_EVIDENCE_DIR:"/tmp/browser-evidence",DELIDEV_QA_SCREENSHOTS:"enabled",DELIDEV_QA_BROWSER_CHANNEL:"chrome"};
test("host configuration pins Chromium and disables all QA screenshots",()=>{
 const env=browserEnvironment(source,"/fixture/checkout");
 assert.equal(env.DELIDEV_QA_PLAYWRIGHT_MODULE,source.DELIDEV_BROWSER_PLAYWRIGHT_MODULE);
 assert.equal(env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE,source.DELIDEV_BROWSER_PLAYWRIGHT_MODULE);
 assert.equal(env.DELIDEV_QA_BROWSER_CHANNEL,"chromium");assert.equal(env.DELIDEV_LAYOUT_BROWSER_CHANNEL,"chromium");assert.equal(env.DELIDEV_QA_SCREENSHOTS,"disabled");
 assert.equal(browserChecks.length,13);assert.equal(new Set(browserChecks.map(value=>value.script)).size,13);
});
test("missing host and checkout-owned evidence fail without skipping checks",()=>{
 assert.throws(()=>browserEnvironment({},"/fixture/checkout"),/host-missing/);
 assert.throws(()=>browserEnvironment({...source,DELIDEV_BROWSER_EVIDENCE_DIR:"/fixture/checkout/artifacts"},"/fixture/checkout"),/must-be-external/);
});
test("QA completion requires assertions, no images and confirmed original cleanup",()=>{
 const check=browserChecks[0],record={operation:check.operation,result:"passed",screenshots:"disabled",checks:["metadata-fixture"],cleanup:[{state:"deleted"},{state:"deleted"}]};
 assert.equal(completedEvidence(check,[record]),record);
 for(const replacement of [{screenshots:"captured"},{checks:[]},{cleanup:undefined},{cleanup:[]},{cleanup:[{state:"preserved"}]},{result:"failed"}])assert.throws(()=>completedEvidence(check,[{...record,...replacement}]));
 assert.throws(()=>completedEvidence(check,[]));
});
test("layout completion requires actual checked cases",()=>{
 const worker=browserChecks[1],menu=browserChecks[3];
 assert.equal(completedEvidence(worker,[{operation:worker.operation,result:"passed",checks:384}]).checks,384);
 assert.throws(()=>completedEvidence(worker,[{operation:worker.operation,result:"passed",checks:0}]));
 assert.equal(completedEvidence(menu,[{operation:menu.operation,cases:12}]).cases,12);
 assert.throws(()=>completedEvidence(menu,[{operation:menu.operation,cases:0}]));
});
test("hosted checks install the exact browser and retain structural evidence without images",()=>{
 const workflow=readFileSync(new URL("../../.github/workflows/CI.yml",import.meta.url),"utf8");
 const block=workflow.slice(workflow.indexOf("  delidev-frontend:"),workflow.indexOf("  devhud-admin:"));
 assert(block.includes("Run hosted desktop browser fixtures"));assert(block.includes("matrix.phase == 'checks'"));assert(block.includes("playwright@1.58.2"));assert(block.includes("install --with-deps chromium"));assert(block.includes("node scripts/ci/run-delidev-browser.mjs"));assert(block.includes("browser-evidence.jsonl"));assert(!block.includes("screenshots/"));
 const qa=readFileSync(new URL("../../apps/delidev/scripts/qa/test-browser.mjs",import.meta.url),"utf8");
 assert(qa.includes('process.env.DELIDEV_QA_SCREENSHOTS ?? "enabled"'));assert(qa.includes("if (screenshotsEnabled) await Promise.all"));assert(qa.includes("if (screenshotsEnabled && !passed && browser && run.artifacts)"));
});

test("sidebar completion requires passed structural assertions without images",()=>{
 const check=browserChecks.find(value=>value.id==="sidebar-collapse-layout");
 assert.equal(completedEvidence(check,[{operation:check.operation,result:"passed",checks:12}]).checks,12);
 for (const record of [{operation:check.operation,result:"passed",checks:0},{operation:check.operation,result:"failed",checks:12}]) assert.throws(()=>completedEvidence(check,[record]));
});

test("closed layout inventory requires every actual completion count and disables optional images and narrow modes",()=>{
 const env=browserEnvironment({...source,DELIDEV_LAYOUT_SCREENSHOT_DIR:"/tmp/images",DELIDEV_LAYOUT_SCREENSHOT:"/tmp/image.png",DELIDEV_SEARCH_SCREENSHOT_DIR:"/tmp/images",DELIDEV_PR_CARDS_SCREENSHOT_DIR:"/tmp/images",DELIDEV_LAYOUT_GITHUB_ONLY:"1"},"/fixture/checkout");
 for(const key of ["DELIDEV_LAYOUT_SCREENSHOT_DIR","DELIDEV_LAYOUT_SCREENSHOT","DELIDEV_SEARCH_SCREENSHOT_DIR","DELIDEV_PR_CARDS_SCREENSHOT_DIR","DELIDEV_LAYOUT_GITHUB_ONLY"]) assert.equal(env[key],undefined);
 for(const check of browserChecks.filter(value=>value.counts)) {
  const record={...(check.operation?{operation:check.operation}:{evidence:check.evidence}),...(check.passed?{result:"passed"}:{}),...Object.fromEntries(check.counts.map(field=>[field,1]))};
  assert.equal(completedEvidence(check,[record]),record);
  for(const field of check.counts) assert.throws(()=>completedEvidence(check,[{...record,[field]:0}]));
  assert.throws(()=>completedEvidence(check,[]));
 }
});

test("usage and session tabs execute existing full fixtures without optional images",()=>{
 const env=browserEnvironment({...source,DELIDEV_USAGE_SCREENSHOT_DIR:"/tmp/usage-images",DELIDEV_BROWSER_SCREENSHOT_DIR:"/tmp/tab-images"},"/fixture/checkout");
 assert.equal(env.DELIDEV_USAGE_SCREENSHOT_DIR,undefined);assert.equal(env.DELIDEV_BROWSER_SCREENSHOT_DIR,undefined);
 for(const [id,operation,field] of [["usage-layout","usage-layout-browser","checks"],["session-tabs-layout","session-tabs-layout","cases"]]) {
  const check=browserChecks.find(value=>value.id===id);assert.equal(check.operation,operation);assert.deepEqual(check.counts,[field]);assert.equal(check.passed,true);
  const record={operation,result:"passed",[field]:24};assert.equal(completedEvidence(check,[record]),record);
  for(const replacement of [{[field]:0},{[field]:undefined},{result:"failed"}]) assert.throws(()=>completedEvidence(check,[{...record,...replacement}]));
 }
});

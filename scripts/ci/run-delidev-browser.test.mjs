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
 assert.deepEqual(browserChecks.map(value=>value.script),["apps/delidev/scripts/qa/test-browser.mjs","apps/delidev/scripts/test-worker-model-layout.mjs","apps/delidev/scripts/test-command-menu-layout.mjs"]);
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
 const worker=browserChecks[1],menu=browserChecks[2];
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

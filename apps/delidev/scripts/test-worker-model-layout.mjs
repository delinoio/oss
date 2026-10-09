// SPDX-License-Identifier: Apache-2.0
// Synthetic geometry fixture uses the committed product styles. Supply the
// host's Playwright module; it is not a product or workspace dependency.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";
const module = process.env.DELIDEV_QA_PLAYWRIGHT_MODULE;
const { chromium } = await import(module ? pathToFileURL(module).href : "playwright");
const styles = await Promise.all(["themes", "styles", "settings-task", "agent-worker-wizard"].map(name => readFile(new URL(`../src/${name}.css`, import.meta.url), "utf8")));
const browser = await chromium.launch({ headless: true, channel: process.env.DELIDEV_QA_BROWSER_CHANNEL ?? "chrome" });
let checks = 0;
try {
  for (const viewport of [{ width: 1440, height: 900 }, { width: 960, height: 640 }, { width: 640, height: 480 }, { width: 320, height: 240 }]) for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const routed of [false, true]) {
    const page = await browser.newPage({ viewport });
    const label = language === "en" ? "Model" : "모델", status = language === "en" ? "Loading model catalog…" : "모델 목록을 불러오는 중…";
    const source = number => `<section class="worker-source-group"><fieldset><header><h4>Source ${number}</h4></header><div class="worker-model-combobox"><label>${label}</label><input role="combobox" value="native-model-${number}"/><div class="worker-model-results" tabindex="0"></div></div><button class="reload">Reload</button><p>Native observation remains explicit.</p></fieldset></section>`;
    await page.setContent(`<html lang="${language}" data-theme="${theme}"><style>${styles.join("\n")}</style><body><dialog class="settings-task-dialog" data-size="wide"><header class="settings-task-header"><h2>New Agent Worker</h2></header><div class="settings-task-body"><form class="agent-configuration worker-wizard ${routed ? "worker-source-wizard" : ""}"><ol class="worker-steps"><li>Harness</li><li>Accounts</li><li>Model</li><li>Configure</li></ol><h3>${label}</h3>${source(1)}${routed ? source(2) : ""}</form></div><footer class="settings-task-footer"><button>Next</button></footer></dialog></body></html>`);
    await page.evaluate(() => document.querySelector("dialog").showModal());
    const snapshot = () => page.evaluate(() => {
      const rect = element => { const { x, y, width, height } = element.getBoundingClientRect(); return { x, y, width, height }; };
      return { dialog: rect(document.querySelector("dialog")), inputs: [...document.querySelectorAll('[role="combobox"]')].map(rect), reload: [...document.querySelectorAll(".reload")].map(rect), footer: rect(document.querySelector("footer")), heights: [...document.querySelectorAll(".worker-model-results")].map(element => element.getBoundingClientRect().height), bodyScroll: document.querySelector(".settings-task-body").scrollTop };
    });
    const baseline = await snapshot();
    for (const state of ["eight", "loading", "one", "empty", "failed", "closed"]) {
      await page.evaluate(({ state, status }) => {
        const region = document.querySelector(".worker-model-results"), rows = state === "eight" ? 8 : state === "one" ? 1 : 0;
        region.innerHTML = state === "closed" ? "" : `<ul role="listbox">${Array.from({ length: rows }, (_, index) => `<li role="option"><strong>OpenAI: GPT-5.6 Luna ${index}</strong><small>${"long-native-id-".repeat(8)}${index}</small></li>`).join("")}<li role="option">Use exact ID</li><li role="presentation"><button>Load more</button></li></ul><p role="status">${state === "loading" ? status : state === "failed" ? status.repeat(8) : state === "empty" ? "No matching models" : "Catalog result"}</p>`;
      }, { state, status });
      const current = await snapshot();
      for (const key of ["dialog", "footer"]) for (const field of ["x", "y", "width", "height"]) assert(Math.abs(current[key][field] - baseline[key][field]) <= 1, `${state} ${key}.${field}`);
      for (const key of ["inputs", "reload"]) current[key].forEach((rect, index) => Object.keys(rect).forEach(field => assert(Math.abs(rect[field] - baseline[key][index][field]) <= 1, `${state} ${key}${index}.${field}`)));
      assert.deepEqual(current.heights, routed ? [280, 280] : [280]); assert.equal(current.bodyScroll, baseline.bodyScroll); checks++;
    }
    assert(await page.locator(".worker-model-results").first().evaluate(element => getComputedStyle(element).overflowY === "auto" && element.scrollWidth <= element.clientWidth + 1));
    await page.locator(".worker-model-results").first().focus(); assert(await page.locator(".worker-model-results").first().evaluate(element => element === document.activeElement));
    await page.keyboard.press("Tab"); assert(await page.locator(".reload").first().evaluate(element => element === document.activeElement));
    await page.close();
  }
  console.log(JSON.stringify({ operation: "worker-model-layout", result: "passed", checks, viewports: 4, languages: 2, themes: 2, generations: 2, nativeAcceptance: "not-performed" }));
} finally { await browser.close(); }

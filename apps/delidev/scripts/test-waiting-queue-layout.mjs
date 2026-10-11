// SPDX-License-Identifier: Apache-2.0
// Synthetic product markup and committed CSS; no native or account acceptance.
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";

const app = resolve(dirname(fileURLToPath(import.meta.url)), "..");
const modulePath = process.env.DELIDEV_LAYOUT_PLAYWRIGHT_MODULE;
const { chromium } = await import(modulePath ? pathToFileURL(resolve(modulePath)).href : "playwright");
const css = (await Promise.all(["themes.css", "styles.css", "session.css"].map(name => readFile(resolve(app, "src", name), "utf8")))).join("\n");
const browser = await chromium.launch({ headless: true });
let cases = 0;
try {
  const page = await browser.newPage();
  for (const language of ["en", "ko"]) for (const theme of ["light", "dark"]) for (const width of [1200, 600, 320]) for (const scale of [1, 2]) for (const count of [1, 3]) {
    await page.setViewportSize({ width, height: 640 });
    const prompt = language === "ko" ? "테스트" : "Test", steer = language === "ko" ? "조정" : "Steer";
    const rows = Array.from({ length: count }, (_, index) => `<div class="queue-row-slot"><article class="queue-item queue-compact-item"><div class="queue-row"><button class="queue-drag-handle">⠿</button><p class="queue-preview">${prompt}</p><div class="queue-compact-actions"><button>${steer}</button><button class="queue-icon">⌫</button><div class="queue-more"><button class="queue-icon">…</button></div></div></div></article>${index === count - 1 ? '<div class="queue-drop-end" data-dragging="false"></div>' : ""}</div>`).join("");
    await page.setContent(`<html lang="${language}" data-theme="${theme}"><style>${css}${scale === 2 ? '.queue-compact-list .queue-preview { font-size:28px; } .queue-compact-list button { font-size:24px; line-height:40px; }' : ""}</style><body><div class="session-workspace" style="display:block;height:auto"><div class="queue-compact-list"><div data-payload-page="">${rows}</div><div class="sidebar-continuation"></div></div></div></body></html>`);
    const measure = () => page.locator(".queue-compact-list").evaluate(list => {
      const box = list.getBoundingClientRect();
      const rows = [...list.querySelectorAll(".queue-row")].map(row => {
        const rect = row.getBoundingClientRect();
        const children = [...row.querySelectorAll(":scope > .queue-drag-handle, :scope > .queue-preview, .queue-compact-actions button")];
        return { top: rect.top, bottom: rect.bottom, height: rect.height, centers: children.map(child => { const r = child.getBoundingClientRect(); return (r.top + r.bottom) / 2; }) };
      });
      const target = list.querySelector(".queue-drop-end"), drop = target.getBoundingClientRect(), slot = target.parentElement.getBoundingClientRect();
      return { top: rows[0].top - box.top, bottom: box.bottom - rows.at(-1).bottom, height: box.height, rows, sentinel: list.querySelector(".sidebar-continuation").getBoundingClientRect().height, drop: { top: drop.top, bottom: drop.bottom, height: drop.height, slotTop: slot.top, slotBottom: slot.bottom, pointerEvents: getComputedStyle(target).pointerEvents } };
    });
    const resting = await measure();
    assert(Math.abs(resting.top - resting.bottom) <= 1, JSON.stringify(resting));
    assert.equal(resting.sentinel, 0); assert.equal(resting.drop.pointerEvents, "none");
    if (width > 760 && scale === 1) for (const row of resting.rows) {
      assert.equal(row.height, 40);
      assert(Math.max(...row.centers) - Math.min(...row.centers) <= 1, JSON.stringify(row));
    }
    await page.locator(".queue-drop-end").evaluate(target => { target.dataset.dragging = "true"; target.classList.add("queue-insertion"); });
    const dragging = await measure();
    assert.equal(dragging.height, resting.height); assert.equal(dragging.drop.height, 8);
    assert.equal(dragging.drop.pointerEvents, "auto");
    assert(dragging.drop.top >= dragging.drop.slotTop && dragging.drop.bottom <= dragging.drop.slotBottom, JSON.stringify(dragging.drop));
    for (const content of ['<p role="status">Loading…</p>', '<button>Load more</button>', '<p>Read failed</p><button>Retry</button>']) {
      await page.locator(".sidebar-continuation").evaluate((anchor, content) => { anchor.innerHTML = content; }, content);
      assert((await measure()).sentinel > 0, "Visible continuation controls must keep their space");
    }
    cases++;
  }
  console.log(JSON.stringify({ operation: "compact-waiting-queue-geometry", result: "passed", cases, locales: 2, themes: 2, widths: 3, textScales: 2, nativeAcceptance: "not-performed" }));
} finally { await browser.close(); }

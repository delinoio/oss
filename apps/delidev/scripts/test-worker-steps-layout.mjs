// SPDX-License-Identifier: Apache-2.0
// Synthetic geometry fixture: the committed product stylesheets and the Agent
// Worker step markup (agent-worker-source-wizard.tsx), mounted in the Settings
// task dialog host and the Settings page host. This is not the mounted app or
// the packaged native build. Supply the host's Playwright module; it is not a
// product or workspace dependency (DELIDEV_QA_PLAYWRIGHT_MODULE, plus
// DELIDEV_QA_BROWSER_CHANNEL, default "chrome").
import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import { pathToFileURL } from "node:url";

const module = process.env.DELIDEV_QA_PLAYWRIGHT_MODULE;
const { chromium } = await import(
  module ? pathToFileURL(module).href : "playwright"
);
const read = (path) => readFile(new URL(path, import.meta.url), "utf8");
// Same order as the app: shared tokens and layout first, then the hosts, then
// the shared wizard presentation, then the worker sheet last. The worker sheet
// imports harness-mark.css, which the step strip does not use.
const styles = (
  await Promise.all(
    [
      "themes",
      "styles",
      "settings-task",
      "settings-presentation",
      "wizard-presentation",
      "agent-worker-wizard",
    ].map((name) => read(`../src/${name}.css`)),
  )
)
  .join("\n")
  .replace(/@import[^;]+;/g, "");
const labels = {};
for (const language of ["en", "ko"]) {
  const copy = JSON.parse(
    await read(`../src/locales/${language}/agent-worker-wizard.json`),
  );
  labels[language] = [
    copy["agent-worker-wizard.harnessStep"],
    copy["agent-worker-wizard.accountsStep"],
    copy["agent-worker-wizard.modelStep"],
    copy["agent-worker-wizard.configureStep"],
  ];
}

const steps = (language, current, visited) =>
  `<ol class="worker-steps wizard-progress" aria-label="Steps">${labels[
    language
  ]
    .map((label, index) => {
      const value = index + 1;
      const inner = `<span class="wizard-step-number" aria-hidden="true">${value}</span><span>${label}</span>`;
      return `<li data-current="${value === current}" data-completed="${value < current}">${
        value <= visited
          ? `<button type="button" class="worker-step-target"${value === current ? ' aria-current="step"' : ""}>${inner}</button>`
          : `<span class="worker-step-target">${inner}</span>`
      }</li>`;
    })
    .join("")}</ol>`;

const wizard = (language, current, visited) =>
  `<form class="desktop-wizard worker-wizard worker-source-wizard">${steps(language, current, visited)}<h3>${labels[language][current - 1]}</h3><button type="button">Next</button></form>`;

const HOSTS = {
  dialog: (body) =>
    `<dialog class="settings-task-dialog" data-size="wide"><header class="settings-task-header"><h2>New Agent Worker</h2></header><div class="settings-task-body">${body}</div><footer class="settings-task-footer"></footer></dialog>`,
  page: (body) =>
    `<div class="settings-layout"><main class="settings-content" aria-label="Agent Workers"><div class="settings-content-column">${body}</div></main></div>`,
};

const browser = await chromium.launch({
  headless: true,
  channel: process.env.DELIDEV_QA_BROWSER_CHANNEL ?? "chrome",
});

// Measured inside the page. Returns plain geometry; assertions stay in Node.
const measure = () => {
  const rect = (element) => {
    const { x, y, width, height, right, bottom } =
      element.getBoundingClientRect();
    return { x, y, width, height, right, bottom };
  };
  const strip = document.querySelector(".worker-steps");
  const form = document.querySelector("form");
  const host = document.querySelector(".settings-task-body, .settings-content");
  const items = [...strip.querySelectorAll("li")].map((li) => {
    const target = li.querySelector(".worker-step-target");
    const number = target.querySelector(".wizard-step-number");
    const label = target.lastElementChild;
    const range = document.createRange();
    range.selectNodeContents(label);
    const lines = new Set(
      [...range.getClientRects()].map((r) => Math.round(r.top)),
    );
    return {
      isButton: target.tagName === "BUTTON",
      text: label.textContent,
      target: rect(target),
      item: rect(li),
      number: rect(number),
      label: rect(label),
      lineCount: lines.size,
      labelScrollWidth: label.scrollWidth,
      labelClientWidth: label.clientWidth,
      whiteSpace: getComputedStyle(label).whiteSpace,
      targetPaddingRight: parseFloat(getComputedStyle(target).paddingRight),
      targetPaddingLeft: parseFloat(getComputedStyle(target).paddingLeft),
      position: getComputedStyle(target).position,
    };
  });
  return {
    items,
    strip: rect(strip),
    form: rect(form),
    host: rect(host),
    hostClientWidth: host.clientWidth,
    hostScrollWidth: host.scrollWidth,
    docClientWidth: document.documentElement.clientWidth,
    docScrollWidth: document.documentElement.scrollWidth,
  };
};

const intersects = (a, b) =>
  a.x < b.right - 0.01 &&
  b.x < a.right - 0.01 &&
  a.y < b.bottom - 0.01 &&
  b.y < a.bottom - 0.01;

let checks = 0;
const check = (condition, message) => {
  assert(condition, message);
  checks += 1;
};

function assertGeometry(geometry, context, expectedRows) {
  const { items } = geometry;
  check(items.length === 4, `${context}: four steps`);
  for (const item of items) {
    const where = `${context} "${item.text}"`;
    // Atomic label: one text line, no scroll or clip, no inner wrapping.
    check(item.lineCount === 1, `${where}: label on one line`);
    check(item.whiteSpace === "nowrap", `${where}: label is nowrap`);
    check(
      item.labelScrollWidth <= item.labelClientWidth + 1,
      `${where}: label not clipped`,
    );
    check(item.label.height <= 20.5, `${where}: label is one 20px line`);
    // The number and label stay together on one line inside the target.
    check(
      Math.abs(
        item.number.y +
          item.number.height / 2 -
          (item.label.y + item.label.height / 2),
      ) <= 1,
      `${where}: number and label share a row`,
    );
    check(
      item.number.width === 28 && item.number.height === 28,
      `${where}: 28px circle, got ${item.number.width}x${item.number.height}`,
    );
    check(
      item.label.right <= item.target.right - item.targetPaddingRight + 0.5,
      `${where}: label fits inside its target`,
    );
    check(
      Math.abs(item.label.x - (item.number.right - 0 + 8)) <= 1 ||
        Math.abs(item.label.x - (item.number.x + 28 + 8)) <= 1,
      `${where}: 8px number-to-label gap`,
    );
    // The hit area stays inside the host content area.
    if (item.isButton) {
      check(
        item.target.x >= geometry.host.x - 0.5 &&
          item.target.right <= geometry.host.right + 0.5,
        `${where}: hit target stays in the host`,
      );
      check(item.target.height >= 36 - 0.5, `${where}: hit height >= 36px`);
    }
  }
  // Source order is row-major order, and rows break between whole groups.
  const ordered = [...items].sort(
    (a, b) =>
      Math.round(a.item.y) - Math.round(b.item.y) || a.item.x - b.item.x,
  );
  check(
    ordered.every((item, index) => item === items[index]),
    `${context}: groups keep source order`,
  );
  const rows = new Set(items.map((item) => Math.round(item.item.y))).size;
  if (expectedRows !== undefined)
    check(
      rows === expectedRows,
      `${context}: ${rows} rows, expected ${expectedRows}`,
    );
  // No pair of hit targets overlaps; groups on one row keep their 24px gap.
  const buttons = items.filter((item) => item.isButton);
  for (let a = 0; a < buttons.length; a += 1)
    for (let b = a + 1; b < buttons.length; b += 1)
      check(
        !intersects(buttons[a].target, buttons[b].target),
        `${context}: hit targets ${buttons[a].text}/${buttons[b].text} overlap`,
      );
  for (let index = 1; index < items.length; index += 1) {
    const previous = items[index - 1].item;
    const next = items[index].item;
    if (Math.round(previous.y) === Math.round(next.y))
      check(
        Math.abs(next.x - previous.right - 24) <= 1,
        `${context}: 24px gap between groups on a row`,
      );
  }
  // No horizontal overflow at the document or the host.
  check(
    geometry.docScrollWidth <= geometry.docClientWidth + 1,
    `${context}: no document overflow (${geometry.docScrollWidth} > ${geometry.docClientWidth})`,
  );
  check(
    geometry.hostScrollWidth <= geometry.hostClientWidth + 1,
    `${context}: no host overflow`,
  );
  return rows;
}

try {
  // 1. 720px host: every EN/KO current/visited combination stays on one row
  //    of four whole labels, in both hosts and both themes.
  for (const host of Object.keys(HOSTS))
    for (const language of ["en", "ko"])
      for (const theme of ["light", "dark"]) {
        const page = await browser.newPage({
          viewport: { width: 1440, height: 900 },
        });
        for (let current = 1; current <= 4; current += 1)
          for (let visited = current; visited <= 4; visited += 1) {
            await page.setContent(
              `<html lang="${language}" data-theme="${theme}"><style>${styles}</style><body>${HOSTS[host](wizard(language, current, visited))}</body></html>`,
            );
            if (host === "dialog")
              await page.evaluate(() =>
                document.querySelector("dialog").showModal(),
              );
            const geometry = await page.evaluate(measure);
            const context = `${host}/${language}/${theme}/current${current}/visited${visited}@720`;
            check(
              geometry.form.width >= 600 && geometry.form.width <= 720.5,
              `${context}: wizard width ${geometry.form.width} is the 720px bound`,
            );
            assertGeometry(geometry, context, 1);
          }
        await page.close();
      }

  // 2. Responsive widths at 100% and 200% browser zoom. A browser at 200% zoom
  //    lays the page out at half the window width in CSS pixels, so zoom is
  //    emulated with the halved CSS viewport. The native window minimum is
  //    960px, which is 480 CSS px at 200% zoom.
  for (const host of Object.keys(HOSTS))
    for (const language of ["en", "ko"])
      for (const theme of ["light", "dark"])
        for (const zoom of [1, 2])
          for (const width of [320, 640, 641, 760, 761]) {
            const cssWidth = Math.round(width / zoom);
            // 320px at 200% would be a 160 CSS px viewport, narrower than one
            // step group and far below the 960px native window minimum, so
            // that single combination is not a supported surface.
            if (cssWidth < 320) continue;
            const page = await browser.newPage({
              viewport: { width: cssWidth, height: 800 },
            });
            for (const [current, visited] of [
              [1, 1],
              [2, 4],
              [4, 4],
            ]) {
              await page.setContent(
                `<html lang="${language}" data-theme="${theme}"><style>${styles}</style><body>${HOSTS[host](wizard(language, current, visited))}</body></html>`,
              );
              if (host === "dialog")
                await page.evaluate(() =>
                  document.querySelector("dialog").showModal(),
                );
              const geometry = await page.evaluate(measure);
              assertGeometry(
                geometry,
                `${host}/${language}/${theme}/zoom${zoom * 100}/${width}px(css ${cssWidth})/current${current}/visited${visited}`,
              );
            }
            await page.close();
          }

  // 3. Wrapped-row boundary: shrink the host in 1px steps; at every width the
  //    labels stay whole, the row count only grows, and the first split happens
  //    between groups. Also report the width where rows first split.
  for (const host of Object.keys(HOSTS)) {
    const page = await browser.newPage({
      viewport: { width: 1440, height: 900 },
    });
    await page.setContent(
      `<html lang="en" data-theme="light"><style>${styles}</style><body>${HOSTS[host](wizard("en", 2, 4))}</body></html>`,
    );
    if (host === "dialog")
      await page.evaluate(() => document.querySelector("dialog").showModal());
    let previousRows = 1;
    let firstSplit = null;
    for (let width = 720; width >= 200; width -= 1) {
      await page.setViewportSize({ width: width + 120, height: 900 });
      const geometry = await page.evaluate(measure);
      const rows = assertGeometry(
        geometry,
        `${host}/boundary/viewport${width + 120}`,
      );
      check(rows >= previousRows, `${host}: rows never shrink while narrowing`);
      if (rows > 1 && firstSplit === null) firstSplit = width + 120;
      previousRows = rows;
    }
    console.log(
      `${host}: strip first splits into rows at viewport ${firstSplit}px`,
    );
    await page.close();
  }

  // 4. Focus ring, hover and interaction semantics on the real cascade.
  for (const host of Object.keys(HOSTS))
    for (const theme of ["light", "dark"]) {
      const page = await browser.newPage({
        viewport: { width: 1440, height: 900 },
      });
      await page.setContent(
        `<html lang="en" data-theme="${theme}"><style>${styles}</style><body>${HOSTS[host](wizard("en", 2, 3))}</body></html>`,
      );
      if (host === "dialog")
        await page.evaluate(() => document.querySelector("dialog").showModal());
      const buttons = page.locator(".worker-steps button.worker-step-target");
      check((await buttons.count()) === 3, `${host}: three buttons (1..3)`);
      check(
        (await page
          .locator(".worker-steps span.worker-step-target")
          .count()) === 1,
        `${host}: one future span`,
      );
      check(
        (await buttons.nth(1).getAttribute("aria-current")) === "step",
        `${host}: current step has aria-current`,
      );
      check(
        (await page
          .locator(".worker-steps span.worker-step-target")
          .evaluate((node) => node.tabIndex)) === -1,
        `${host}: future step is not focusable`,
      );
      await page.keyboard.press("Tab");
      const focused = await page.evaluate(() => {
        const style = getComputedStyle(document.activeElement);
        return {
          isStep:
            document.activeElement.classList.contains("worker-step-target"),
          width: style.outlineWidth,
          style: style.outlineStyle,
        };
      });
      check(
        focused.isStep && focused.width === "2px" && focused.style === "solid",
        `${host}/${theme}: first Tab lands on a step with a 2px solid outline`,
      );
      await buttons.nth(0).hover();
      const hover = await buttons
        .nth(0)
        .evaluate((node) => getComputedStyle(node).backgroundColor);
      check(
        hover !== "rgba(0, 0, 0, 0)" && hover !== "transparent",
        `${host}/${theme}: hover background paints (${hover})`,
      );
      await page.close();
    }
} finally {
  await browser.close();
}
console.log(`worker steps layout: ${checks} checks passed`);

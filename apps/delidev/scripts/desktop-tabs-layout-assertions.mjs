// SPDX-License-Identifier: Apache-2.0
// Synthetic renderer geometry is separate from installed platform acceptance.
import assert from "node:assert/strict";
export async function assertDesktopTabs(page, selector = ".desktop-tab-strip:visible") {
  const strips = page.locator(selector);
  assert(await strips.count() > 0, "Shared tab strip is present");
  for (const strip of await strips.all()) {
    const rows = await strip.evaluate(root => {
      const scale = parseFloat(getComputedStyle(document.body).zoom) || 1;
      return [...root.querySelectorAll(".desktop-tab-item")].map(item => {
        const label = item.matches(".desktop-tab-label") ? item : item.querySelector(".desktop-tab-label"), close = item.querySelector(".desktop-tab-close");
        const css = getComputedStyle(label), shell = item.getBoundingClientRect(), box = label.getBoundingClientRect(), underline = getComputedStyle(item, "::after"), target = close?.getBoundingClientRect();
        const selected = item.matches('.is-selected, [aria-selected="true"], [aria-current="page"]');
        return { scale, height: box.height, radius: css.borderRadius, background: css.backgroundColor, border: css.borderTopWidth, font: css.fontSize, line: css.lineHeight, padding: css.paddingLeft, selected, underline: underline.backgroundColor, underlineHeight: underline.height, itemWidth: shell.width, dynamic: item.classList.contains("desktop-tab-dynamic"), close: target ? { width: target.width, height: target.height, overlaps: box.right > target.left + 1, outside: target.right > shell.right + 1 } : null };
      });
    });
    for (const row of rows) {
      assert(row.height >= 40 * row.scale - 1, JSON.stringify(row));
      assert.equal(row.radius, "0px"); assert.equal(row.background, "rgba(0, 0, 0, 0)"); assert.equal(row.border, "0px");
      assert.equal(row.font, "14px"); assert.equal(row.line, "20px"); assert.equal(row.padding, "16px"); assert.equal(row.underlineHeight, "2px");
      assert.equal(row.underline === "rgba(0, 0, 0, 0)", !row.selected);
      if (row.dynamic) assert(row.itemWidth <= 320 * row.scale + 1, JSON.stringify(row));
      if (row.close) assert(Math.abs(row.close.width - 40 * row.scale) <= 1 && row.close.height >= 40 * row.scale - 1 && !row.close.overlaps && !row.close.outside, JSON.stringify(row));
    }
    assert(await strip.evaluate(root => getComputedStyle(root).flexWrap === "nowrap" && getComputedStyle(root).gap === "0px" && getComputedStyle(root).overflowX === "auto"));
  }
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), "Tabs do not overflow the document");
}

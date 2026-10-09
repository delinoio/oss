// SPDX-License-Identifier: Apache-2.0
import { readFileSync } from "node:fs";
import { expect, it } from "vitest";

it("keeps both grouped checkout disclosure indicators inside their clipped row", () => {
 const style = document.createElement("style");
 style.textContent = readFileSync("src/disclosure.css", "utf8") + readFileSync("src/repository-registration.css", "utf8");
 const group = document.createElement("div");group.className="repository-checkout-group";
 const buttons = ["Existing checkout", "New checkout"].map(label => { const button=document.createElement("button");button.className="disclosure-header repository-checkout-toggle";button.textContent=label;group.append(button);return button; });
 document.head.append(style);document.body.append(group);
 try {
  for (const button of buttons) {
   button.focus();expect(button.matches(":focus-visible")).toBe(true);
   const computed=getComputedStyle(button);
   expect(computed.outlineOffset).toBe("-4px");
   expect(computed.outline).toBe("2px solid var(--accent)");
   expect(parseFloat(computed.outlineOffset)).toBeLessThanOrEqual(-2);
  }
 } finally { group.remove();style.remove(); }
});

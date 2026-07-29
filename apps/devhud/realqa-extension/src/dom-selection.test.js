import { afterEach, describe, expect, it, vi } from "vitest";

import { selectDomBoundary } from "./dom-selection.js";

afterEach(() => {
  document.body.replaceChildren();
});

describe("RealQA DOM selection", () => {
  it("selects the clicked element without requiring prior pointer movement", async () => {
    const button = document.createElement("button");
    button.id = "capture-target";
    button.setAttribute("aria-label", "Capture target");
    button.getBoundingClientRect = vi.fn(() => ({
      bottom: 60,
      height: 40,
      left: 10,
      right: 110,
      top: 20,
      width: 100,
      x: 10,
      y: 20,
      toJSON: () => undefined,
    }));
    document.body.append(button);

    const selection = selectDomBoundary();
    button.dispatchEvent(
      new MouseEvent("click", { bubbles: true, cancelable: true }),
    );

    await expect(selection).resolves.toMatchObject({
      accessibleName: "Capture target",
      boundary: { x: 10, y: 20, width: 100, height: 40 },
      selector: "button#capture-target",
      tag: "button",
    });
  });
});

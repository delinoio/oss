// SPDX-License-Identifier: Apache-2.0
import { render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { useEntranceActivation } from "./entrance-motion";
import { useRepositoryDetailsPopup } from "./repository-details-popup";

function Harness() {
  const entrance = useEntranceActivation("page:1");
  const { trigger, popup } = useRepositoryDetailsPopup({
    expanded: true,
    active: true,
    contentKey: "details",
  });
  return (
    <section data-testid="page" {...entrance}>
      <div>
        <button ref={trigger} type="button">
          Repository
        </button>
      </div>
      <div ref={popup} hidden>
        Details
      </div>
    </section>
  );
}

const originalMatches = Element.prototype.matches;
beforeEach(() => {
  // jsdom has no native popover layer or layout; stub only what placement reads.
  for (const name of ["showPopover", "hidePopover"])
    Object.defineProperty(HTMLElement.prototype, name, {
      configurable: true,
      writable: true,
      value: () => {},
    });
  vi.spyOn(Element.prototype, "matches").mockImplementation(function (
    this: Element,
    selector: string,
  ) {
    return selector === ":popover-open"
      ? true
      : originalMatches.call(this, selector);
  });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({
    left: 10,
    right: 200,
    top: 10,
    bottom: 40,
    width: 190,
    height: 30,
    x: 10,
    y: 10,
    toJSON: () => ({}),
  } as DOMRect);
  vi.spyOn(HTMLElement.prototype, "getClientRects").mockImplementation(
    () => [{}] as unknown as DOMRectList,
  );
});
afterEach(() => {
  vi.restoreAllMocks();
  for (const name of ["showPopover", "hidePopover"])
    delete (HTMLElement.prototype as unknown as Record<string, unknown>)[name];
});

it("settles the enclosing page entrance before the first details placement", () => {
  render(<Harness />);
  expect(screen.getByTestId("page").hasAttribute("data-entrance")).toBe(false);
});

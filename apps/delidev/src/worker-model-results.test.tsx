// SPDX-License-Identifier: Apache-2.0
import { useRef } from "react";
import { fireEvent, render } from "@testing-library/react";
import { expect, it, vi } from "vitest";
import { revealWorkerModelOption } from "./worker-model-results";
import { ScrollContinuation } from "./scroll-continuation";
it("scrolls only the reserved owner and rejects closed or hidden ancestors", () => {
  const outer = document.createElement("div"), details = document.createElement("details"), region = document.createElement("div"), option = document.createElement("li");
  region.className = "worker-model-results"; outer.scrollTop = 90; region.scrollTop = 20;
  outer.append(details); details.append(region); region.append(option); document.body.append(outer);
  region.getBoundingClientRect = () => ({ top: 100, bottom: 380 } as DOMRect);
  option.getBoundingClientRect = () => ({ top: 390, bottom: 430 } as DOMRect);
  revealWorkerModelOption(option); expect(region.scrollTop).toBe(20);
  details.open = true; revealWorkerModelOption(option); expect(region.scrollTop).toBe(70); expect(outer.scrollTop).toBe(90);
  option.getBoundingClientRect = () => ({ top: 80, bottom: 110 } as DOMRect);
  revealWorkerModelOption(option); expect(region.scrollTop).toBe(50); expect(outer.scrollTop).toBe(90);
  option.getBoundingClientRect = () => ({ top: 150, bottom: 900 } as DOMRect);
  revealWorkerModelOption(option); expect(region.scrollTop).toBe(100); expect(outer.scrollTop).toBe(90);
  outer.hidden = true; revealWorkerModelOption(option); expect(region.scrollTop).toBe(100);
  outer.remove();
});
it("uses the bounded result owner for continuation and never reads beneath a closed source", () => {
  const append = vi.fn();
  function View({ loaded = false }: { loaded?: boolean }) {
    const root = useRef<HTMLUListElement>(null);
    return <details><summary>Source</summary><div className="worker-model-results" style={{ overflow: "auto" }}><ul ref={root}><li><ScrollContinuation root={root} active label="Model results" query={{ loaded, nextPageToken: "original-tail", append, retry: vi.fn(), reload: vi.fn() }} /></li></ul></div></details>;
  }
  const { container, rerender } = render(<View />), region = container.querySelector<HTMLElement>(".worker-model-results")!, details = container.querySelector("details")!;
  Object.defineProperty(region, "clientHeight", { value: 280 });
  region.getBoundingClientRect = () => ({ top: 100, bottom: 380 } as DOMRect);
  const anchor = container.querySelector<HTMLElement>("[data-continuation]")!;
  anchor.getBoundingClientRect = () => ({ top: 340, bottom: 370 } as DOMRect);
  rerender(<View loaded />);
  fireEvent.scroll(region); expect(append).not.toHaveBeenCalled();
  details.open = true; fireEvent.scroll(region); expect(append).toHaveBeenCalledOnce();
  details.hidden = true; fireEvent.scroll(region); expect(append).toHaveBeenCalledOnce();
  details.hidden = false; details.open = false; fireEvent.scroll(region); expect(append).toHaveBeenCalledOnce();
});

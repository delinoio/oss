// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { WorkerSourceOrder } from "./worker-source-order";
const original = ["A", "B", "C"], names = new Map(original.map(id => [id, id]));
const transport = createRouterTransport(() => {});
function mount() {
 const change = vi.fn();
 function Harness({ enabled }: { enabled: boolean }) { const [ids, set] = useState(original); return <WorkerSourceOrder ids={ids} names={names} counts={new Map(original.map(id => [id, 1]))} selected="B" select={() => {}} active={enabled} change={value => { change(value); set(value); }} />; }
 const view = (enabled = true, connection = transport) => <TransportProvider transport={connection}><Harness enabled={enabled} /></TransportProvider>;
 const rendered = render(view()); return { ...rendered, change, view };
}
const grip = (id: string) => screen.getByRole("button", { name: new RegExp(`Reorder source .*: ${id}$`) });
const order = () => Array.from(document.querySelectorAll("li[data-source-key]"), node => node.getAttribute("data-source-key"));
it("keeps keyboard preview local, commits exactly once and follows UUID focus", () => {
 const f = mount(); const c = grip("C"); fireEvent.keyDown(c, { key: " " }); fireEvent.keyDown(c, { key: "ArrowUp" }); fireEvent.keyDown(c, { key: "ArrowUp" }); fireEvent.keyDown(c, { key: "ArrowUp" });
 expect(order()).toEqual(["C", "A", "B"]); expect(f.change).not.toHaveBeenCalled(); expect(document.activeElement).toBe(c);
 fireEvent.keyDown(c, { key: "Enter" }); expect(f.change).toHaveBeenCalledExactlyOnceWith(["C", "A", "B"]); expect(document.activeElement).toBe(c);
});
it("Escape consumes the movement and restores snapshot without a draft commit", () => {
 const f = mount(), c = grip("C"); fireEvent.keyDown(c, { key: "Enter" }); fireEvent.keyDown(c, { key: "ArrowUp" });
 const event = new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }); fireEvent(c, event);
 expect(event.defaultPrevented).toBe(true); expect(order()).toEqual(original); expect(f.change).not.toHaveBeenCalled(); expect(screen.getByRole("status").textContent).toContain("canceled");
});
it.each(["inactive", "connection", "disabled", "hidden", "inert", "unmount"])("fences late callbacks after %s", async gate => {
 const f = mount(), c = grip("C"); fireEvent.keyDown(c, { key: " " }); fireEvent.keyDown(c, { key: "ArrowUp" });
 if (gate === "inactive") f.rerender(f.view(false));
 else if (gate === "connection") f.rerender(f.view(true, createRouterTransport(() => {})));
 else if (gate === "unmount") f.unmount();
 else { const list = c.closest("ol")!; if (gate === "disabled") list.setAttribute("aria-disabled", "true"); else list.setAttribute(gate, ""); await act(async () => {}); }
 fireEvent.keyDown(c, { key: "Enter" }); fireEvent.pointerUp(c, { pointerId: 7 }); expect(f.change).not.toHaveBeenCalled(); if (gate !== "unmount") expect(order()).toEqual(original);
});
it("pointer preview shares keyboard commit order; cancellation and same-place drops are no-ops", () => {
 const f = mount(), c = grip("C");
 const pointer = (type: string, pointerId: number, clientY = 10) => { const event = new MouseEvent(type, { bubbles: true, cancelable: true, button: 0, clientY }); Object.defineProperty(event, "pointerId", { value: pointerId }); fireEvent(c, event); };
 document.querySelectorAll("li").forEach((row, index) => vi.spyOn(row, "getBoundingClientRect").mockReturnValue({ top: index * 50, bottom: (index + 1) * 50 } as DOMRect));
 pointer("pointerdown", 7); pointer("pointermove", 7); expect(order()).toEqual(["C", "A", "B"]); expect(f.change).not.toHaveBeenCalled();
 pointer("pointercancel", 7); expect(order()).toEqual(original);
 pointer("pointerdown", 8); pointer("pointerup", 8); expect(f.change).not.toHaveBeenCalled();
 pointer("pointerdown", 9); pointer("pointermove", 9); pointer("pointerup", 9); expect(f.change).toHaveBeenCalledExactlyOnceWith(["C", "A", "B"]);
});
it("disables a singleton grip without hiding its separate selection action", () => {
 render(<TransportProvider transport={transport}><WorkerSourceOrder ids={["A"]} names={names} counts={new Map([["A", 0]])} selected="A" select={vi.fn()} active change={vi.fn()} /></TransportProvider>);
 expect(grip("A")).toHaveProperty("disabled", true); expect(screen.getByRole("button", { name: /1 · A/ })).toHaveProperty("disabled", false);
});
it("rejects late commits after draft membership or order changes", () => {
 const change = vi.fn(); const view = (ids: string[]) => <TransportProvider transport={transport}><WorkerSourceOrder ids={ids} names={names} counts={new Map()} selected="B" select={() => {}} active change={change} /></TransportProvider>;
 const f = render(view(original)), c = grip("C"); fireEvent.keyDown(c, {key:" "}); fireEvent.keyDown(c,{key:"ArrowUp"}); f.rerender(view(["B","A","C"])); fireEvent.keyDown(c,{key:"Enter"}); expect(change).not.toHaveBeenCalled(); expect(order()).toEqual(["B","A","C"]);
 f.rerender(view(original)); fireEvent.keyDown(c,{key:" "}); fireEvent.keyDown(c,{key:"ArrowUp"}); f.rerender(view(["A","C"])); fireEvent.keyDown(c,{key:"Enter"}); expect(change).not.toHaveBeenCalled(); expect(order()).toEqual(["A","C"]);
});
it("keeps pending or uncertain grips visible and disabled", () => {
 render(<TransportProvider transport={transport}><WorkerSourceOrder ids={original} names={names} counts={new Map()} selected="B" select={() => {}} active editable={false} change={vi.fn()} /></TransportProvider>);
 expect(screen.getAllByRole("button",{name:/Reorder source/})).toHaveLength(3); for(const id of original) expect(grip(id)).toHaveProperty("disabled",true);
});

// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { useState, type ReactNode } from "react";
import { expect, it, vi } from "vitest";
import { WorkerSourceOrder } from "./worker-source-order";
const original = ["A", "B", "C"],
  names = new Map(original.map((id) => [id, id]));
const transport = createRouterTransport(() => {});
// The owner renders the card around the supplied grip; the grip stays a sibling.
const card = ({
  id,
  index,
  grip,
}: {
  id: string;
  index: number;
  grip: ReactNode;
}) => (
  <div>
    {grip}
    <strong>
      {index + 1} · {id}
    </strong>
  </div>
);
function mount() {
  const change = vi.fn();
  function Harness({ enabled }: { enabled: boolean }) {
    const [ids, set] = useState(original);
    return (
      <WorkerSourceOrder
        ids={ids}
        names={names}
        active={enabled}
        change={(value) => {
          change(value);
          set(value);
        }}
        renderItem={card}
      />
    );
  }
  const view = (enabled = true, connection = transport) => (
    <TransportProvider transport={connection}>
      <Harness enabled={enabled} />
    </TransportProvider>
  );
  const rendered = render(view());
  return { ...rendered, change, view };
}
const grip = (id: string) =>
  screen.getByRole("button", { name: new RegExp(`Reorder source .*: ${id}$`) });
const order = () =>
  Array.from(document.querySelectorAll("li[data-source-key]"), (node) =>
    node.getAttribute("data-source-key"),
  );
it("keeps keyboard preview local, commits exactly once and follows UUID focus", () => {
  const f = mount();
  const c = grip("C");
  fireEvent.keyDown(c, { key: " " });
  fireEvent.keyDown(c, { key: "ArrowUp" });
  fireEvent.keyDown(c, { key: "ArrowUp" });
  fireEvent.keyDown(c, { key: "ArrowUp" });
  expect(order()).toEqual(["C", "A", "B"]);
  expect(f.change).not.toHaveBeenCalled();
  expect(document.activeElement).toBe(c);
  fireEvent.keyDown(c, { key: "Enter" });
  expect(f.change).toHaveBeenCalledExactlyOnceWith(["C", "A", "B"]);
  expect(document.activeElement).toBe(c);
});
it("Escape consumes the movement and restores snapshot without a draft commit", () => {
  const f = mount(),
    c = grip("C");
  fireEvent.keyDown(c, { key: "Enter" });
  fireEvent.keyDown(c, { key: "ArrowUp" });
  const event = new KeyboardEvent("keydown", {
    key: "Escape",
    bubbles: true,
    cancelable: true,
  });
  fireEvent(c, event);
  expect(event.defaultPrevented).toBe(true);
  expect(order()).toEqual(original);
  expect(f.change).not.toHaveBeenCalled();
  expect(screen.getByRole("status").textContent).toContain("canceled");
});
it.each(["inactive", "connection", "disabled", "hidden", "inert", "unmount"])(
  "fences late callbacks after %s",
  async (gate) => {
    const f = mount(),
      c = grip("C");
    fireEvent.keyDown(c, { key: " " });
    fireEvent.keyDown(c, { key: "ArrowUp" });
    if (gate === "inactive") f.rerender(f.view(false));
    else if (gate === "connection")
      f.rerender(
        f.view(
          true,
          createRouterTransport(() => {}),
        ),
      );
    else if (gate === "unmount") f.unmount();
    else {
      const list = c.closest("ol")!;
      if (gate === "disabled") list.setAttribute("aria-disabled", "true");
      else list.setAttribute(gate, "");
      await act(async () => {});
    }
    fireEvent.keyDown(c, { key: "Enter" });
    fireEvent.pointerUp(c, { pointerId: 7 });
    expect(f.change).not.toHaveBeenCalled();
    if (gate !== "unmount") expect(order()).toEqual(original);
  },
);
it("pointer preview shares keyboard commit order; cancellation and same-place drops are no-ops", () => {
  const f = mount(),
    c = grip("C");
  const pointer = (type: string, pointerId: number, clientY = 10) => {
    const event = new MouseEvent(type, {
      bubbles: true,
      cancelable: true,
      button: 0,
      clientY,
    });
    Object.defineProperty(event, "pointerId", { value: pointerId });
    fireEvent(c, event);
  };
  // Pointer hit-testing reads the card header, which is the grip's parent.
  original.forEach((id, index) =>
    vi.spyOn(grip(id).parentElement!, "getBoundingClientRect").mockReturnValue({
      top: index * 50,
      bottom: (index + 1) * 50,
    } as DOMRect),
  );
  pointer("pointerdown", 7);
  pointer("pointermove", 7);
  expect(order()).toEqual(["C", "A", "B"]);
  expect(f.change).not.toHaveBeenCalled();
  pointer("pointercancel", 7);
  expect(order()).toEqual(original);
  pointer("pointerdown", 8);
  pointer("pointerup", 8);
  expect(f.change).not.toHaveBeenCalled();
  pointer("pointerdown", 9);
  pointer("pointermove", 9);
  pointer("pointerup", 9);
  expect(f.change).toHaveBeenCalledExactlyOnceWith(["C", "A", "B"]);
});
it("hit-tests card headers so an expanded body does not stretch a slot", () => {
  const f = mount(),
    c = grip("C");
  const pointer = (type: string, clientY: number) => {
    const event = new MouseEvent(type, {
      bubbles: true,
      cancelable: true,
      button: 0,
      clientY,
    });
    Object.defineProperty(event, "pointerId", { value: 7 });
    fireEvent(c, event);
  };
  // A is expanded: its 40px header sits above a 360px body, so the full row is
  // taller than its header. B and C headers follow below the body.
  const headers: Record<string, [number, number]> = {
    A: [0, 40],
    B: [400, 440],
    C: [450, 490],
  };
  for (const id of original) {
    const [top, bottom] = headers[id];
    vi.spyOn(grip(id).parentElement!, "getBoundingClientRect").mockReturnValue({
      top,
      bottom,
    } as DOMRect);
  }
  pointer("pointerdown", 450);
  // Pointer inside A's body, below A's header midpoint, passes A only.
  pointer("pointermove", 100);
  expect(order()).toEqual(["A", "C", "B"]);
  // Above A's header midpoint the moving item goes first.
  pointer("pointermove", 10);
  expect(order()).toEqual(["C", "A", "B"]);
  // Past every other header it lands last.
  pointer("pointermove", 480);
  expect(order()).toEqual(["A", "B", "C"]);
  pointer("pointercancel", 450);
  expect(f.change).not.toHaveBeenCalled();
});
it("disables a singleton grip while the owner's card content stays reachable", () => {
  render(
    <TransportProvider transport={transport}>
      <WorkerSourceOrder
        ids={["A"]}
        names={names}
        active
        change={vi.fn()}
        renderItem={card}
      />
    </TransportProvider>,
  );
  expect(grip("A")).toHaveProperty("disabled", true);
  expect(screen.getByText("1 · A")).toBeTruthy();
});
it("rejects late commits after draft membership or order changes", () => {
  const change = vi.fn();
  const view = (ids: string[]) => (
    <TransportProvider transport={transport}>
      <WorkerSourceOrder
        ids={ids}
        names={names}
        active
        change={change}
        renderItem={card}
      />
    </TransportProvider>
  );
  const f = render(view(original)),
    c = grip("C");
  fireEvent.keyDown(c, { key: " " });
  fireEvent.keyDown(c, { key: "ArrowUp" });
  f.rerender(view(["B", "A", "C"]));
  fireEvent.keyDown(c, { key: "Enter" });
  expect(change).not.toHaveBeenCalled();
  expect(order()).toEqual(["B", "A", "C"]);
  f.rerender(view(original));
  fireEvent.keyDown(c, { key: " " });
  fireEvent.keyDown(c, { key: "ArrowUp" });
  f.rerender(view(["A", "C"]));
  fireEvent.keyDown(c, { key: "Enter" });
  expect(change).not.toHaveBeenCalled();
  expect(order()).toEqual(["A", "C"]);
});
it("keeps pending or uncertain grips visible and disabled", () => {
  render(
    <TransportProvider transport={transport}>
      <WorkerSourceOrder
        ids={original}
        names={names}
        active
        editable={false}
        change={vi.fn()}
        renderItem={card}
      />
    </TransportProvider>,
  );
  expect(
    screen.getAllByRole("button", { name: /Reorder source/ }),
  ).toHaveLength(3);
  for (const id of original) expect(grip(id)).toHaveProperty("disabled", true);
});

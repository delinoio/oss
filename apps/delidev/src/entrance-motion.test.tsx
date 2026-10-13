// SPDX-License-Identifier: Apache-2.0
import { StrictMode, useState } from "react";
import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
  EntranceMotion,
  settleEnclosingEntrances,
  useActivationCount,
  useEntranceActivation,
} from "./entrance-motion";

function Harness({ enabled = true }: { enabled?: boolean }) {
  const [activation, setActivation] = useState("first");
  const [text, setText] = useState("");
  const entrance = useEntranceActivation(activation, enabled);
  return (
    <div>
      <div data-testid="surface" {...entrance}>
        <span data-testid="spinner" />
        <input
          aria-label="Draft"
          value={text}
          onChange={(event) => setText(event.target.value)}
        />
      </div>
      <button type="button" onClick={() => setActivation("second")}>
        Re-enter
      </button>
    </div>
  );
}

function reducedMotion(initial: boolean) {
  const listeners = new Set<() => void>();
  const media = {
    matches: initial,
    addEventListener: (_: string, listener: () => void) =>
      listeners.add(listener),
    removeEventListener: (_: string, listener: () => void) =>
      listeners.delete(listener),
  };
  vi.stubGlobal("matchMedia", () => media);
  return (next: boolean) =>
    act(() => {
      media.matches = next;
      listeners.forEach((changed) => changed());
    });
}
function dispatch(node: Element, type: string, name = "entrance-y4") {
  const event = new Event(type, { bubbles: true });
  Object.defineProperty(event, "animationName", { value: name });
  act(() => {
    node.dispatchEvent(event);
  });
}
const surface = () => screen.getByTestId("surface");
const entering = () => surface().getAttribute("data-entrance");
async function setAnimation(value: string | undefined) {
  await act(async () => {
    if (value === undefined) delete document.documentElement.dataset.animation;
    else document.documentElement.dataset.animation = value;
    await Promise.resolve();
  });
}

beforeEach(() => {
  vi.useFakeTimers();
  reducedMotion(false);
});
afterEach(() => {
  vi.useRealTimers();
  vi.unstubAllGlobals();
  delete document.documentElement.dataset.animation;
});

it("plays once on activation and settles on animation end without replay", () => {
  render(<Harness />);
  expect(entering()).toBe("y4");
  fireEvent.change(screen.getByLabelText("Draft"), {
    target: { value: "typed while entering" },
  });
  expect(entering()).toBe("y4");
  dispatch(surface(), "animationend");
  expect(entering()).toBeNull();
  fireEvent.change(screen.getByLabelText("Draft"), {
    target: { value: "typed afterwards" },
  });
  expect(entering()).toBeNull();
  expect((screen.getByLabelText("Draft") as HTMLInputElement).value).toBe(
    "typed afterwards",
  );
});

it("ignores unrelated or bubbled animations and settles on cancel", () => {
  render(<Harness />);
  dispatch(screen.getByTestId("spinner"), "animationend");
  dispatch(surface(), "animationend", "spin");
  expect(entering()).toBe("y4");
  dispatch(surface(), "animationcancel");
  expect(entering()).toBeNull();
});

it("settles through a generation-fenced fallback when no event arrives", () => {
  render(<Harness />);
  act(() => {
    vi.advanceTimersByTime(239);
  });
  expect(entering()).toBe("y4");
  act(() => {
    vi.advanceTimersByTime(1);
  });
  expect(entering()).toBeNull();
});

it("keeps the mounted surface identity while settling", () => {
  render(<Harness />);
  const node = surface();
  dispatch(node, "animationend");
  expect(surface()).toBe(node);
});

it("does not play when Animation is Off or the activation is not enabled", async () => {
  await setAnimation("false");
  const { unmount } = render(<Harness />);
  expect(entering()).toBeNull();
  unmount();
  await setAnimation("true");
  render(<Harness enabled={false} />);
  expect(entering()).toBeNull();
});

it("does not play with OS reduced motion", () => {
  reducedMotion(true);
  render(<Harness />);
  expect(entering()).toBeNull();
});

it("settles mid-flight when Animation turns Off and never replays on re-enable", async () => {
  await setAnimation("true");
  render(<Harness />);
  expect(entering()).toBe("y4");
  await setAnimation("false");
  expect(entering()).toBeNull();
  await setAnimation("true");
  expect(entering()).toBeNull();
  act(() => {
    vi.advanceTimersByTime(1000);
  });
  expect(entering()).toBeNull();
});

it("settles mid-flight for OS reduced motion and does not replay when it clears", () => {
  const change = reducedMotion(false);
  render(<Harness />);
  expect(entering()).toBe("y4");
  change(true);
  expect(entering()).toBeNull();
  change(false);
  expect(entering()).toBeNull();
});

it("replays only for a new explicit activation, even before the old one finishes", () => {
  render(<Harness />);
  fireEvent.click(screen.getByRole("button", { name: "Re-enter" }));
  expect(entering()).toBe("y4");
  // The first activation's fallback was fenced; the second still owns motion.
  act(() => {
    vi.advanceTimersByTime(239);
  });
  expect(entering()).toBe("y4");
  dispatch(surface(), "animationend");
  expect(entering()).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Re-enter" }));
  expect(entering()).toBeNull();
});

function trackDeclaration(node: Element) {
  const writes: string[] = [];
  const remove = node.removeAttribute.bind(node);
  const set = node.setAttribute.bind(node);
  node.removeAttribute = (name: string) => {
    if (name === "data-entrance") writes.push("remove");
    remove(name);
  };
  node.setAttribute = (name: string, value: string) => {
    if (name === "data-entrance") writes.push(`set:${value}`);
    set(name, value);
  };
  return writes;
}

it("restarts the CSS animation when a new activation arrives mid-flight", () => {
  render(<Harness />);
  const node = surface();
  const writes = trackDeclaration(node);
  fireEvent.click(screen.getByRole("button", { name: "Re-enter" }));
  // An unchanged declaration would continue the old timeline; the attribute is
  // reset and re-declared so the browser starts a fresh 160ms animation.
  expect(writes).toEqual(["remove", "set:y4"]);
  expect(entering()).toBe("y4");
  expect(surface()).toBe(node);
});

it("does not restart for renders or edits inside one activation", () => {
  render(<Harness />);
  const writes = trackDeclaration(surface());
  fireEvent.change(screen.getByLabelText("Draft"), {
    target: { value: "typed while entering" },
  });
  expect(writes).toEqual([]);
});

it("is replay-safe under Strict Mode", () => {
  render(
    <StrictMode>
      <Harness />
    </StrictMode>,
  );
  expect(entering()).toBe("y4");
  dispatch(surface(), "animationend");
  expect(entering()).toBeNull();
});

function MotionHarness({ motion }: { motion: EntranceMotion }) {
  const entrance = useEntranceActivation("only", true, motion);
  return <div data-testid="surface" {...entrance} />;
}

it("settles each 120ms primitive only on its own animation name", () => {
  const { unmount } = render(<MotionHarness motion={EntranceMotion.Y2} />);
  expect(entering()).toBe("y2");
  dispatch(surface(), "animationend", "entrance-y4");
  dispatch(surface(), "animationend", "entrance-x4");
  expect(entering()).toBe("y2");
  dispatch(surface(), "animationend", "entrance-y2");
  expect(entering()).toBeNull();
  unmount();
  const { unmount: unmount2 } = render(
    <MotionHarness motion={EntranceMotion.X4} />,
  );
  expect(entering()).toBe("x4");
  dispatch(surface(), "animationend", "entrance-y2");
  expect(entering()).toBe("x4");
  dispatch(surface(), "animationcancel", "entrance-x4");
  expect(entering()).toBeNull();
  unmount2();
  render(<MotionHarness motion={EntranceMotion.Y4Short} />);
  expect(entering()).toBe("y4-short");
  // The 160ms Y4 and 120ms Y2 names do not settle the short Y4 shape.
  dispatch(surface(), "animationend", "entrance-y4");
  dispatch(surface(), "animationend", "entrance-y2");
  expect(entering()).toBe("y4-short");
  dispatch(surface(), "animationend", "entrance-y4-short");
  expect(entering()).toBeNull();
});

function EdgeHarness({ active }: { active: boolean }) {
  const count = useActivationCount(active);
  return <output data-testid="count">{count}</output>;
}

it("counts rising activation edges only", () => {
  const { rerender } = render(<EdgeHarness active />);
  const count = () => screen.getByTestId("count").textContent;
  expect(count()).toBe("1");
  rerender(<EdgeHarness active />);
  expect(count()).toBe("1");
  rerender(<EdgeHarness active={false} />);
  expect(count()).toBe("1");
  rerender(<EdgeHarness active />);
  expect(count()).toBe("2");
});

function stubAnimations(node: Element, running: string[]) {
  Object.defineProperty(node, "getAnimations", {
    configurable: true,
    value: () =>
      running.map((animationName) => ({ animationName, playState: "running" })),
  });
}

it("ignores the stale cancellation queued by an intentional restart", () => {
  render(<MotionRestartHarness />);
  expect(entering()).toBe("x4");
  fireEvent.click(screen.getByRole("button", { name: "Re-enter" }));
  expect(entering()).toBe("x4");
  // The reset cancelled the old animation after the new one already started.
  stubAnimations(surface(), ["entrance-x4"]);
  dispatch(surface(), "animationcancel", "entrance-x4");
  expect(entering()).toBe("x4");
  // The restarted animation still settles on its own end.
  dispatch(surface(), "animationend", "entrance-x4");
  expect(entering()).toBeNull();
});

it("still settles on a genuine cancellation with no live animation", () => {
  render(<MotionRestartHarness />);
  stubAnimations(surface(), ["entrance-y2"]);
  dispatch(surface(), "animationcancel", "entrance-x4");
  expect(entering()).toBeNull();
});

function MotionRestartHarness() {
  const [activation, setActivation] = useState("first");
  const entrance = useEntranceActivation(activation, true, EntranceMotion.X4);
  return (
    <div>
      <div data-testid="surface" {...entrance} />
      <button type="button" onClick={() => setActivation("second")}>
        Re-enter
      </button>
    </div>
  );
}

it("clears the attribute synchronously when an enclosed host requests a settle", () => {
  render(
    <div>
      <Harness />
    </div>,
  );
  expect(entering()).toBe("y4");
  // No act() flush: a top-layer host measures its anchor right after the call.
  settleEnclosingEntrances(screen.getByTestId("spinner"));
  expect(entering()).toBeNull();
  act(() => {});
  expect(entering()).toBeNull();
});

it("clears an in-flight entrance when the surface stops being enabled and never replays on re-enable", () => {
  const { rerender } = render(<Harness />);
  expect(entering()).toBe("y4");
  rerender(<Harness enabled={false} />);
  expect(entering()).toBeNull();
  rerender(<Harness />);
  expect(entering()).toBeNull();
  act(() => {
    fireEvent.click(screen.getByRole("button", { name: "Re-enter" }));
  });
  expect(entering()).toBe("y4");
});

// SPDX-License-Identifier: Apache-2.0
import { act, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { MutationIntents, useRetainedMutationPresentation } from "./mutation";

it("isolates reconstructed metadata by original connection and fences late writers", () => {
  let write!: (action: string, bytes: number) => void;
  function Presenter() {
    const owner = useRetainedMutationPresentation<{ action: string }>(
      "revert:same-id",
    );
    write = (action, bytes) => owner.save({ action }, bytes);
    return <output>{owner.value?.action ?? "No original operation"}</output>;
  }
  const view = (connection: string, mounted = true) => (
    <MutationIntents key={connection}>
      {mounted ? <Presenter /> : null}
    </MutationIntents>
  );
  const rendered = render(view("original"));
  // Metadata is bounded separately from immutable mutation request bytes.
  act(() => write("original-action", 4 << 20));
  expect(() => write("overflow", (4 << 20) + 1)).toThrow();
  rendered.rerender(view("original", false));
  rendered.rerender(view("original"));
  expect(screen.getByText("original-action")).toBeTruthy();
  const originalWrite = write;
  rendered.rerender(view("replacement"));
  expect(screen.getByText("No original operation")).toBeTruthy();
  expect(() => originalWrite("late-original", 1)).toThrow();
  expect(screen.getByText("No original operation")).toBeTruthy();
});

function mountKeyed() {
  let owner!: ReturnType<typeof useRetainedMutationPresentation<{ n: number }>>;
  let renders = 0;
  function Presenter({ id }: { id: string }) {
    owner = useRetainedMutationPresentation<{ n: number }>(`revert:${id}`);
    renders += 1;
    return <output>{owner.value ? `held:${owner.value.n}` : "empty"}</output>;
  }
  const view = (id: string) => (
    <MutationIntents>
      <Presenter id={id} />
    </MutationIntents>
  );
  const rendered = render(view("first"));
  return {
    rendered,
    view,
    renders: () => renders,
    owner: () => owner,
  };
}

it("releases one retained presentation, frees capacity and notifies readers", () => {
  const { rendered, view, owner, renders } = mountKeyed();
  act(() => owner().save({ n: 1 }, 4 << 20));
  expect(screen.getByText("held:1")).toBeTruthy();
  // The byte bound is full, so a second key cannot be retained.
  rendered.rerender(view("second"));
  expect(() => owner().save({ n: 2 }, 1)).toThrow();
  rendered.rerender(view("first"));
  const before = renders();
  act(() => owner().release());
  expect(screen.getByText("empty")).toBeTruthy();
  expect(renders()).toBeGreaterThan(before);
  // The freed bytes admit another key; releasing a missing key is a no-op.
  rendered.rerender(view("second"));
  act(() => owner().save({ n: 2 }, 4 << 20));
  expect(screen.getByText("held:2")).toBeTruthy();
  rendered.rerender(view("missing"));
  const afterMount = renders();
  act(() => owner().release());
  expect(renders()).toBe(afterMount);
  rendered.rerender(view("second"));
  expect(screen.getByText("held:2")).toBeTruthy();
});

it("keeps the 1000-key limit for retained guards and frees a slot per release", () => {
  const { rendered, view, owner } = mountKeyed();
  for (let index = 0; index < 1000; index += 1) {
    rendered.rerender(view(`session-${index}`));
    act(() => owner().save({ n: index }, 2));
  }
  rendered.rerender(view("overflow"));
  expect(() => owner().save({ n: -1 }, 2)).toThrow();
  rendered.rerender(view("session-500"));
  act(() => owner().release());
  // Only the released slot is reusable; every other guard is untouched.
  rendered.rerender(view("overflow"));
  act(() => owner().save({ n: -1 }, 2));
  rendered.rerender(view("session-499"));
  expect(screen.getByText("held:499")).toBeTruthy();
  rendered.rerender(view("session-501"));
  expect(screen.getByText("held:501")).toBeTruthy();
  rendered.rerender(view("another"));
  expect(() => owner().save({ n: -2 }, 2)).toThrow();
});

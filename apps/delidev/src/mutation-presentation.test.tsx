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

it("keeps original background intent metadata across nested pane replacement", () => {
  let write!: () => void;
  function Presenter() {
    const metadata = useRetainedMutationPresentation<{ action: string }>(
      "native-background:session",
    );
    const control = useRetainedMutationPresentation<{ action: string }>(
      "native-background-control:session",
    );
    write = () => {
      metadata.save({ action: "original-uncertain" }, 1024);
      control.save({ action: "original-fence" }, 1024);
    };
    return (
      <output>
        {metadata.value?.action}:{control.value?.action}
      </output>
    );
  }
  const view = (pane: string) => (
    <MutationIntents>
      <MutationIntents key={pane}>
        <Presenter />
      </MutationIntents>
    </MutationIntents>
  );
  const rendered = render(view("commands"));
  act(() => write());
  rendered.rerender(view("replacement-pane"));
  expect(screen.getByText("original-uncertain:original-fence")).toBeTruthy();
  rendered.unmount();
});

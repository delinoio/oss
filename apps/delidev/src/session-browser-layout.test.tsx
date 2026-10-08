// SPDX-License-Identifier: Apache-2.0
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import { browserSplitBounds, SessionBrowserLayouts, useSessionBrowserLayout } from "./session-browser-layout";
it("uses the exact conversation width and bounded 55 percent default", () => {
  expect(browserSplitBounds(959).wide).toBe(false);
  expect(browserSplitBounds(960)).toEqual({ wide: true, minimum: 480, maximum: 592, initial: 523.6 });
  expect(browserSplitBounds(1440)).toEqual({ wide: true, minimum: 480, maximum: 1072, initial: 787.6 });
});
function View({ id, width }: { id: string; width: number }) {
  const layout = useSessionBrowserLayout(id, width);
  return <><output>{`${layout.width}:${layout.expanded}:${layout.wide}`}</output><button onClick={() => layout.resize(900)}>Resize</button><button onClick={layout.toggleExpanded}>Expand</button></>;
}
it("retains session width/expansion outside view mounts, clamps reflow and isolates connections", () => {
  const client = new QueryClient();
  const view = (id = "session-a", width = 1440, connection = client, mounted = true) => <QueryClientProvider client={connection}>{mounted ? <View id={id} width={width} /> : null}</QueryClientProvider>;
  const mounted = render(view());
  fireEvent.click(screen.getByRole("button", { name: "Resize" })); expect(screen.getByText("900:false:true")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Expand" })); expect(screen.getByText("1072:true:true")).toBeTruthy();
  mounted.rerender(view("session-a", 959)); expect(screen.getByText("480:true:false")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Expand" })); expect(screen.getByText("480:true:false")).toBeTruthy();
  mounted.rerender(view("session-a", 960)); expect(screen.getByText("592:true:true")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Expand" })); expect(screen.getByText("592:false:true")).toBeTruthy();
  mounted.rerender(view("session-a", 1440, client, false)); mounted.rerender(view()); expect(screen.getByText("900:false:true")).toBeTruthy();
  mounted.rerender(view("session-b")); expect(screen.getByText("787.6:false:true")).toBeTruthy();
  mounted.rerender(view("session-a", 1440, new QueryClient())); expect(screen.getByText("787.6:false:true")).toBeTruthy();
});
it("bounds presentation metadata and rejects nonfinite widths", () => {
  const owner = new SessionBrowserLayouts();
  owner.save("invalid", { width: Infinity, expanded: true }); expect(owner.read("invalid").expanded).toBe(false);
  for (let n = 0; n <= 1000; n++) owner.save(`${n}`, { width: 600, expanded: false });
  expect(owner.read("0").width).toBeUndefined(); expect(owner.read("1000").width).toBe(600);
});

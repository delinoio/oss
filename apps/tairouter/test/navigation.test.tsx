// SPDX-License-Identifier: Apache-2.0
import { act, render, screen } from "@testing-library/react";
import { describe, expect, it, rs } from "@rstest/core";
import { lazy, Suspense, useEffect } from "react";
import { useBrowserLocation, useConsoleNavigation } from "../src/navigation";

function Probe() {
  useConsoleNavigation();
  const href = useBrowserLocation();
  return <output>{new URL(href).pathname}</output>;
}

function click(
  href: string,
  attributes: Record<string, string> = {},
  init: MouseEventInit = {},
) {
  const anchor = document.createElement("a");
  anchor.href = href;
  for (const [name, value] of Object.entries(attributes))
    anchor.setAttribute(name, value);
  const child = document.createElement("span");
  anchor.append(child);
  document.body.append(anchor);
  let intercepted = false;
  // Observe the router's decision, then suppress jsdom's native navigation.
  const observe = (event: MouseEvent) => {
    intercepted = event.defaultPrevented;
    event.preventDefault();
  };
  document.addEventListener("click", observe);
  act(() =>
    child.dispatchEvent(
      new MouseEvent("click", {
        bubbles: true,
        cancelable: true,
        button: 0,
        ...init,
      }),
    ),
  );
  document.removeEventListener("click", observe);
  anchor.remove();
  return intercepted;
}

describe("console link navigation", () => {
  it("handles nested link content and preserves query parameters", () => {
    window.history.replaceState(null, "", "/connect");
    render(<Probe />);
    expect(click("/providers?organization=studio")).toBe(true);
    expect(window.location.pathname + window.location.search).toBe(
      "/organizations/studio/providers",
    );
    expect(screen.getByText("/organizations/studio/providers")).toBeTruthy();
  });

  it.each([
    ["/keys", {}, { ctrlKey: true }],
    ["/keys", {}, { metaKey: true }],
    ["/keys", {}, { shiftKey: true }],
    ["/keys", {}, { altKey: true }],
    ["/keys", {}, { button: 1 }],
    ["/keys", { target: "_blank" }, {}],
    ["/keys", { download: "keys" }, {}],
    ["#main", {}, {}],
    ["https://example.test/keys", {}, {}],
    ["/auth/login?return_to=%2Foverview", {}, {}],
    ["/keys?org=studio", {}, {}],
  ])("leaves native behavior for %s %j %j", (href, attributes, init) => {
    window.history.replaceState(null, "", "/connect");
    render(<Probe />);
    expect(
      click(
        href as string,
        attributes as Record<string, string>,
        init as MouseEventInit,
      ),
    ).toBe(false);
    expect(window.location.pathname).toBe("/connect");
  });

  it("keeps already visible content while a lazy route loads", async () => {
    let reveal!: (value: { default: () => React.ReactNode }) => void;
    const Destination = lazy(
      () =>
        new Promise<{ default: () => React.ReactNode }>((resolve) => {
          reveal = resolve;
        }),
    );
    const unmount = rs.fn();
    function Shell() {
      useEffect(() => unmount, []);
      return <aside>Persistent shell</aside>;
    }
    function Router() {
      useConsoleNavigation();
      const href = useBrowserLocation();
      return (
        <Suspense fallback={<p>Replacing the shell</p>}>
          <Shell />
          {new URL(href).pathname === "/keys" ? (
            <Destination />
          ) : (
            <p>Current page</p>
          )}
        </Suspense>
      );
    }
    window.history.replaceState(null, "", "/connect");
    render(<Router />);
    expect(click("/keys")).toBe(true);
    expect(screen.getByText("Persistent shell")).toBeTruthy();
    expect(screen.getByText("Current page")).toBeTruthy();
    expect(screen.queryByText("Replacing the shell")).toBeNull();
    await act(async () => reveal({ default: () => <p>Destination</p> }));
    expect(screen.getByText("Destination")).toBeTruthy();
    expect(unmount).not.toHaveBeenCalled();
  });
});

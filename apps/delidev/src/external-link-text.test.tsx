// SPDX-License-Identifier: Apache-2.0
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { LinkedPre } from "./external-link-text";

const native = vi.hoisted(() => ({
  invoke: vi.fn(),
  isTauri: vi.fn(() => true),
}));
vi.mock("@tauri-apps/api/core", () => native);

beforeEach(() => {
  native.invoke.mockReset();
  native.isTauri.mockReturnValue(true);
});
afterEach(cleanup);

it("renders the original text byte for byte and activates one complete URL", async () => {
  native.invoke.mockResolvedValue(undefined);
  const text = "Open  https://example.com/path?q=1#part. now\n  and https://localhost:3000";
  const { container } = render(<LinkedPre text={text} />);
  expect(container.querySelector("pre")!.textContent).toBe(text);
  const links = screen.getAllByRole("link");
  expect(links.map((link) => link.textContent)).toEqual([
    "https://example.com/path?q=1#part",
    "https://localhost:3000",
  ]);
  const click = fireEvent.click(links[0]!);
  // Navigation never happens in the renderer.
  expect(click).toBe(false);
  await act(async () => {});
  expect(native.invoke).toHaveBeenCalledTimes(1);
  expect(native.invoke).toHaveBeenCalledWith("open_external_url", {
    url: "https://example.com/path?q=1#part",
  });
});

it("ignores a second activation while a launch is pending and never retries", async () => {
  let settle!: () => void;
  native.invoke.mockImplementation(
    () => new Promise<void>((resolve) => (settle = resolve)),
  );
  render(<LinkedPre text="https://example.com/" />);
  const link = screen.getByRole("link");
  fireEvent.click(link);
  fireEvent.click(link);
  expect(native.invoke).toHaveBeenCalledTimes(1);
  await act(async () => settle());
  expect(native.invoke).toHaveBeenCalledTimes(1);
});

it.each([
  ["browser-unavailable", "not installed"],
  ["browser-launch-failed", "could not be opened in the selected browser"],
  ["storage-unavailable", "Browser settings could not be read"],
  ["invalid-input", "Only http and https"],
])("shows a localized error for %s with selection guidance and keeps the link usable", async (code, expected) => {
  native.invoke.mockRejectedValue(code);
  render(<LinkedPre text="https://example.com/" />);
  fireEvent.click(screen.getByRole("link"));
  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain(expected);
  expect(alert.textContent).toMatch(/Settings > Browser|http and https/);
  expect(screen.getByRole("link")).toBeTruthy();
  expect(native.invoke).toHaveBeenCalledTimes(1);
  fireEvent.click(screen.getByRole("button", { name: "Dismiss link error" }));
  expect(screen.queryByRole("alert")).toBeNull();
});

it("keeps links inert outside the native desktop host", () => {
  native.isTauri.mockReturnValue(false);
  const { container } = render(<LinkedPre text="see https://example.com/ now" />);
  expect(container.querySelectorAll("a,button")).toHaveLength(0);
  expect(container.querySelector("pre")!.textContent).toBe(
    "see https://example.com/ now",
  );
});

it("updates activatable tokens with streamed text and never activates partial tokens", () => {
  const { rerender, container } = render(<LinkedPre text="see https://" />);
  expect(screen.queryAllByRole("link")).toHaveLength(0);
  rerender(<LinkedPre text="see https://example" />);
  expect(screen.getByRole("link").textContent).toBe("https://example");
  rerender(<LinkedPre text="see https://example.com/a" />);
  expect(screen.getByRole("link").textContent).toBe("https://example.com/a");
  expect(container.querySelector("pre")!.textContent).toBe(
    "see https://example.com/a",
  );
});

// SPDX-License-Identifier: Apache-2.0
import { it, expect, vi, afterEach } from "vitest";
import { render, screen, fireEvent, cleanup } from "@testing-library/react";
import { App } from "./app";
import { ProtectedState } from "./state";
import { en, ko } from "./localization";
vi.mock("./platform", () => ({
  storage: { read: async () => null, write: async () => {} },
  observeForeground: async () => () => {},
  permission: async () => false,
  notify: async () => false,
  tls: async () => "ok",
}));
afterEach(cleanup);
it("preserves exactly three accessible tabs and profile guidance without Local or image creation", async () => {
  render(
    <App
      state={
        new ProtectedState({ read: async () => null, write: async () => {} })
      }
    />,
  );
  await screen.findByText(en.selectProfile);
  const nav = screen.getByRole("navigation");
  expect(nav.querySelectorAll("button")).toHaveLength(3);
  fireEvent.click(screen.getByRole("button", { name: en.settings }));
  await screen.findByRole("heading", { name: en.settings });
  expect(screen.getByLabelText(en.theme)).toBeTruthy();
  expect(screen.queryByText("Local")).toBeNull();
  expect(screen.queryByLabelText("Attach images")).toBeNull();
});
it("English and Korean catalogs expose identical nonempty text keys", () => {
  expect(Object.keys(ko).sort()).toEqual(Object.keys(en).sort());
  for (const value of Object.values(ko))
    expect(value.trim().length).toBeGreaterThan(0);
});

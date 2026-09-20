import { render, screen } from "@testing-library/react";
import { createClient, createRouterTransport } from "@connectrpc/connect";
import { LocalService } from "@delinoio/async-commit-hook-api-client";
import { expect, it, vi } from "vitest";
import { App } from "./App";
import * as connection from "./connection";

it("opens the workspace without pairing or reading legacy authorization", async () => {
  localStorage.setItem("ach-v1-browser-46309", "revoked-token");
  history.replaceState(null, "", "/#port=46309&pair=retired");
  const readStorage = vi.spyOn(Storage.prototype, "getItem");
  const pair = vi.fn();
  const transport = vi.spyOn(connection, "transportFor").mockImplementation(() =>
    createRouterTransport((router) => router.service(LocalService, {
      getVersion: () => ({ apiVersion: 1 }),
      listRepositories: () => ({ repositories: [] }), pair,
    })),
  );
  const { unmount } = render(<App />);
  try {
    expect(await screen.findByRole("complementary", { name: "Repositories" })).toBeTruthy();
    expect(screen.queryByText("Pair this browser")).toBeNull();
    expect(screen.queryByRole("button", { name: "Disconnect" })).toBeNull();
    expect(screen.getByRole("link", { name: /Documentation/ }).getAttribute("href")).toBe("https://ach.delino.io");
    expect(pair).not.toHaveBeenCalled();
    expect(readStorage).not.toHaveBeenCalled();
    expect(location.hash).toBe("");
  } finally {
    unmount(); transport.mockRestore(); readStorage.mockRestore(); localStorage.clear();
  }
});

it("retains version recovery guidance through the real Connect web transport", async () => {
  const fetch = vi.fn().mockResolvedValue(new Response(
    JSON.stringify({ code: "aborted", message: "incompatible-version" }),
    { status: 409, headers: { "Content-Type": "application/json" } },
  ));
  vi.stubGlobal("fetch", fetch);
  try {
    const client = createClient(LocalService, connection.transportFor());
    const error = await client.listRepositories({}).catch((error: unknown) => error);
    expect(connection.describeError(error)).toMatch(/Reload this page from the URL printed by ach ui/);
    expect(fetch.mock.calls[0][0]).toBe(`${window.location.origin}/async_commit_hook.v1.LocalService/ListRepositories`);
    expect(new Headers(fetch.mock.calls[0][1].headers).get("X-Ach-Api-Version")).toBe("1");
  } finally {
    vi.unstubAllGlobals();
  }
});

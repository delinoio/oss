import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { LocalService } from "@delinoio/async-commit-hook-api-client";
import { expect, it, vi } from "vitest";
import { App } from "./App";
import * as connection from "./connection";

it("reuses a fresh fragment code after recovering from stale browser authorization", async () => {
  localStorage.setItem("ach-v1-browser-46309", "revoked-token");
  history.replaceState(null, "", "/#port=46309&pair=fresh-code");
  const pair = vi.fn((_request: { code: string }) => ({ token: "replacement-token" }));
  const transport = vi.spyOn(connection, "transportFor").mockImplementation((value) =>
    createRouterTransport((router) => router.service(LocalService, {
      getVersion: () => ({ apiVersion: 1 }),
      listRepositories: () => {
        if (value.token !== "replacement-token") throw new ConnectError("revoked", Code.Unauthenticated);
        return { repositories: [] };
      },
      pair,
    })),
  );
  const { unmount } = render(<App />);
  try {
    fireEvent.click(await screen.findByRole("button", { name: "Pair again" }));
    expect(location.hash).toBe("");
    expect(localStorage.getItem("ach-v1-browser-46309")).toBeNull();
    const code = screen.getByLabelText("Pairing code") as HTMLInputElement;
    expect(code.value).toBe("fresh-code");
    expect(document.activeElement).toBe(code);
    fireEvent.submit(code.closest("form")!);
    await waitFor(() => expect(localStorage.getItem("ach-v1-browser-46309")).toBe("replacement-token"));
    expect(pair.mock.calls[0]?.[0]).toMatchObject({ code: "fresh-code" });
    expect(await screen.findByRole("button", { name: "Disconnect" })).toBeTruthy();
  } finally {
    unmount();
    transport.mockRestore();
    localStorage.clear();
    history.replaceState(null, "", "/");
  }
});

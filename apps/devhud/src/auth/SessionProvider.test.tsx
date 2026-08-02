import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import {
  AuthFeature,
  SessionProvider,
  useSession,
} from "./SessionProvider";
import type {
  NativeSessionBridge,
  NativeSessionSnapshot,
} from "./contracts";
import { publishPersistenceReset } from "../runtime/theme";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function bridge(
  overrides: Partial<NativeSessionBridge> = {},
): NativeSessionBridge {
  return {
    restore: vi.fn(async () => ({ status: "signed-out" as const })),
    start: vi.fn(async () => ({ status: "authenticating" as const })),
    logout: vi.fn(async () => ({ status: "signed-out" as const })),
    ...overrides,
  };
}

function SessionHarness() {
  const auth = useSession();
  return (
    <section>
      <p data-testid="session">{auth.session.status}</p>
      <p data-testid="ready">{String(auth.ready)}</p>
      {auth.failure === null ? null : <p role="alert">{auth.failure.guidance}</p>}
      <button onClick={() => void auth.signIn(AuthFeature.Deck)} type="button">
        Sign in
      </button>
      <button onClick={() => void auth.logout()} type="button">
        Log out
      </button>
    </section>
  );
}

describe("dependency-injected DevHud session provider", () => {
  it("keeps the signed-out shell ready when native auth is unconfigured", async () => {
    const native = bridge({
      restore: vi.fn(async () => {
        throw "configuration-unavailable";
      }),
    });
    render(
      <SessionProvider bridge={native}>
        <SessionHarness />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("ready")).toHaveTextContent("true"));
    expect(screen.getByTestId("session")).toHaveTextContent("signed-out");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Local DevHud tools remain available",
    );
  });

  it("does not expose a token getter or generic network/browser operation", () => {
    const native = bridge();
    expect(Object.keys(native).toSorted()).toEqual([
      "logout",
      "restore",
      "start",
    ]);
    expect(JSON.stringify(native)).not.toMatch(
      /token|fetch|request|url|open|storage|query/iu,
    );
  });

  it("rejects account switching without disclosing the restored subject", async () => {
    const native = bridge({
      restore: vi.fn(async () => ({
        status: "signed-in" as const,
        subject: "account-a",
        features: [AuthFeature.Deck],
        offlineFeatures: [],
      })),
      start: vi.fn(async () => {
        throw "account-switch-requires-logout";
      }),
    });
    const user = userEvent.setup();
    render(
      <SessionProvider bridge={native}>
        <SessionHarness />
      </SessionProvider>,
    );
    expect(await screen.findByTestId("session")).toHaveTextContent("signed-in");
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Log out of the active DeliDev account",
    );
    expect(screen.getByRole("alert")).not.toHaveTextContent("account-a");
  });

  it("observes a native-only mobile callback while authentication is active", async () => {
    const restore = vi
      .fn<NativeSessionBridge["restore"]>()
      .mockResolvedValueOnce({ status: "signed-out" })
      .mockResolvedValueOnce({ status: "authenticating" })
      .mockResolvedValueOnce({ status: "signed-in", subject: "account-a", features: [AuthFeature.Deck], offlineFeatures: [] });
    const native = bridge({ restore });
    const user = userEvent.setup();
    render(
      <SessionProvider bridge={native}>
        <SessionHarness />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("ready")).toHaveTextContent("true"));
    await user.click(screen.getByRole("button", { name: "Sign in" }));
    expect(screen.getByTestId("session")).toHaveTextContent("authenticating");
    await waitFor(() =>
      expect(screen.getByTestId("session")).toHaveTextContent("signed-in"),
    );
    expect(restore).toHaveBeenCalledTimes(3);
  });

  it("restores the active account after incremental authorization is rejected", async () => {
    const restore = vi
      .fn<NativeSessionBridge["restore"]>()
      .mockResolvedValueOnce({ status: "signed-in", subject: "account-a", features: [AuthFeature.Deck], offlineFeatures: [] })
      .mockRejectedValueOnce("authorization-rejected")
      .mockResolvedValueOnce({ status: "signed-in", subject: "account-a", features: [AuthFeature.Deck], offlineFeatures: [] });
    const native = bridge({ restore });
    const user = userEvent.setup();
    render(
      <SessionProvider bridge={native}>
        <SessionHarness />
      </SessionProvider>,
    );
    expect(await screen.findByTestId("session")).toHaveTextContent("signed-in");

    await user.click(screen.getByRole("button", { name: "Sign in" }));

    await waitFor(() =>
      expect(screen.getByRole("alert")).toHaveTextContent(
        "Sign-in was cancelled or rejected",
      ),
    );
    expect(screen.getByTestId("session")).toHaveTextContent("signed-in");
    expect(restore).toHaveBeenCalledTimes(3);
  });

  it("clears frontend account state before a failing vault logout finishes", async () => {
    let rejectLogout: ((reason: unknown) => void) | undefined;
    const native = bridge({
      restore: vi.fn(async () => ({
        status: "signed-in" as const,
        subject: "account-a",
        features: [AuthFeature.Deck],
        offlineFeatures: [],
      })),
      logout: vi.fn(
        () =>
          new Promise<NativeSessionSnapshot>((_, reject) => {
            rejectLogout = reject;
          }),
      ),
    });
    const user = userEvent.setup();
    render(
      <SessionProvider bridge={native}>
        <SessionHarness />
      </SessionProvider>,
    );
    expect(await screen.findByTestId("session")).toHaveTextContent("signed-in");
    await user.click(screen.getByRole("button", { name: "Log out" }));
    expect(screen.getByTestId("session")).toHaveTextContent("signed-out");
    rejectLogout?.("secure-vault-delete-failed");
    expect(await screen.findByTestId("session")).toHaveTextContent(
      "cleanup-required",
    );
    expect(screen.getByRole("alert")).not.toHaveTextContent("account-a");
  });

  it("clears frontend account state after a partially retained reset", async () => {
    const resetListeners = new Set<(event: MessageEvent<unknown>) => void>();
    class TestBroadcastChannel {
      addEventListener(
        _type: "message",
        listener: (event: MessageEvent<unknown>) => void,
      ) {
        resetListeners.add(listener);
      }

      postMessage(data: unknown) {
        for (const listener of resetListeners) {
          listener({ data } as MessageEvent<unknown>);
        }
      }

      close() {}
    }
    vi.stubGlobal("BroadcastChannel", TestBroadcastChannel);
    const native = bridge({
      restore: vi.fn(async () => ({
        status: "signed-in" as const,
        subject: "account-a",
        features: [AuthFeature.Deck],
        offlineFeatures: [],
      })),
    });
    render(
      <SessionProvider bridge={native}>
        <SessionHarness />
      </SessionProvider>,
    );
    expect(await screen.findByTestId("session")).toHaveTextContent("signed-in");

    act(() => publishPersistenceReset({ status: "partially-retained" }));

    expect(screen.getByTestId("session")).toHaveTextContent("signed-out");
  });

  it("never reads or writes browser persistence", async () => {
    const localGet = vi.spyOn(Storage.prototype, "getItem");
    const localSet = vi.spyOn(Storage.prototype, "setItem");
    render(
      <SessionProvider bridge={bridge()}>
        <SessionHarness />
      </SessionProvider>,
    );
    await waitFor(() => expect(screen.getByTestId("ready")).toHaveTextContent("true"));
    expect(localGet).not.toHaveBeenCalled();
    expect(localSet).not.toHaveBeenCalled();
  });
});

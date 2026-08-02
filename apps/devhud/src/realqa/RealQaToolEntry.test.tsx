import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";

import { SessionProvider } from "../auth/SessionProvider";
import type { NativeSessionBridge } from "../auth/contracts";
import { RealQaToolEntry } from "./RealQaToolEntry";

afterEach(cleanup);

function bridge(
  restored: Awaited<ReturnType<NativeSessionBridge["restore"]>>,
): NativeSessionBridge {
  return {
    restore: vi.fn(async () => restored),
    start: vi.fn(async () => ({ status: "signed-in" as const, subject: "account-1" })),
    logout: vi.fn(async () => ({ status: "signed-out" as const })),
  };
}

describe("RealQA authenticated entry", () => {
  it("retains the signed-out base card and requires authentication before entry", async () => {
    const user = userEvent.setup();
    const session = bridge({ status: "signed-out" });
    const open = vi.fn(async () => undefined);
    render(
      <SessionProvider bridge={session}>
        <RealQaToolEntry open={open} />
      </SessionProvider>,
    );
    await user.click(await screen.findByRole("button", { name: "Sign in to RealQA" }));
    expect(session.start).toHaveBeenCalledWith("real-qa");
    expect(open).not.toHaveBeenCalled();
  });

  it("permits prior-bound offline entry while explaining remote restrictions", async () => {
    const user = userEvent.setup();
    const open = vi.fn(async () => undefined);
    const session = bridge({ status: "prior-session-offline" });
    render(
      <SessionProvider bridge={session}>
        <RealQaToolEntry open={open} />
      </SessionProvider>,
    );
    expect(await screen.findByText(/limited to capture, editing, and encrypted drafts/u)).toBeVisible();
    await user.click(screen.getByRole("button", { name: "Open RealQA" }));
    expect(open).toHaveBeenCalledOnce();
    await user.click(screen.getByRole("button", { name: "Log out" }));
    expect(session.logout).toHaveBeenCalledOnce();
  });
});

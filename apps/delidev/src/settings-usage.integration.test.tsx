// SPDX-License-Identifier: Apache-2.0
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { Usage } from "./usage";
import { useSettingsFixture } from "./settings-test-fixture";
import { observeUsageReads, usageReadSucceeded, UsageReadState } from "./test-usage-read";

const fixture = useSettingsFixture();

it("reads unavailable usage through the actual Go service without inventing cost", async () => {
  let releaseFiltered!: () => void;
  const filteredDelivery = new Promise<void>(resolve => { releaseFiltered = resolve; });
  const observed = observeUsageReads(fixture.transport, read => read.generalChat ? filteredDelivery : Promise.resolve());
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<TransportProvider transport={observed.transport}><QueryClientProvider client={client}><Usage active open={() => {}} /></QueryClientProvider></TransportProvider>);
  const panel = document.querySelector(".usage-page")!;
  const assertSettled = (generalChat: boolean) => {
    const read = observed.reads.at(-1);
    expect(read?.generalChat).toBe(generalChat);
    expect(usageReadSucceeded(read)).toBe(true);
    expect(panel.getAttribute("aria-busy")).toBe("false");
    expect(panel.querySelector(".usage-loading")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
  };
  try {
    await waitFor(() => assertSettled(false));
    expect(screen.getByText("Incomplete coverage")).toBeTruthy();
    expect(screen.getByText(/No exact response usage is recorded/)).toBeTruthy();
    expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
    const initialReads = observed.reads.length;
    fireEvent.click(screen.getByRole("checkbox", { name: "General Chat only" }));
    expect(screen.queryByRole("button", { name: "Apply filters" })).toBeNull();
    await waitFor(() => {
      expect(observed.reads).toHaveLength(initialReads + 1);
      expect(observed.reads.at(-1)).toMatchObject({ generalChat: true, responseReceived: true, state: UsageReadState.Pending });
    });
    expect(usageReadSucceeded(observed.reads.at(-1))).toBe(false);
    expect(panel.getAttribute("aria-busy")).toBe("true");
    expect(panel.querySelector(".usage-loading")).not.toBeNull();
    await act(async () => { releaseFiltered(); });
    await waitFor(() => assertSettled(true));
    expect(screen.getByRole("group", { name: "Applied conditions" }).textContent).toContain("General Chat");
    expect(screen.getByText("Incomplete coverage")).toBeTruthy();
    expect(screen.getByText(/No exact response usage is recorded/)).toBeTruthy();
    expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
  } finally {
    releaseFiltered();
    cleanup();
    client.clear();
  }
});

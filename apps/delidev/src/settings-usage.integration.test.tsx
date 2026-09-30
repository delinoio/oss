// SPDX-License-Identifier: Apache-2.0
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { Usage } from "./usage";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("reads unavailable usage through the actual Go service without inventing cost", async () => {
  const { transport } = fixture;
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active open={()=>{}} /></QueryClientProvider></TransportProvider>);
 await screen.findByText("Incomplete coverage");
 expect(screen.getByText(/No exact response usage is recorded/)).toBeTruthy();
 expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
 fireEvent.click(screen.getByRole("checkbox",{name:"General Chat only"}));
 fireEvent.click(screen.getByRole("button",{name:"Apply filters"}));
 await waitFor(()=>expect(screen.queryByText("Loading usage…")).toBeNull());
 cleanup();client.clear();
});



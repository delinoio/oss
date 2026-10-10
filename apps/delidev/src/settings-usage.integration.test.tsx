// SPDX-License-Identifier: Apache-2.0
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { Usage } from "./usage";
import { filteredUsageRead } from "./test-filtered-usage-read";
import { useSettingsFixture } from "./settings-test-fixture";

const fixture = useSettingsFixture();

it("reads unavailable usage through the actual Go service without inventing cost", async () => {
 const read = filteredUsageRead(fixture.transport);
 const transport = read.transport;
 const client=new QueryClient({defaultOptions:{queries:{retry:false}}});
 try {
 render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Usage active open={()=>{}} /></QueryClientProvider></TransportProvider>);
 await screen.findByText("Incomplete coverage");
 expect(screen.getByText(/No exact response usage is recorded/)).toBeTruthy();
 expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
 fireEvent.click(screen.getByRole("checkbox",{name:"General Chat only"}));
 expect(screen.queryByRole("button",{name:"Apply filters"})).toBeNull();
 await waitFor(()=>expect(screen.getByRole("group",{name:"Applied conditions"}).textContent).toContain("General Chat"));
 const request = await read.started;
 expect(request.generalChat).toBe(true); expect(request.projectId).toBe("");
 await waitFor(() => expect(screen.getByRole("tablist", { name: "Usage analysis" }).closest("section")?.getAttribute("aria-busy")).toBe("true"));
 let completed = false; void read.completed.then(() => { completed = true; }, () => {});
 expect(completed).toBe(false);
 // Holding the actual server response proves that old coverage text and the
 // newly applied label cannot satisfy completion before the current read.
 read.release();
 const current = await read.completed;
 expect(current.request.generalChat).toBe(true);
 expect(current.response.fromUnixMs < current.response.untilUnixMs).toBe(true);
 await waitFor(() => {
   expect(screen.getByRole("tablist", { name: "Usage analysis" }).closest("section")?.getAttribute("aria-busy")).toBe("false");
   expect(screen.queryByText("Loading token usage…")).toBeNull();
   expect(screen.queryByText("Refreshing this applied range…")).toBeNull();
   expect(screen.queryByRole("alert")).toBeNull();
   expect(screen.getByRole("group", { name: "Applied conditions" }).textContent).toContain("General Chat");
   expect(screen.getByText("Incomplete coverage")).toBeTruthy();
   expect(screen.getByText(/No exact response usage is recorded/)).toBeTruthy();
   expect(screen.getByText("Actual API cost:").parentElement!.textContent).toContain("Unavailable");
 });
 } finally { read.release(); cleanup(); client.clear(); }
});



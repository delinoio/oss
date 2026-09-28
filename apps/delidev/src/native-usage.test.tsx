import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { EntityKind, ResourceSchema, ResourceService } from "@delinoio/delidev-api-client";
import { render, screen, waitFor } from "@testing-library/react";
import { expect, it } from "vitest";
import { encode } from "./documents";
import { NativeUsage, NativeUsageObservation } from "./native-usage";

function observation() {
  return { source: "step-finish", native_id: "original-part", native_parent_id: "original-message", counts: { uncached_input: "12", nonreasoning_output: "7", reasoning_output: "3", cache_read_input: "8", cache_write_input: "2", total: null as string | null }, native_estimate: "0.000001e+0" };
}

it("retains distinct native categories, missing total and exact estimate without billing claims", () => {
  render(<NativeUsageObservation value={observation()} />);
  expect(screen.getByText("Uncached input").nextElementSibling?.textContent).toBe("12");
  expect(screen.getByText("Output excluding reasoning").nextElementSibling?.textContent).toBe("7");
  expect(screen.getByText("Reported total").nextElementSibling?.textContent).toBe("Unavailable");
  expect(screen.getByText("Native estimate · currency unspecified").nextElementSibling?.textContent).toBe("0.000001e+0");
  expect(screen.getByText(/Step and message observations overlap/)).toBeTruthy();
});

it("shows an explicitly reported zero independently from an unavailable total", () => {
  const value = observation(); value.counts.total = "0";
  render(<NativeUsageObservation value={value} />);
  expect(screen.getByText("Reported total").nextElementSibling?.textContent).toBe("0");
});

it.each(["01", "-1", "1.5", "1e3", "9007199254740992"])("refuses inexact native count %s", (count) => {
  const value = observation(); value.counts.uncached_input = count;
  render(<NativeUsageObservation value={value} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
  expect(screen.queryByText("Uncached input")).toBeNull();
});

it.each(["-0.1", "-1e-999999999", "1e309", "NaN", "USD 0"])("refuses invalid native estimate %s", (estimate) => {
  const value = observation(); value.native_estimate = estimate;
  render(<NativeUsageObservation value={value} />);
  expect(screen.getByText(/unavailable or inconsistent/)).toBeTruthy();
});

it("reads only the exact original session observation and identifies a preceding execution", async () => {
  const session = create(ResourceSchema, { schemaVersion: 1, id: "session", kind: EntityKind.SESSION, revision: 1n, documentJson: encode({ initial_execution: { configuration: { harness: "opencode" } }, current_execution: { id: "new-execution" }, execution: { execution_id: "original-execution", latest_usage_id: "original-usage" } }) });
  const original = create(ResourceSchema, { schemaVersion: 1, id: "original-usage", kind: EntityKind.USAGE, sessionId: session.id, revision: 1n, documentJson: encode({ execution_id: "original-execution", harness: "opencode", native_version: "1.18.32", opencode_observation: observation() }) });
  const requests: string[] = [];
  const transport = createRouterTransport((router) => router.service(ResourceService, { getResource: (request) => { requests.push(request.id); expect(request.kind).toBe(EntityKind.USAGE); return { resource: original }; } }));
  const query = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={query}><TransportProvider transport={transport}><NativeUsage session={session} /></TransportProvider></QueryClientProvider>);
  await waitFor(() => expect(screen.getByText("This observation belongs to a preceding execution.")).toBeTruthy());
  expect(requests).toEqual(["original-usage"]);
  expect(screen.getByText("Reported total")).toBeTruthy();
});

it("does not misattribute a returned observation from another session", async () => {
  const session = create(ResourceSchema, { schemaVersion: 1, id: "session", kind: EntityKind.SESSION, revision: 1n, documentJson: encode({ initial_execution: { configuration: { harness: "opencode" } }, execution: { execution_id: "original", latest_usage_id: "usage" } }) });
  const foreign = create(ResourceSchema, { schemaVersion: 1, id: "usage", kind: EntityKind.USAGE, sessionId: "another-session", documentJson: encode({ execution_id: "original", harness: "opencode", native_version: "1.18.32", opencode_observation: observation() }) });
  const transport = createRouterTransport((router) => router.service(ResourceService, { getResource: () => ({ resource: foreign }) }));
  const query = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  render(<QueryClientProvider client={query}><TransportProvider transport={transport}><NativeUsage session={session} /></TransportProvider></QueryClientProvider>);
  await waitFor(() => expect(screen.getByText("The matching native usage observation is unavailable.")).toBeTruthy());
  expect(screen.queryByText("Uncached input")).toBeNull();
});


it.each(["original", "foreign", "mixed"])("retains the selected Claude usage scope: %s", async (kind) => {
 const session=create(ResourceSchema,{schemaVersion:1,id:"session",kind:EntityKind.SESSION,revision:1n,documentJson:encode({initial_execution:{configuration:{harness:"claude-code"}},execution:{execution_id:"original",latest_usage_id:"usage"}})});
 const retained=create(ResourceSchema,{schemaVersion:1,id:"usage",kind:EntityKind.USAGE,sessionId:kind==="foreign"?"other":"session",documentJson:encode({execution_id:"original",harness:"claude-code",native_version:"2.1.236",claude_observation:{source:"input-result",native_event_id:"01960dcb-e1fa-7000-8000-000000000001",result:{main_loop_turn:{input_tokens:"9007199254740993"},native_cumulative_cost_usd:"0.0006994999999999999"}},...(kind==="mixed"?{response:{}}:{})})});
 const requests:string[]=[];
 const transport=createRouterTransport((router)=>router.service(ResourceService,{getResource:(request)=>{requests.push(request.id);return {resource:retained};}}));
 const query=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<QueryClientProvider client={query}><TransportProvider transport={transport}><NativeUsage session={session}/></TransportProvider></QueryClientProvider>);
 if(kind==="original") expect(await screen.findByText("9007199254740993")).toBeTruthy();
 else { expect(await screen.findByText("The matching native usage observation is unavailable.")).toBeTruthy();expect(screen.queryByLabelText("Claude native usage")).toBeNull(); }
 expect(requests).toEqual(["usage"]);
});

it.each(["original", "foreign-input", "foreign-session", "mixed", "old-version"])("attributes original Grok usage: %s", async (kind) => {
 const session=create(ResourceSchema,{schemaVersion:1,id:"session",kind:EntityKind.SESSION,revision:1n,documentJson:encode({initial_execution:{configuration:{harness:"grok-build"}},execution:{execution_id:"original",native_thread_id:"thread",native_turn_id:"turn",latest_usage_id:"usage"}})});
 const retained=create(ResourceSchema,{schemaVersion:1,id:"usage",kind:EntityKind.USAGE,sessionId:kind==="foreign-session"?"other":"session",documentJson:encode({execution_id:"original",harness:"grok-build",native_version:kind==="old-version"?"1.0.40":"1.0.41",native_thread_id:"thread",native_turn_id:kind==="foreign-input"?"other":"turn",grok_observation:{ordinal:1,counts:{input_tokens:"18446744073709551615",output_tokens:"5",cache_read_input_tokens:"0",cache_creation_input_tokens:"0",reasoning_tokens:"0"}},...(kind==="mixed"?{opencode_observation:{}}:{})})});
 const transport=createRouterTransport((router)=>router.service(ResourceService,{getResource:()=>({resource:retained})}));
 const query=new QueryClient({defaultOptions:{queries:{retry:false}}});
 render(<QueryClientProvider client={query}><TransportProvider transport={transport}><NativeUsage session={session}/></TransportProvider></QueryClientProvider>);
 if(kind==="original") expect(await screen.findByText("18446744073709551615")).toBeTruthy();
 else { expect(await screen.findByText("The matching native usage observation is unavailable.")).toBeTruthy();expect(screen.queryByLabelText("Grok response usage")).toBeNull(); }
});

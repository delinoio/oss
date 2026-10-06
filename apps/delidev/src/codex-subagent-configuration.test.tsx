// SPDX-License-Identifier: Apache-2.0
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { useState } from "react";
import { expect, it, vi } from "vitest";
import { SystemCapability, SystemService } from "@delinoio/delidev-api-client";
import { CodexSubagentConfiguration } from "./codex-subagent-configuration";
import type { Document } from "./documents";

function mount(supported: boolean, options: Document) {
  const change = vi.fn();
  const transport = createRouterTransport(router => router.service(SystemService, { getStatus: () => ({ capabilities: supported ? [SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1] : [] }) }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  function Editor() {
    const [current, set] = useState(options);
    return <CodexSubagentConfiguration active options={current} change={(key, value) => { change(key, value); set(old => ({ ...old, [key]: value })); }} />;
  }
  render(<TransportProvider transport={transport}><QueryClientProvider client={client}><Editor /></QueryClientProvider></TransportProvider>);
  return change;
}

it("retains original settings when the server has no child configuration capability", async () => {
  const change = mount(false, { subagent_model: "original-child", subagent_effort: "medium", max_concurrency: 4 });
  await screen.findByText(/Update the connected server/);
  expect((screen.getByLabelText("Subagent model") as HTMLInputElement).value).toBe("original-child");
  expect((screen.getByRole("combobox", { name: "Subagent effort" }) as HTMLInputElement).disabled).toBe(true);
  expect(change).not.toHaveBeenCalled();
});

it("keeps native defaults omitted and sends exact explicit child options", async () => {
  const change = mount(true, {});
  await waitFor(() => expect((screen.getByLabelText("Subagent model") as HTMLInputElement).disabled).toBe(false));
  expect(change).not.toHaveBeenCalled();
  fireEvent.change(screen.getByLabelText("Subagent model"), { target: { value: "registered-child" } });
  fireEvent.change(screen.getByLabelText("Subagent effort"), { target: { value: "medium" } });
  fireEvent.change(screen.getByLabelText("Maximum concurrency (0 uses native default)"), { target: { value: "64" } });
  expect(change.mock.calls).toEqual([["subagent_model", "registered-child"], ["subagent_effort", "medium"], ["max_concurrency", 64]]);
  fireEvent.change(screen.getByLabelText("Subagent effort"), { target: { value: "" } });
  fireEvent.change(screen.getByLabelText("Maximum concurrency (0 uses native default)"), { target: { value: "0" } });
  expect(change.mock.calls.slice(-2)).toEqual([["subagent_effort", ""], ["max_concurrency", 0]]);
});

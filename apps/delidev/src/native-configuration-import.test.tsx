// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  ConfigurationService,
  EntityKind,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
  NativeConfigurationImportState,
  newRequestId,
} from "@delinoio/delidev-api-client";
import {
  nativeImportObservation,
  nativeImportSelection,
  NativeConfigurationImport,
} from "./native-configuration-import";
import { encode } from "./documents";
import { codexScope } from "./harness-settings-model";
import { i18n } from "./localization";
const fixture = vi.hoisted(() => ({ machine: undefined as unknown }));
vi.mock("./configuration-fields", () => ({
  ResourceChoice: ({
    label,
    change,
  }: {
    label: string;
    change: (id: string, data: unknown, row: unknown) => void;
  }) => (
    <button onClick={() => change("fixture", {}, fixture.machine)}>
      {label}
    </button>
  ),
}));
afterEach(async () => {
  await i18n.changeLanguage("en");
});
const setting = {
  id: "a".repeat(64),
  kind: "setting",
  name: "effort",
  file: "config.toml",
  scope_index: 0,
  source_digest: "b".repeat(64),
  value: "high",
};
const extension = {
  id: "c".repeat(64),
  kind: "inert-extension",
  name: "mcp_servers",
  file: "config.toml",
  scope_index: 0,
  source_digest: "b".repeat(64),
  contents: '{"fixture":{"command":"tool"}}',
  files: [
    {
      path: "config.toml",
      bytes: 27,
      digest: "d".repeat(64),
      executable: false,
      contents: '[mcp_servers.x]\ncommand="t"',
    },
  ],
};
const observed = {
  version: 1,
  roots: ["e".repeat(64)],
  sources: [],
  entries: [setting, extension],
};
function mount({ uncertain = false }: { uncertain?: boolean } = {}) {
  const server = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.SETTINGS,
    revision: 1n,
    schemaVersion: 4,
    documentJson: encode({
      name: "Demo server",
      harness_defaults: { version: 1, entries: [] },
      automatic_plan_approval: false,
      plan_mode_default: false,
      branch_prefix: "kdy1/",
    }),
  });
  const machine = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.MACHINE,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({ name: "Demo Runner" }),
  });
  fixture.machine = machine;
  const preview = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.JOB,
    revision: 1n,
    schemaVersion: 1,
    documentJson: encode({
      type: "native-configuration-preview",
      state: "succeeded",
      input: {},
      native_configuration_digest: "f".repeat(64),
      output: observed,
    }),
  });
  let applies = 0,
    checks = 0,
    lastRequest = "";
  let original: Uint8Array | undefined;
  const transport = createRouterTransport(({ service }) => {
    service(SystemService, {
      getStatus: () => ({
        serverId: server.id,
        capabilities: [SystemCapability.NATIVE_CONFIGURATION_IMPORT_V1],
      }),
    });
    service(ResourceService, {
      listResources: () => ({ resources: [server] }),
    });
    service(ConfigurationService, {
      previewNativeConfigurationImport: (req) => {
        lastRequest = req.requestId;
        const selection = JSON.parse(
          new TextDecoder().decode(req.selectionJson),
        );
        expect(selection.scopes).toEqual([
          { kind: "home", path: "/explicit/home" },
        ]);
        return { requestId: req.requestId, job: preview };
      },
      getNativeConfigurationImport: (req) => {
        checks++;
        expect(req.requestId).toBe(lastRequest);
        return {
          requestId: req.requestId,
          job: preview,
          state: NativeConfigurationImportState.SUCCEEDED,
        };
      },
      prepareNativeConfigurationImport: (req) => ({
        planJson: encode({
          version: 1,
          preview_job_id: preview.id,
          selection: JSON.parse(new TextDecoder().decode(req.selectionJson)),
          token: "signed",
        }),
      }),
      applyNativeConfigurationImport: (req) => {
        applies++;
        lastRequest = req.requestId;
        original = req.planJson.slice();
        if (uncertain) throw new Error("transport lost");
        return {
          requestId: req.requestId,
          job: create(ResourceSchema, {
            ...preview,
            documentJson: encode({
              type: "native-configuration-apply",
              state: "succeeded",
              input: {},
              output: [{ kind: "settings" }],
            }),
          }),
        };
      },
    });
  });
  const query = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const ui = (active: boolean) => (
    <QueryClientProvider client={query}>
      <TransportProvider transport={transport}>
        <NativeConfigurationImport active={active} initialScope={codexScope} />
      </TransportProvider>
    </QueryClientProvider>
  );
  const view = render(ui(true));
  return {
    ...view,
    active: (active: boolean) => view.rerender(ui(active)),
    count: () => ({ applies, checks, original }),
  };
}
async function preview() {
  fireEvent.click(
    screen.getByRole("button", { name: "Import configuration…" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Runner Device" }));
  fireEvent.click(screen.getByRole("checkbox", { name: "User settings" }));
  fireEvent.change(
    screen.getByLabelText("User configuration directory on this Runner"),
    { target: { value: "/explicit/home" } },
  );
  fireEvent.change(screen.getByLabelText("Source"), {
    target: { value: "subscription" },
  });
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Preview configuration",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Preview configuration" }),
  );
  await screen.findByRole("checkbox", { name: "effort" });
}
it("rejects unsupported raw contents and enforces one selected proposal per setting", () => {
  expect(
    nativeImportObservation({
      ...observed,
      entries: [{ ...setting, kind: "excluded", contents: "private" }],
    }),
  ).toBeUndefined();
  const entries = [setting, { ...setting, id: "1".repeat(64) }] as Parameters<
    typeof nativeImportSelection
  >[0];
  expect(
    nativeImportSelection(entries, [setting.id], entries[1].id, true),
  ).toEqual([entries[1].id]);
  expect(nativeImportSelection(entries, [], "unknown", true)).toEqual([]);
});
it("starts every candidate unchecked and confirms only the deliberate selection", async () => {
  const view = mount();
  await preview();
  expect(
    (screen.getByRole("checkbox", { name: "effort" }) as HTMLInputElement)
      .checked,
  ).toBe(false);
  expect(
    (screen.getByRole("checkbox", { name: "mcp_servers" }) as HTMLInputElement)
      .checked,
  ).toBe(false);
  expect(
    (
      screen.getByRole("button", {
        name: "Review selected changes",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("checkbox", { name: "effort" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Review selected changes" }),
  );
  await screen.findByRole("button", { name: "Import selected" });
  expect(view.count().applies).toBe(0);
  fireEvent.click(screen.getByRole("button", { name: "Back" }));
  await screen.findByRole("checkbox", { name: "effort" });
  view.active(false);
  expect(screen.queryByText(extension.contents)).toBeNull();
  view.active(true);
  expect(screen.queryByRole("button", { name: "Import selected" })).toBeNull();
});
it("retains an uncertain original apply and offers only status inspection", async () => {
  const view = mount({ uncertain: true });
  await preview();
  fireEvent.click(screen.getByRole("checkbox", { name: "effort" }));
  fireEvent.click(
    screen.getByRole("button", { name: "Review selected changes" }),
  );
  await screen.findByRole("button", { name: "Import selected" });
  fireEvent.click(screen.getByRole("button", { name: "Import selected" }));
  await screen.findByRole("button", { name: "Check status" });
  expect(view.count().applies).toBe(1);
  expect(screen.queryByRole("button", { name: "Import selected" })).toBeNull();
  const plan = JSON.parse(new TextDecoder().decode(view.count().original));
  expect(plan.selection.entries).toEqual([setting.id]);
  view.active(false);
  view.active(true);
  expect(screen.queryByRole("button", { name: "Import selected" })).toBeNull();
});

// SPDX-License-Identifier: Apache-2.0
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { useState } from "react";
import { afterEach, expect, it, vi } from "vitest";
import {
  EntityKind,
  newRequestId,
  ResourceSchema,
  ResourceService,
  SystemCapability,
  SystemService,
} from "@delinoio/delidev-api-client";
import { HarnessSettingsEditor } from "./harness-settings-editor";
import {
  codexScope,
  claudeScope,
  HarnessSettingsOwner,
  HarnessScopeSource,
  type HarnessRoutePreview,
} from "./harness-settings-model";
import { encode, type Document } from "./documents";
import { i18n } from "./localization";
vi.mock("./configuration-fields", () => ({ ResourceChoice: () => null }));
afterEach(async () => {
  await i18n.changeLanguage("en");
});
function mount(
  data: Document,
  owner = HarnessSettingsOwner.Server,
  capabilities = [
    SystemCapability.HARNESS_DEFAULTS_V1,
    SystemCapability.CODEX_SUBAGENT_CONFIGURATION_V1,
  ],
  serverDefaults?: Document,
  harness: "codex" | "claude-code" = "codex",
  routes?: HarnessRoutePreview[],
) {
  const change = vi.fn();
  const resource = create(ResourceSchema, {
    kind: EntityKind.SETTINGS,
    id: newRequestId(),
    revision: 12n,
    schemaVersion: 4,
    documentJson: encode({
      harness_defaults: serverDefaults ?? { version: 1, entries: [] },
    }),
  });
  const transport = createRouterTransport((router) => {
    router.service(SystemService, { getStatus: () => ({ capabilities }) });
    router.service(ResourceService, {
      listResources: () => ({ resources: [resource] }),
    });
  });
  function Editor() {
    const [current, set] = useState(data);
    return (
      <HarnessSettingsEditor
        active
        data={current}
        owner={owner}
        harness={harness}
        routes={routes}
        change={(next) => {
          change(next);
          set(next);
        }}
      />
    );
  }
  render(
    <TransportProvider transport={transport}>
      <QueryClientProvider
        client={
          new QueryClient({ defaultOptions: { queries: { retry: false } } })
        }
      >
        <Editor />
      </QueryClientProvider>
    </TransportProvider>,
  );
  return change;
}
it("keeps override/reset choices and draft focus across locale changes without enabling unsupported features", async () => {
  const change = mount({ harness_defaults: { version: 1, entries: [] } });
  const effort = screen.getByRole("combobox", {
    name: "Reasoning effort: Selection",
  });
  await waitFor(() => expect(effort.closest("fieldset")?.disabled).toBe(false));
  expect(change).not.toHaveBeenCalled();
  fireEvent.change(effort, { target: { value: "override" } });
  const input = screen.getByRole("textbox", {
    name: "Reasoning effort: Override value",
  });
  fireEvent.change(input, { target: { value: "high" } });
  input.focus();
  const calls = change.mock.calls.length;
  await act(() => i18n.changeLanguage("ko"));
  expect(document.activeElement).toBe(input);
  expect((input as HTMLInputElement).value).toBe("high");
  expect(change).toHaveBeenCalledTimes(calls);
  expect(
    (
      screen.getByRole("combobox", {
        name: "승인 검토자: 선택",
      }) as HTMLSelectElement
    ).disabled,
  ).toBe(true);
  expect(
    (
      screen.getByRole("textbox", {
        name: "승인 검토 모델: 재정의 값",
      }) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  const section = input.closest("section")!;
  fireEvent.click(
    within(section).getByRole("button", { name: "상속으로 초기화" }),
  );
  expect(
    change.mock.calls.at(-1)?.[0].harness_defaults.entries[0].settings.effort,
  ).toEqual({ mode: "inherit" });
});
it("previews a server value for inherited Project settings and keeps exact Project overrides", async () => {
  const defaults = {
    version: 1,
    entries: [
      {
        scope: codexScope,
        settings: { version: 1, effort: { mode: "override", value: "low" } },
      },
    ],
  };
  const change = mount(
    { harness_defaults: { version: 1, entries: [] } },
    HarnessSettingsOwner.Project,
    [SystemCapability.HARNESS_DEFAULTS_V1],
    defaults,
  );
  await screen.findByText(
    "Configured value: low · Source: Server defaults · Revision: 12",
  );
  fireEvent.change(
    screen.getByRole("combobox", { name: "Reasoning effort: Selection" }),
    { target: { value: "override" } },
  );
  fireEvent.change(
    screen.getByRole("textbox", { name: "Reasoning effort: Override value" }),
    { target: { value: "high" } },
  );
  expect(
    change.mock.calls.at(-1)?.[0].harness_defaults.entries[0].settings.effort,
  ).toEqual({ mode: "override", value: "high" });
  expect(
    screen.getByText("Configured value: high · Source: Project · Revision: —"),
  ).toBeTruthy();
});
it("does not offer editing authority when inherited settings capability is absent", async () => {
  const change = mount(
    { harness_defaults: { version: 1, entries: [] } },
    HarnessSettingsOwner.Server,
    [],
  );
  await screen.findByText(/Harness settings are unavailable/);
  const field = screen.getByRole("combobox", {
    name: "Native permissions: Selection",
  });
  expect(field.closest("fieldset")?.disabled).toBe(true);
  fireEvent.change(field, { target: { value: "override" } });
  expect(change).not.toHaveBeenCalled();
});

it("uses Claude native permission choices and preserves hidden selectors, Codex entries and other sources", async () => {
  const codex = {
    scope: codexScope,
    settings: { version: 1, effort: { mode: "override", value: "low" } },
  };
  const source = {
    scope: {
      harness: "claude-code",
      source: "subscription",
      subscription_service: "claude",
    },
    settings: {
      version: 1,
      model: { mode: "override", value: "source-model" },
    },
  };
  const hidden = {
    mode: "override",
    value: "retained-future-tier",
    future: true,
  };
  const change = mount(
    {
      harness_defaults: {
        version: 1,
        entries: [
          codex,
          source,
          {
            scope: claudeScope,
            future: { keep: true },
            settings: {
              version: 1,
              service_tier: hidden,
              claude_permission: {
                mode: "override",
                value: "future-permission",
              },
            },
          },
        ],
      },
    },
    HarnessSettingsOwner.Server,
    [SystemCapability.HARNESS_DEFAULTS_V1],
    undefined,
    "claude-code",
  );
  const permission = screen.getByRole("combobox", {
    name: "Native Claude permissions: Override value",
  });
  await waitFor(() =>
    expect(permission.closest("fieldset")?.disabled).toBe(false),
  );
  expect(
    screen.queryByRole("combobox", { name: "Approval policy: Selection" }),
  ).toBeNull();
  expect(
    screen.queryByRole("textbox", { name: "Service tier: Override value" }),
  ).toBeNull();
  expect(
    within(permission).getByRole("option", {
      name: "future-permission · Unsupported",
    }),
  ).toBeTruthy();
  for (const value of [
    "default",
    "plan",
    "acceptEdits",
    "dontAsk",
    "bypassPermissions",
    "auto",
  ])
    expect(
      within(permission).getByRole("option", { name: value }),
    ).toBeTruthy();
  fireEvent.change(permission, { target: { value: "auto" } });
  const saved = change.mock.calls.at(-1)![0].harness_defaults;
  expect(saved.entries[0]).toEqual(codex);
  expect(saved.entries[1]).toEqual(source);
  expect(saved.entries[2]).toMatchObject({
    future: { keep: true },
    settings: {
      service_tier: hidden,
      claude_permission: { mode: "override", value: "auto" },
    },
  });
  permission.focus();
  await act(() => i18n.changeLanguage("ko"));
  expect(document.activeElement).toBe(permission);
  expect((permission as HTMLSelectElement).value).toBe("auto");
  fireEvent.click(
    within(permission.closest("section")!).getByRole("button", {
      name: "상속으로 초기화",
    }),
  );
  expect(
    change.mock.calls.at(-1)![0].harness_defaults.entries[2].settings
      .claude_permission,
  ).toEqual({ mode: "inherit" });
});
it("resets a Claude Project override to the current inherited server value", async () => {
  const change = mount(
    {
      harness_defaults: {
        version: 1,
        entries: [
          {
            scope: claudeScope,
            settings: {
              version: 1,
              effort: { mode: "override", value: "high" },
            },
          },
        ],
      },
    },
    HarnessSettingsOwner.Project,
    [SystemCapability.HARNESS_DEFAULTS_V1],
    {
      version: 1,
      entries: [
        {
          scope: claudeScope,
          settings: { version: 1, effort: { mode: "override", value: "low" } },
        },
      ],
    },
    "claude-code",
  );
  const input = screen.getByRole("textbox", {
    name: "Reasoning effort: Override value",
  });
  await waitFor(() => expect(input.closest("fieldset")?.disabled).toBe(false));
  fireEvent.click(
    within(input.closest("section")!).getByRole("button", {
      name: "Reset to inherited",
    }),
  );
  await screen.findByText(
    "Configured value: low · Source: Server defaults · Revision: 12",
  );
  expect(
    change.mock.calls.at(-1)![0].harness_defaults.entries[0].settings.effort,
  ).toEqual({ mode: "inherit" });
});
it("previews the exact Claude Agent route while retaining its independent override owner", async () => {
  const routes: HarnessRoutePreview[] = [
    {
      scope: {
        harness: "claude-code",
        source: HarnessScopeSource.Subscription,
        subscription_service: "claude",
      },
      modelSelection: { mode: "override", value: "route-model" },
    },
  ];
  const change = mount(
    {
      harness: "claude-code",
      harness_settings: {
        version: 1,
        effort: { mode: "override", value: "high" },
        service_tier: { mode: "override", value: "hidden" },
      },
    },
    HarnessSettingsOwner.Agent,
    [SystemCapability.HARNESS_DEFAULTS_V1],
    {
      version: 1,
      entries: [
        {
          scope: claudeScope,
          settings: { version: 1, effort: { mode: "override", value: "low" } },
        },
      ],
    },
    "claude-code",
    routes,
  );
  await screen.findByText(
    "Configured value: route-model · Source: Agent Worker · Revision: —",
  );
  expect(
    screen.queryByRole("textbox", { name: "Default model: Override value" }),
  ).toBeNull();
  const input = screen.getByRole("textbox", {
    name: "Reasoning effort: Override value",
  });
  await waitFor(() => expect(input.closest("fieldset")?.disabled).toBe(false));
  fireEvent.click(
    within(input.closest("section")!).getByRole("button", {
      name: "Reset to inherited",
    }),
  );
  await screen.findByText(
    "Configured value: low · Source: Server defaults · Revision: 12",
  );
  expect(change.mock.calls.at(-1)![0].harness_settings.service_tier).toEqual({
    mode: "override",
    value: "hidden",
  });
});

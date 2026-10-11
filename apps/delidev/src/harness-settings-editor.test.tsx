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
import { HarnessSettingsPanel } from "./harness-settings-panel";
import { HarnessSettingsEditor } from "./harness-settings-editor";
import {
  claudeScope,
  codexScope,
  HarnessSettingsOwner,
  type HarnessScope,
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
  harness: HarnessScope["harness"] = "codex",
  panel = false,
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
    const props = {
      active: true,
      data: current,
      owner,
      change: (next: Document) => {
        change(next);
        set(next);
      },
    };
    return panel ? (
      <HarnessSettingsPanel {...props} />
    ) : (
      <HarnessSettingsEditor {...props} harness={harness} />
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

it("uses one native search control and preserves inherited unknowns and explicit modes", async () => {
  const change = mount(
    { harness_defaults: { version: 1, entries: [] } },
    HarnessSettingsOwner.Server,
    [
      SystemCapability.HARNESS_DEFAULTS_V1,
      SystemCapability.CODEX_WEB_SEARCH_V1,
    ],
  );
  const control = screen.getByRole("combobox", { name: "Web search" });
  await waitFor(() =>
    expect(control.closest("fieldset")?.disabled).toBe(false),
  );
  expect((control as HTMLSelectElement).value).toBe("inherit");
  expect(change).not.toHaveBeenCalled();
  expect(
    (
      within(control).getByRole("option", {
        name: "Cached",
      }) as HTMLOptionElement
    ).disabled,
  ).toBe(true);
  fireEvent.change(screen.getByRole("combobox", { name: "Account source" }), {
    target: { value: "subscription" },
  });
  expect(
    (
      within(control).getByRole("option", {
        name: "Cached",
      }) as HTMLOptionElement
    ).disabled,
  ).toBe(false);
  for (const mode of ["disabled", "cached", "live"]) {
    fireEvent.change(control, { target: { value: mode } });
    expect(
      change.mock.lastCall?.[0].harness_defaults.entries.at(-1).settings
        .web_search,
    ).toEqual({ mode: "override", value: mode });
  }
  fireEvent.change(control, { target: { value: "inherit" } });
  expect(
    change.mock.lastCall?.[0].harness_defaults.entries.at(-1).settings
      .web_search,
  ).toEqual({ mode: "inherit" });
});

it("edits only Claude selectors and preserves other harness defaults and unsupported hidden fields", async () => {
  const codex = {
    scope: codexScope,
    settings: { version: 1, effort: { mode: "override", value: "ultra" } },
  };
  const change = mount(
    {
      harness_defaults: {
        version: 1,
        entries: [
          codex,
          {
            scope: claudeScope,
            settings: {
              version: 1,
              service_tier: { mode: "override", value: "" },
              max_concurrency: { mode: "override", value: 0 },
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
  await screen.findByRole("heading", { name: "Claude Code CLI" });
  const effort = screen.getByRole("combobox", {
    name: "Reasoning effort: Selection",
  });
  await waitFor(() => expect(effort.closest("fieldset")?.disabled).toBe(false));
  expect(
    screen.queryByRole("combobox", { name: "Approval policy: Selection" }),
  ).toBeNull();
  expect(screen.queryByRole("spinbutton")).toBeNull();
  expect(
    screen.getByText(/Additional Claude extension settings are unavailable/),
  ).toBeTruthy();
  fireEvent.change(effort, { target: { value: "override" } });
  const value = screen.getByRole("combobox", {
    name: "Reasoning effort: Override value",
  });
  fireEvent.change(value, { target: { value: "high" } });
  fireEvent.change(
    screen.getByRole("combobox", { name: "Native permissions: Selection" }),
    { target: { value: "override" } },
  );
  fireEvent.change(
    screen.getByRole("combobox", {
      name: "Native permissions: Override value",
    }),
    { target: { value: "plan" } },
  );
  const entries = change.mock.lastCall?.[0].harness_defaults.entries;
  expect(entries[0]).toEqual(codex);
  expect(entries[1].settings).toMatchObject({
    effort: { mode: "override", value: "high" },
    claude_permission: { mode: "override", value: "plan" },
    service_tier: { mode: "override", value: "" },
    max_concurrency: { mode: "override", value: 0 },
  });
  value.focus();
  const calls = change.mock.calls.length;
  await act(() => i18n.changeLanguage("ko"));
  expect(document.activeElement).toBe(value);
  expect((value as HTMLSelectElement).value).toBe("high");
  expect(change).toHaveBeenCalledTimes(calls);
});

it("resets a Claude Agent override to its current server source without changing routes", async () => {
  const defaults = {
    version: 1,
    entries: [
      {
        scope: claudeScope,
        settings: { version: 1, effort: { mode: "override", value: "low" } },
      },
    ],
  };
  const routes = [{ model_selection: { mode: "override", value: "selected" } }];
  const change = mount(
    {
      harness: "claude-code",
      source_routes: routes,
      harness_settings: {
        version: 1,
        effort: { mode: "override", value: "high" },
        claude_permission: { mode: "override", value: "" },
      },
    },
    HarnessSettingsOwner.Agent,
    [SystemCapability.HARNESS_DEFAULTS_V1],
    defaults,
    "claude-code",
  );
  await screen.findByText(
    "Configured value: high · Source: Agent Worker · Revision: —",
  );
  const input = screen.getByRole("combobox", {
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
  expect(change.mock.lastCall?.[0]).toMatchObject({
    source_routes: routes,
    harness_settings: {
      effort: { mode: "inherit" },
      claude_permission: { mode: "override", value: "" },
    },
  });
});

it("keeps Claude source scopes independent and offers only Claude subscriptions", async () => {
  const change = mount(
    { harness_defaults: { version: 1, entries: [] } },
    HarnessSettingsOwner.Server,
    [SystemCapability.HARNESS_DEFAULTS_V1],
    undefined,
    "claude-code",
  );
  const source = screen.getByRole("combobox", { name: "Account source" });
  await waitFor(() => expect(source.closest("fieldset")?.disabled).toBe(false));
  fireEvent.change(source, { target: { value: "subscription" } });
  const service = screen.getByRole("combobox", {
    name: "Subscription service",
  });
  expect(within(service).getByRole("option", { name: "Claude" })).toBeTruthy();
  expect(within(service).queryByRole("option", { name: "ChatGPT" })).toBeNull();
  fireEvent.change(service, { target: { value: "claude" } });
  fireEvent.change(
    screen.getByRole("combobox", { name: "Reasoning effort: Selection" }),
    { target: { value: "override" } },
  );
  expect(change.mock.lastCall?.[0].harness_defaults.entries[0].scope).toEqual({
    harness: "claude-code",
    source: "subscription",
    subscription_service: "claude",
  });
});

it("keeps both Harness menus in one draft while editing independent source defaults", async () => {
  const change = mount(
    { name: "original", harness_defaults: { version: 1, entries: [] } },
    HarnessSettingsOwner.Server,
    [SystemCapability.HARNESS_DEFAULTS_V1],
    undefined,
    "codex",
    true,
  );
  const codex = within(screen.getByRole("region", { name: "Codex CLI" }));
  const claude = within(
    screen.getByRole("region", { name: "Claude Code CLI" }),
  );
  const codexEffort = codex.getByRole("combobox", {
    name: "Reasoning effort: Selection",
  });
  await waitFor(() =>
    expect(codexEffort.closest("fieldset")?.disabled).toBe(false),
  );
  fireEvent.change(codexEffort, { target: { value: "override" } });
  fireEvent.change(
    codex.getByRole("textbox", { name: "Reasoning effort: Override value" }),
    { target: { value: "high" } },
  );
  fireEvent.change(
    claude.getByRole("combobox", { name: "Reasoning effort: Selection" }),
    { target: { value: "override" } },
  );
  fireEvent.change(
    claude.getByRole("combobox", { name: "Reasoning effort: Override value" }),
    { target: { value: "low" } },
  );
  fireEvent.change(claude.getByRole("combobox", { name: "Account source" }), {
    target: { value: "api" },
  });
  fireEvent.change(claude.getByRole("combobox", { name: "Account source" }), {
    target: { value: "any" },
  });
  expect(
    (
      codex.getByRole("textbox", {
        name: "Reasoning effort: Override value",
      }) as HTMLInputElement
    ).value,
  ).toBe("high");
  expect(
    (
      claude.getByRole("combobox", {
        name: "Reasoning effort: Override value",
      }) as HTMLSelectElement
    ).value,
  ).toBe("low");
  expect(change.mock.lastCall?.[0]).toMatchObject({
    name: "original",
    harness_defaults: {
      entries: [
        {
          scope: codexScope,
          settings: { effort: { mode: "override", value: "high" } },
        },
        {
          scope: claudeScope,
          settings: { effort: { mode: "override", value: "low" } },
        },
      ],
    },
  });
});

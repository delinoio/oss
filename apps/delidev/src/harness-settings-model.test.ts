// SPDX-License-Identifier: Apache-2.0
import { expect, it } from "vitest";
import {
  codexScope,
  claudeScope,
  configuredHarnessValue,
  HarnessScopeSource,
  HarnessSettingMode,
  HarnessSettingsOwner,
  matchingHarnessSettings,
  harnessRoutePreviews,
  scopedHarnessSettings,
  setHarnessSelector,
  updateHarnessDefaults,
  type HarnessScope,
} from "./harness-settings-model";
const override = (value: string | number) => ({ mode: "override", value });
it("resolves exact protocol, Provider, source and general defaults independently of inventory order", () => {
  const selected: HarnessScope = {
    harness: "codex",
    source: HarnessScopeSource.Api,
    provider_id: "selected",
    api_protocol: "openai-responses",
  };
  const entries = [
    {
      scope: codexScope,
      settings: {
        version: 1,
        model: override("global"),
        effort: override("low"),
      },
    },
    {
      scope: { harness: "codex", source: "api", provider_id: "foreign" },
      settings: { model: override("foreign") },
    },
    {
      scope: { harness: "codex", source: "subscription" },
      settings: { model: override("subscription") },
    },
    {
      scope: { harness: "codex", source: "api" },
      settings: { model: override("api") },
    },
    {
      scope: { harness: "codex", source: "api", provider_id: "selected" },
      settings: { model: override("provider") },
    },
    { scope: selected, settings: { model: override("protocol") } },
  ];
  for (const ordered of [entries, [...entries].reverse()]) {
    const settings = matchingHarnessSettings(
      { version: 1, entries: ordered },
      selected,
    );
    expect(
      configuredHarnessValue("model", [
        { owner: HarnessSettingsOwner.Server, settings },
      ])?.value,
    ).toBe("protocol");
    expect(
      configuredHarnessValue("effort", [
        { owner: HarnessSettingsOwner.Server, settings },
      ])?.value,
    ).toBe("low");
    expect(
      configuredHarnessValue("permission", [
        { owner: HarnessSettingsOwner.Server, settings },
      ]),
    ).toBeUndefined();
  }
});
it("keeps Agent, Project and server inheritance live while preserving explicit empty and zero overrides", () => {
  const project = { version: 1, effort: override("high") };
  const agent = {
    version: 1,
    effort: { mode: "inherit" },
    service_tier: override(""),
    max_concurrency: override(0),
  };
  const layers = [
    { owner: HarnessSettingsOwner.Agent, settings: [agent], revision: "9" },
    {
      owner: HarnessSettingsOwner.Project,
      settings: [project],
      revision: "10",
    },
    {
      owner: HarnessSettingsOwner.Server,
      settings: [{ version: 1, effort: override("low") }],
      revision: "11",
    },
  ];
  expect(configuredHarnessValue("effort", layers)).toEqual({
    value: "high",
    owner: HarnessSettingsOwner.Project,
    revision: "10",
  });
  expect(configuredHarnessValue("service_tier", layers)?.value).toBe("");
  expect(configuredHarnessValue("max_concurrency", layers)?.value).toBe(0);
  layers[0].settings = [
    setHarnessSelector(
      agent,
      "effort",
      HarnessSettingMode.Override,
      "medium",
    ) as typeof agent,
  ];
  expect(configuredHarnessValue("effort", layers)?.value).toBe("medium");
  layers[0].settings = [
    setHarnessSelector(
      agent,
      "effort",
      HarnessSettingMode.Inherit,
      "",
    ) as typeof agent,
  ];
  project.effort = override("xhigh");
  expect(configuredHarnessValue("effort", layers)?.value).toBe("xhigh");
});
it("updates only the exact source scope and keeps imported selectors and other harness entries", () => {
  const other = {
    scope: { harness: "claude-code", source: "any" },
    settings: { version: 1, effort: override("high") },
  };
  const saved = {
    version: 1,
    entries: [
      other,
      {
        scope: codexScope,
        settings: {
          version: 1,
          model: override("original"),
          permission: { mode: "inherit" },
        },
      },
    ],
  };
  const next = updateHarnessDefaults(
    saved,
    codexScope,
    setHarnessSelector(
      scopedHarnessSettings(saved, codexScope),
      "permission",
      HarnessSettingMode.Override,
      "read-only",
    ),
  );
  expect((next.entries as unknown[])[0]).toBe(other);
  expect(scopedHarnessSettings(next, codexScope)).toEqual({
    version: 1,
    model: override("original"),
    permission: override("read-only"),
  });
  expect(saved.entries[1].settings).toMatchObject({
    permission: { mode: "inherit" },
  });
});

it("preserves hidden entry and selector values when one visible field changes", () => {
  const saved = {
    version: 1,
    metadata: "retain",
    entries: [
      {
        scope: codexScope,
        source_note: "retain",
        settings: {
          version: 1,
          future: { mode: "override", value: "retain" },
          effort: override("high"),
        },
      },
    ],
  };
  const changed = updateHarnessDefaults(
    saved,
    codexScope,
    setHarnessSelector(
      scopedHarnessSettings(saved, codexScope),
      "effort",
      HarnessSettingMode.Inherit,
      "",
    ),
  );
  expect(changed).toMatchObject({
    metadata: "retain",
    entries: [
      {
        source_note: "retain",
        settings: {
          future: { mode: "override", value: "retain" },
          effort: { mode: "inherit" },
        },
      },
    ],
  });
});
it("binds model previews to the exact original source independently of inheritance", () => {
  const previews = harnessRoutePreviews([
    {
      model: { provider_id: "one", native_id: "" },
      model_selection: { mode: "inherit" },
    },
    {
      model: { subscription_service: "chatgpt", native_id: "chosen" },
      model_selection: override("chosen"),
    },
  ]);
  expect(previews).toEqual([
    {
      scope: { harness: "codex", source: "api", provider_id: "one" },
      modelSelection: { mode: "inherit" },
    },
    {
      scope: {
        harness: "codex",
        source: "subscription",
        subscription_service: "chatgpt",
      },
      modelSelection: override("chosen"),
    },
  ]);
});

it("keeps Claude exact source precedence separate from Codex and resolves Agent, Project then server", () => {
  const scope: HarnessScope = {
    harness: "claude-code",
    source: HarnessScopeSource.Api,
    provider_id: "chosen-provider",
    api_protocol: "anthropic-messages",
  };
  const defaults = {
    version: 1,
    entries: [
      { scope: codexScope, settings: { effort: override("codex") } },
      { scope: claudeScope, settings: { effort: override("all-claude") } },
      {
        scope: { ...scope, api_protocol: "foreign-profile" },
        settings: { effort: override("foreign") },
      },
      { scope, settings: { effort: override("exact-server") } },
    ],
  };
  const server = {
    owner: HarnessSettingsOwner.Server,
    revision: "1",
    settings: matchingHarnessSettings(defaults, scope),
  };
  const project: import("./harness-settings-model").HarnessSettingsLayer = {
    owner: HarnessSettingsOwner.Project,
    revision: "2",
    settings: [{ effort: override("project") }],
  };
  const agent: import("./harness-settings-model").HarnessSettingsLayer = {
    owner: HarnessSettingsOwner.Agent,
    revision: "3",
    settings: [{ effort: override("agent") }],
  };
  expect(configuredHarnessValue("effort", [agent, project, server])).toEqual({
    value: "agent",
    owner: HarnessSettingsOwner.Agent,
    revision: "3",
  });
  agent.settings = [{ effort: { mode: "inherit" } }];
  expect(
    configuredHarnessValue("effort", [agent, project, server])?.value,
  ).toBe("project");
  project.settings = [{ effort: { mode: "inherit" } }];
  expect(
    configuredHarnessValue("effort", [agent, project, server])?.value,
  ).toBe("exact-server");
  const route = harnessRoutePreviews(
    [
      {
        model: { provider_id: "chosen-provider", native_id: "" },
        model_selection: { mode: "inherit" },
      },
    ],
    "claude-code",
  );
  expect(route).toEqual([
    {
      scope: {
        harness: "claude-code",
        source: "api",
        provider_id: "chosen-provider",
      },
      modelSelection: { mode: "inherit" },
    },
  ]);
});

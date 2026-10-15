import { create } from "@bufbuild/protobuf";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { expect, it } from "vitest";
import {
  EntityKind,
  ResourceSchema,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { encode } from "./documents";
import {
  ExecutionConfiguration as OriginalExecutionConfiguration,
  SavedExecutionChips,
  savedExecutionSnapshot,
} from "./execution-configuration";
import { rawSourceModelKey, sourceModelKey } from "./source-model-key-fixture";
import { useState, type ComponentProps } from "react";
function ExecutionConfiguration(
  props: ComponentProps<typeof OriginalExecutionConfiguration>,
) {
  const [target, setTarget] = useState<HTMLDivElement | null>(null);
  return (
    <>
      <div ref={setTarget} />
      <OriginalExecutionConfiguration {...props} diagnosticsTarget={target} />
    </>
  );
}

function fixture(harness = "codex") {
  const execution = newRequestId(),
    input = newRequestId(),
    account = newRequestId();
  const first =
    "Original first template 지침.\n  Keep indentation. <script>privateScript()</script>";
  const second =
    "Original second template.\nDo not use the later edited template.";
  const provider = newRequestId();
  const configuration = {
    agent_id: newRequestId(),
    agent_revision: 3,
    model_key: sourceModelKey({
      provider_id: provider,
      native_id: "original-model",
    }),
    model_revision: 7,
    provider_id: provider,
    harness,
    native_model: "original-model",
    effort: "high",
    options: {
      permission: "workspace-write",
      approval_policy: "on-request",
      subagent_model: "original-child",
      subagent_effort: "medium",
      max_concurrency: 4,
      approval_review_model: "original-review",
      service_tier: "priority",
    },
    accounts: [
      { id: account, weight: 2 },
      { id: newRequestId(), weight: 1 },
    ],
    routing: "priority",
    templates: [
      { id: newRequestId(), revision: 2, contents: first },
      { id: newRequestId(), revision: 8, contents: second },
    ],
    instructions: first + "\n\n" + second,
    native_system_prompt: "PRIVATE NATIVE PROMPT MUST NEVER APPEAR",
  };
  const data = {
    initial_execution: {
      id: execution,
      input_id: input,
      initial_account_id: account,
      configuration,
      accepted_at: "2026-09-26T01:00:00Z",
    },
    execution: {
      execution_id: execution,
      input_id: input,
      native_thread_id: "native-original",
      observed: {
        model: "original-model",
        effort: "high",
        service_tier: null,
        permission: "workspace-write",
        approval_policy: "on-request",
        native_prompt: "PRIVATE OBSERVER PROMPT",
      },
    },
  };
  const resource = create(ResourceSchema, {
    kind: EntityKind.SESSION,
    id: newRequestId(),
    revision: 9n,
    schemaVersion: 1,
    documentJson: encode(data),
  });
  return { data, resource, configuration, first, second };
}

function detail(region: HTMLElement, label: string): string | null | undefined {
  return within(region).getByText(label, { selector: "dt" }).nextElementSibling
    ?.textContent;
}

it("labels only immutable saved selections and omits unspecified effort", () => {
  const value = fixture();
  const mounted = render(<SavedExecutionChips resource={value.resource} />);
  expect(
    screen.getByRole("group", { name: "Saved model: original-model" }),
  ).toBeTruthy();
  expect(
    screen.getByRole("group", { name: "Saved reasoning effort: high" }),
  ).toBeTruthy();
  const data = {
    ...value.data,
    current_execution: {
      configuration: { native_model: "later-model", effort: "low" },
    },
  };
  mounted.rerender(
    <SavedExecutionChips
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  expect(screen.queryByText("later-model")).toBeNull();
  value.configuration.effort = "";
  mounted.rerender(
    <SavedExecutionChips
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  expect(
    screen.queryByRole("group", { name: /Saved reasoning effort/ }),
  ).toBeNull();
  mounted.rerender(
    <SavedExecutionChips resource={{ ...value.resource, schemaVersion: 2 }} />,
  );
  expect(
    screen.getByRole("group", { name: "Saved model: Unavailable" }),
  ).toBeTruthy();
});

type Mutation = (
  data: { initial_execution: { id: string } },
  configuration: Record<string, unknown>,
) => void;
const invalidSnapshots: [string, Mutation][] = [
  [
    "a non-canonical execution id",
    (data) => {
      data.initial_execution.id = "not-a-uuid";
    },
  ],
  [
    "an uppercase execution id",
    (data) => {
      data.initial_execution.id = data.initial_execution.id.toUpperCase();
    },
  ],
  [
    "a non-canonical agent id",
    (_, configuration) => {
      configuration.agent_id = "agent-1";
    },
  ],
  [
    "a model key without the source prefix",
    (_, configuration) => {
      configuration.model_key = "model-1";
    },
  ],
  [
    "a resource UUID in place of the source model key",
    (_, configuration) => {
      configuration.model_key = newRequestId();
    },
  ],
  [
    "a source model key with a malformed payload",
    (_, configuration) => {
      configuration.model_key = "source:***";
    },
  ],
  [
    "a source model key whose payload is not JSON",
    (_, configuration) => {
      configuration.model_key = "source:bm90LWpzb24";
    },
  ],
  [
    "a source model key for a different native model",
    (_, configuration) => {
      configuration.model_key = sourceModelKey({
        provider_id: configuration.provider_id as string,
        native_id: "another-model",
      });
    },
  ],
  [
    "a source model key for a different provider",
    (_, configuration) => {
      configuration.model_key = sourceModelKey({
        provider_id: newRequestId(),
        native_id: "original-model",
      });
    },
  ],
  [
    "a source model key for a subscription service while a provider is saved",
    (_, configuration) => {
      configuration.model_key = sourceModelKey({
        subscription_service: "chatgpt",
        native_id: "original-model",
      });
    },
  ],
  [
    "a subscription service whose harness differs from the saved harness",
    (_, configuration) => {
      delete configuration.provider_id;
      configuration.harness = "claude-code";
      configuration.subscription_service = "chatgpt";
      configuration.model_key = sourceModelKey({
        subscription_service: "chatgpt",
        native_id: "original-model",
      });
    },
  ],
  [
    "a source model key naming both a provider and a subscription service",
    (_, configuration) => {
      configuration.model_key = sourceModelKey({
        provider_id: configuration.provider_id as string,
        subscription_service: "chatgpt",
        native_id: "original-model",
      });
    },
  ],
  [
    "a source model key with an unsupported subscription service",
    (_, configuration) => {
      delete configuration.provider_id;
      configuration.subscription_service = "unknown";
      configuration.model_key = sourceModelKey({
        subscription_service: "unknown",
        native_id: "original-model",
      });
    },
  ],
  [
    "a source model key with a non-canonical provider id",
    (_, configuration) => {
      configuration.provider_id = "provider-1";
      configuration.model_key = sourceModelKey({
        provider_id: "provider-1",
        native_id: "original-model",
      });
    },
  ],
  [
    "a source model key with reordered identity fields",
    (_, configuration) => {
      configuration.model_key = rawSourceModelKey(
        `{"native_id":"original-model","provider_id":"${configuration.provider_id}"}`,
      );
    },
  ],
  [
    "a source model key with whitespace in the identity JSON",
    (_, configuration) => {
      configuration.model_key = rawSourceModelKey(
        `{"provider_id": "${configuration.provider_id}", "native_id": "original-model"}`,
      );
    },
  ],
  [
    "a source model key with a duplicate identity field",
    (_, configuration) => {
      configuration.model_key = rawSourceModelKey(
        `{"provider_id":"${newRequestId()}","provider_id":"${configuration.provider_id}","native_id":"original-model"}`,
      );
    },
  ],
  [
    "a source model key with non-canonical base64 trailing bits",
    (_, configuration) => {
      const alphabet =
        "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-_";
      const canonical = configuration.model_key as string;
      const payload = canonical.slice("source:".length);
      // The canonical payload length leaves unused low bits in the last
      // character; setting the lowest bit decodes to the same bytes.
      expect(payload.length % 4).not.toBe(0);
      const last = alphabet.indexOf(payload.slice(-1));
      expect(last % 2).toBe(0);
      configuration.model_key = canonical.slice(0, -1) + alphabet[last + 1];
    },
  ],
  [
    "an unsupported harness",
    (_, configuration) => {
      configuration.harness = "unknown-harness";
    },
  ],
  [
    "a missing harness",
    (_, configuration) => {
      delete configuration.harness;
    },
  ],
  [
    "an agent revision above the uint64 range",
    (_, configuration) => {
      configuration.agent_revision = 1e30;
    },
  ],
  [
    "a model revision above the uint64 range",
    (_, configuration) => {
      configuration.model_revision = 2 ** 64 * 2;
    },
  ],
  [
    "a missing agent revision",
    (_, configuration) => {
      delete configuration.agent_revision;
    },
  ],
  [
    "a missing model revision",
    (_, configuration) => {
      delete configuration.model_revision;
    },
  ],
  [
    "a missing native model",
    (_, configuration) => {
      delete configuration.native_model;
    },
  ],
  [
    "an empty native model",
    (_, configuration) => {
      configuration.native_model = "";
    },
  ],
  [
    "a whitespace-only native model",
    (_, configuration) => {
      const native = " \u3000\u0085\n";
      configuration.native_model = native;
      configuration.model_key = sourceModelKey({
        provider_id: configuration.provider_id as string,
        native_id: native,
      });
    },
  ],
  [
    "a native model containing NUL",
    (_, configuration) => {
      const native = "original\u0000model";
      configuration.native_model = native;
      configuration.model_key = sourceModelKey({
        provider_id: configuration.provider_id as string,
        native_id: native,
      });
    },
  ],
  [
    "a native model longer than 256 UTF-8 bytes",
    (_, configuration) => {
      const native = "모".repeat(86);
      configuration.native_model = native;
      configuration.model_key = sourceModelKey({
        provider_id: configuration.provider_id as string,
        native_id: native,
      });
    },
  ],
  [
    "a native model that is not well-formed UTF-8",
    (_, configuration) => {
      const native = "original\ud800model";
      configuration.native_model = native;
      configuration.model_key = sourceModelKey({
        provider_id: configuration.provider_id as string,
        native_id: native,
      });
    },
  ],
  [
    "a non-string effort",
    (_, configuration) => {
      configuration.effort = 5;
    },
  ],
  [
    "missing options",
    (_, configuration) => {
      delete configuration.options;
    },
  ],
  [
    "non-array accounts",
    (_, configuration) => {
      configuration.accounts = {};
    },
  ],
  [
    "unsupported routing",
    (_, configuration) => {
      configuration.routing = "random";
    },
  ],
  [
    "missing templates",
    (_, configuration) => {
      delete configuration.templates;
    },
  ],
  [
    "missing instructions",
    (_, configuration) => {
      delete configuration.instructions;
    },
  ],
];

it.each(invalidSnapshots)(
  "rejects %s with unavailable chips and incomplete-snapshot guidance",
  (_name, mutate) => {
    const value = fixture();
    const data = structuredClone(value.data);
    mutate(
      data,
      data.initial_execution.configuration as Record<string, unknown>,
    );
    const resource = { ...value.resource, documentJson: encode(data) };
    expect(savedExecutionSnapshot(resource).ready).toBe(false);
    const view = render(
      <>
        <SavedExecutionChips resource={resource} />
        <ExecutionConfiguration resource={resource} />
      </>,
    );
    expect(
      screen.getByRole("group", { name: "Saved model: Unavailable" }),
    ).toBeTruthy();
    expect(screen.queryByRole("group", { name: /original-model/ })).toBeNull();
    expect(
      screen.queryByRole("group", { name: /Saved reasoning effort/ }),
    ).toBeNull();
    expect(
      screen.getByText(/saved execution configuration is incomplete/i),
    ).toBeTruthy();
    expect(
      screen.queryByRole("region", { name: "Saved execution configuration" }),
    ).toBeNull();
    view.unmount();
  },
);

it("keeps native observations and applied instructions when only strict saved-configuration fields are invalid", () => {
  const value = fixture();
  const data = structuredClone(value.data);
  delete (data.initial_execution.configuration as Record<string, unknown>)
    .templates;
  const resource = { ...value.resource, documentJson: encode(data) };
  expect(savedExecutionSnapshot(resource).ready).toBe(false);
  render(<ExecutionConfiguration resource={resource} />);
  expect(
    screen.getByText(/saved execution configuration is incomplete/i),
  ).toBeTruthy();
  expect(
    screen.queryByRole("region", { name: "Saved execution configuration" }),
  ).toBeNull();
  expect(
    screen.getByRole("region", { name: "Native execution observations" }),
  ).toBeTruthy();
  expect(
    screen.getByRole("region", { name: "Applied DeliDev instructions" }),
  ).toBeTruthy();
});

it("accepts the source-scoped model key of API provider and subscription snapshots", () => {
  const api = fixture();
  expect((api.configuration as Record<string, unknown>).model_key).toMatch(
    /^source:[A-Za-z0-9_-]+$/,
  );
  expect(savedExecutionSnapshot(api.resource).ready).toBe(true);
  for (const [service, harness] of [
    ["chatgpt", "codex"],
    ["claude", "claude-code"],
    ["grok", "grok-build"],
    ["opencode_go", "opencode"],
  ]) {
    const value = fixture(harness);
    const configuration = value.configuration as Record<string, unknown>;
    delete configuration.provider_id;
    configuration.subscription_service = service;
    configuration.model_key = sourceModelKey({
      subscription_service: service,
      native_id: "original-model",
    });
    const resource = { ...value.resource, documentJson: encode(value.data) };
    expect(savedExecutionSnapshot(resource)).toEqual({
      ready: true,
      nativeModel: "original-model",
      effort: "high",
    });
  }
});

it("accepts a source model key whose native model ID is not ASCII", () => {
  const value = fixture();
  const configuration = value.configuration as Record<string, unknown>;
  configuration.native_model = "모델/<preview>&v2";
  configuration.model_key = sourceModelKey({
    provider_id: configuration.provider_id as string,
    native_id: "모델/<preview>&v2",
  });
  const resource = { ...value.resource, documentJson: encode(value.data) };
  expect(savedExecutionSnapshot(resource)).toEqual({
    ready: true,
    nativeModel: "모델/<preview>&v2",
    effort: "high",
  });
});

it("accepts a native model ID of exactly 256 UTF-8 bytes", () => {
  const value = fixture();
  const configuration = value.configuration as Record<string, unknown>;
  const native = `${"모".repeat(85)}x`;
  expect(new TextEncoder().encode(native).length).toBe(256);
  configuration.native_model = native;
  configuration.model_key = sourceModelKey({
    provider_id: configuration.provider_id as string,
    native_id: native,
  });
  const resource = { ...value.resource, documentJson: encode(value.data) };
  expect(savedExecutionSnapshot(resource)).toEqual({
    ready: true,
    nativeModel: native,
    effort: "high",
  });
});

it("accepts every supported harness and keeps unspecified effort omitted", () => {
  for (const harness of ["codex", "claude-code", "opencode", "grok-build"]) {
    const value = fixture(harness);
    const snapshot = savedExecutionSnapshot(value.resource);
    expect(snapshot).toEqual({
      ready: true,
      nativeModel: "original-model",
      effort: "high",
    });
  }
  const value = fixture();
  delete (value.configuration as Record<string, unknown>).effort;
  const resource = {
    ...value.resource,
    documentJson: encode(value.data),
  };
  expect(savedExecutionSnapshot(resource)).toEqual({
    ready: true,
    nativeModel: "original-model",
    effort: "",
  });
});

it("keeps selected Grok context provenance separate from native context observation", () => {
  const value = fixture("grok-build");
  const data = {
    ...value.data,
    initial_execution: {
      ...value.data.initial_execution,
      configuration: {
        ...value.configuration,
        grok_context: { tokens: 48000, source: "user-declared" },
      },
    },
    execution: {
      ...value.data.execution,
      observed: {
        model: "original-model",
        grok_mode: "default",
        grok_context_tokens: 48000,
      },
    },
  };
  const view = render(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  expect(
    screen.queryByText("Execution configuration and instructions"),
  ).toBeNull();
  const saved = screen.getByRole("region", {
    name: "Saved execution configuration",
  });
  const native = screen.getByRole("region", {
    name: "Native execution observations",
  });
  expect(detail(saved, "Saved Grok context window")).toBe("48000");
  expect(detail(saved, "Saved context source")).toBe(
    "User-declared model metadata",
  );
  expect(detail(native, "Observed Grok context window")).toBe("48000");
  for (const source of ["unknown", "private-future-metadata"]) {
    const changed = {
      ...data,
      initial_execution: {
        ...data.initial_execution,
        configuration: {
          ...data.initial_execution.configuration,
          grok_context: { tokens: 32000, source },
        },
      },
      execution: {
        ...data.execution,
        observed: {
          model: "original-model",
          grok_mode: "default",
          grok_context_tokens: 0,
        },
      },
    };
    view.rerender(
      <ExecutionConfiguration
        resource={{ ...value.resource, documentJson: encode(changed) }}
      />,
    );
    expect(detail(saved, "Saved Grok context window")).toBe("Unavailable");
    expect(detail(saved, "Saved context source")).toBe("Unavailable");
    expect(detail(native, "Observed Grok context window")).toBe("Unavailable");
    expect(screen.queryByText("private-future-metadata")).toBeNull();
  }
});

it("shows None selected without routine explanatory prose when no additional instructions exist", () => {
  const value = fixture();
  const data = structuredClone(value.data);
  const configuration = data.initial_execution.configuration as Record<
    string,
    unknown
  >;
  configuration.instructions = "";
  configuration.templates = [];
  render(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  const region = screen.getByRole("region", {
    name: "Applied DeliDev instructions",
  });
  expect(within(region).getByText("None selected")).toBeTruthy();
  expect(
    within(region).queryByLabelText("Combined applied instructions"),
  ).toBeNull();
  expect(screen.queryByText(/Read-only additional instructions/)).toBeNull();
  expect(
    screen.queryByText(/No additional DeliDev instructions were selected/),
  ).toBeNull();
  expect(region.querySelector("details, summary")).toBeNull();
});

it("shows the immutable ordered instructions as inert read-only text without native prompts", () => {
  const value = fixture();
  const { container } = render(
    <ExecutionConfiguration resource={value.resource} />,
  );
  expect(screen.queryByText(/Read-only additional instructions/)).toBeNull();
  expect(screen.queryByText("None selected")).toBeNull();
  expect(
    screen.queryByText("Execution configuration and instructions"),
  ).toBeNull();
  const combined = screen.getByLabelText("Combined applied instructions");
  expect(combined.textContent).toBe(value.configuration.instructions);
  expect(container.querySelector("script")).toBeNull();
  expect(container.querySelector("input, textarea, button, a")).toBeNull();
  expect(container.textContent).not.toContain("PRIVATE NATIVE PROMPT");
  expect(container.textContent).not.toContain("PRIVATE OBSERVER PROMPT");
  const templates = screen
    .getByLabelText("Applied instruction template order")
    .querySelectorAll("li");
  expect(templates).toHaveLength(2);
  expect(templates[0].querySelector("pre")?.textContent).toBe(value.first);
  expect(templates[1].querySelector("pre")?.textContent).toBe(value.second);
  expect(screen.getByText(/Template 1.*revision 2/)).toBeTruthy();
  expect(screen.getByText(/Template 2.*revision 8/)).toBeTruthy();
  const saved = screen.getByRole("region", {
    name: "Saved execution configuration",
  });
  expect(detail(saved, "Requested subagent model")).toBe("original-child");
  expect(detail(saved, "Requested concurrency")).toBe("4");
  expect(detail(saved, "Requested approval review model")).toBe(
    "original-review",
  );
  expect(detail(saved, "Requested service tier")).toBe("priority");
  const savedAccount = screen.getByLabelText("Saved account order").children[0];
  expect(savedAccount.textContent).toMatch(/^Account unavailable/);
  expect(savedAccount.textContent).not.toContain(
    value.configuration.accounts[0].id,
  );
});

it("distinguishes missing native values from requested settings and empty native observations", () => {
  const value = fixture();
  value.data.execution.observed.effort = "";
  render(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(value.data) }}
    />,
  );
  expect(
    screen.queryByText("Execution configuration and instructions"),
  ).toBeNull();
  const native = screen.getByRole("region", {
    name: "Native execution observations",
  });
  expect(detail(native, "Observed reasoning effort")).toBe(
    "Empty native value",
  );
  expect(detail(native, "Observed service tier")).toBe("Unavailable");
  const saved = screen.getByRole("region", {
    name: "Saved execution configuration",
  });
  expect(detail(saved, "Requested reasoning effort")).toBe("high");
  expect(detail(saved, "Requested service tier")).toBe("priority");
});

it("preserves first-execution choices while separately identifying retained prior observations", () => {
  const value = fixture();
  const view = render(<ExecutionConfiguration resource={value.resource} />);
  expect(
    screen.queryByText("Execution configuration and instructions"),
  ).toBeNull();
  const next = {
    ...value.data,
    current_execution: {
      id: newRequestId(),
      input_id: newRequestId(),
      account_id: newRequestId(),
    },
  };
  view.rerender(
    <ExecutionConfiguration
      resource={{
        ...value.resource,
        revision: 10n,
        documentJson: encode(next),
      }}
    />,
  );
  expect(
    screen.getByText(/retained observations belong to an earlier execution/),
  ).toBeTruthy();
  expect(
    screen.getByLabelText("Combined applied instructions").textContent,
  ).toBe(value.configuration.instructions);
  const saved = screen.getByRole("region", {
    name: "Saved execution configuration",
  });
  const firstAccount = detail(document.body, "First account");
  const selectedAccount = detail(document.body, "Selected execution account");
  expect(firstAccount).toMatch(/^Account unavailable/);
  expect(selectedAccount).toMatch(/^Account unavailable/);
  expect(firstAccount).not.toContain(
    value.data.initial_execution.initial_account_id,
  );
  expect(selectedAccount).not.toContain(next.current_execution.account_id);
  const bound = {
    ...next,
    execution: {
      ...next.execution,
      execution_id: next.current_execution.id,
      input_id: next.current_execution.input_id,
    },
  };
  view.rerender(
    <ExecutionConfiguration
      resource={{
        ...value.resource,
        revision: 11n,
        documentJson: encode(bound),
      }}
    />,
  );
  expect(
    screen.queryByText(/retained observations belong to an earlier execution/),
  ).toBeNull();
  expect(
    screen.getByLabelText("Combined applied instructions").textContent,
  ).toBe(value.configuration.instructions);
});

it.each([
  ["claude-code", "claude_permission", "plan", "Observed Claude permission"],
  ["opencode", "opencode_agent", "plan", "Observed OpenCode primary agent"],
  ["grok-build", "grok_mode", "plan", "Observed Grok initial mode"],
])(
  "keeps %s native policy separate from Codex sandbox labels",
  (harness, key, selection, label) => {
    const value = fixture(harness);
    const data = {
      ...value.data,
      execution: {
        ...value.data.execution,
        observed: {
          model: "original-model",
          effort: null,
          service_tier: null,
          permission: "default",
          approval_policy: "",
          [key]: selection,
        },
      },
    };
    render(
      <ExecutionConfiguration
        resource={{ ...value.resource, documentJson: encode(data) }}
      />,
    );
    expect(
      screen.queryByText("Execution configuration and instructions"),
    ).toBeNull();
    const native = screen.getByRole("region", {
      name: "Native execution observations",
    });
    expect(detail(native, label)).toBe("plan");
    expect(within(native).queryByText("Observed sandbox")).toBeNull();
    expect(within(native).queryByText("Observed approval policy")).toBeNull();
  },
);

it("accepts the maximum uint64 revision that JSON decodes to 2^64", () => {
  const value = fixture();
  const configuration = value.configuration as Record<string, unknown>;
  configuration.agent_revision = JSON.parse("18446744073709551615");
  configuration.model_revision = JSON.parse("18446744073709551615");
  expect(configuration.agent_revision).toBe(2 ** 64);
  const resource = { ...value.resource, documentJson: encode(value.data) };
  expect(savedExecutionSnapshot(resource).ready).toBe(true);
});

it("keeps unavailable snapshots, unknown options and imprecise revisions explicit", () => {
  const value = fixture();
  const view = render(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode({}) }}
    />,
  );
  expect(
    screen.queryByText("Execution configuration and instructions"),
  ).toBeNull();
  expect(screen.getByText(/No accepted execution configuration/)).toBeTruthy();
  const data = {
    ...value.data,
    initial_execution: {
      ...value.data.initial_execution,
      configuration: {
        ...value.configuration,
        agent_revision: Number.MAX_SAFE_INTEGER + 1,
        options: {
          ...value.configuration.options,
          future_native_setting: "private unknown data",
        },
      },
    },
    execution: { observed: { model: "unbound-model" } },
  };
  view.rerender(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  expect(
    screen.getByText(/additional options that this app version cannot display/),
  ).toBeTruthy();
  expect(screen.queryByText("private unknown data")).toBeNull();
  expect(
    screen.getByText(/No native settings have been observed/),
  ).toBeTruthy();
  expect(screen.queryByText("unbound-model")).toBeNull();
  expect(
    screen.getByText(/^Agent unavailable.*revision Unavailable$/),
  ).toBeTruthy();
  expect(document.body.textContent).not.toContain(value.configuration.agent_id);
  view.rerender(
    <ExecutionConfiguration
      resource={{ ...value.resource, schemaVersion: 2 }}
    />,
  );
  expect(
    screen.getByText(/session document version is not supported/),
  ).toBeTruthy();
  expect(screen.queryByLabelText("Combined applied instructions")).toBeNull();
});

it("preserves initial Claude settings and earlier-execution ownership across sticky native mode changes", () => {
  const value = fixture("claude-code");
  const turn = newRequestId();
  const data = {
    ...value.data,
    current_execution: { id: newRequestId(), input_id: newRequestId() },
    execution: {
      ...value.data.execution,
      native_turn_id: turn,
      observed: { ...value.data.execution.observed, claude_permission: "plan" },
      claude_progress: {
        native_turn_id: turn,
        latest_status_id: newRequestId(),
        permission: "default",
        permission_changed: true,
      },
    },
  };
  const view = render(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  expect(
    screen.queryByText("Execution configuration and instructions"),
  ).toBeNull();
  const native = screen.getByRole("region", {
    name: "Native execution observations",
  });
  expect(detail(native, "Observed Claude permission")).toBe("plan");
  expect(detail(native, "Latest reported Claude permission")).toBe("default");
  expect(
    screen.getByText(/retained observations belong to an earlier execution/),
  ).toBeTruthy();
  expect(
    screen.getByText(/Further input requires configuration reconciliation/),
  ).toBeTruthy();
  data.execution.claude_progress.permission = "plan";
  view.rerender(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  expect(detail(native, "Latest reported Claude permission")).toBe("plan");
  expect(
    screen.getByText(/Further input requires configuration reconciliation/),
  ).toBeTruthy();
  data.execution.claude_progress.native_turn_id = newRequestId();
  view.rerender(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(data) }}
    />,
  );
  expect(
    screen.getByText(/Retained Claude permission progress is unavailable/),
  ).toBeTruthy();
  expect(
    screen.queryByText(/Further input requires configuration reconciliation/),
  ).toBeNull();
});

it("shows the original Fast execution tier independently of later Worker defaults and absent native evidence", () => {
  const value = fixture();
  value.configuration.options.service_tier = "fast";
  render(
    <ExecutionConfiguration
      resource={{ ...value.resource, documentJson: encode(value.data) }}
    />,
  );
  expect(
    screen.queryByText("Execution configuration and instructions"),
  ).toBeNull();
  const saved = screen.getByRole("region", {
    name: "Saved execution configuration",
  });
  expect(detail(saved, "Requested service tier")).toBe("fast");
  value.configuration.options.service_tier = "";
  expect(detail(saved, "Requested service tier")).toBe("fast");
  expect(
    detail(
      screen.getByRole("region", { name: "Native execution observations" }),
      "Observed service tier",
    ),
  ).toBe("Unavailable");
});

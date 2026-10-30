// SPDX-License-Identifier: Apache-2.0
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, it, expect, rs } from "@rstest/core";
import { createRouterTransport, ConnectError, Code } from "@connectrpc/connect";
import { create } from "@bufbuild/protobuf";
import { QueryClient } from "@tanstack/react-query";
import {
  RoutingService,
  ProviderService,
  PoolService,
  RouteSchema,
  JevDecisionConfigSchema,
  JevQuotaTiersSchema,
  type JevDecisionConfig,
  type ReasoningRetentionConfig,
  ReasoningRetentionConfigSchema,
  RoutingStrategy,
  ConnectionState,
  OrganizationSchema,
  GetSessionResponseSchema,
  type Route,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { RoutingPage } from "../src/routing";
const ordinary = create(RouteSchema, {
  id: "ordinary",
  modelId: "decision-alias",
  enabled: true,
  strategy: RoutingStrategy.DIRECT,
  targets: [
    {
      target: { case: "connectionId", value: "target" },
      modelId: "judge",
      weight: 1,
    },
  ],
});
const configured = create(RouteSchema, {
  id: "configured",
  modelId: "smart-route",
  enabled: true,
  strategy: RoutingStrategy.JEV,
  jevDecision: {
    decisionConnectionId: "decision",
    decisionServiceModel: "custom-model",
    decisionTimeoutMs: 8000,
    sessionPinTtlSeconds: 120,
  },
  targets: [
    {
      target: { case: "connectionId", value: "target" },
      modelId: "model",
      priority: 0,
      weight: 1,
      reasoningEfforts: ["high", "max"],
      modelProfile: "Careful reasoning",
    },
  ],
});
function mount(route: Route | undefined, mutate: (request: unknown) => object) {
  const transport = createRouterTransport((r) => {
    r.service(ProviderService, {
      listProviderConnections: () => ({
        connections: [
          {
            id: "target",
            name: "Inference account",
            state: ConnectionState.READY,
          },
          {
            id: "decision",
            name: "Decision service",
            providerId: "jev",
            state: ConnectionState.READY,
          },
        ],
      }),
    });
    r.service(PoolService, { listPools: () => ({}) });
    r.service(RoutingService, {
      listRoutes: () => ({ routes: route ? [route, ordinary] : [ordinary] }),
      createRoute: mutate,
      updateRoute: mutate,
    });
  });
  render(
    <ApiProvider
      rpcTransport={transport}
      queryClient={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <RoutingPage
        organization={create(OrganizationSchema, { id: "org", name: "Studio" })}
        session={create(GetSessionResponseSchema, { userId: "person" })}
      />
    </ApiProvider>,
  );
}
async function edit() {
  const heading = await screen.findByRole("heading", { name: "smart-route" });
  await userEvent.click(
    within(heading.closest("section")!).getByRole("button", {
      name: /^Edit route /,
    }),
  );
}
function setValue(element: HTMLElement, value: string) {
  fireEvent.change(element, { target: { value } });
}
function saveButton() {
  return screen.getByRole("button", {
    name: "Save routing",
  }) as HTMLButtonElement;
}
/** Returns the text of every element the control is described by. */
function describedBy(element: HTMLElement) {
  return (element.getAttribute("aria-describedby") ?? "")
    .split(" ")
    .filter(Boolean)
    .map((id) => document.getElementById(id)?.textContent ?? "");
}
function expectInvalid(label: string, message: string) {
  const control = screen.getByLabelText(label);
  expect(control.getAttribute("aria-invalid")).toBe("true");
  expect(describedBy(control)).toContain(message);
  expect(saveButton().disabled).toBe(true);
  expect(
    within(screen.getByTestId("error-summary")).getByText(message),
  ).toBeTruthy();
}
function lastRequest(mutate: { mock: { calls: unknown[][] } }) {
  return mutate.mock.calls.at(-1)![0] as {
    jevDecision?: JevDecisionConfig;
    reasoningRetention?: ReasoningRetentionConfig;
  };
}

describe("JEV decision group", () => {
  it("renders only for JEV, between route details and targets, with hints", async () => {
    mount(configured, () => ({}));
    await edit();
    const dialog = screen.getByRole("dialog", { name: "Edit routing" });
    const group = within(dialog).getByRole("group", {
      name: "JEV decision settings",
    });
    expect(group.getAttribute("aria-describedby")).toBeTruthy();
    expect(
      within(group).getByText(
        "Choose an independent personal decision service or an ordinary routing model. Decision routes cannot use JEV chains or cycles.",
      ),
    ).toBeTruthy();
    const follows = (a: Node, b: Node) =>
      expect(
        a.compareDocumentPosition(b) & Node.DOCUMENT_POSITION_FOLLOWING,
      ).toBeTruthy();
    follows(within(dialog).getByText("Route details"), group);
    follows(group, within(dialog).getByText("Ordered targets"));
    for (const label of [
      "Decision timeout (ms)",
      "Session pin duration (seconds)",
    ]) {
      expect(describedBy(screen.getByLabelText(label)).length).toBe(1);
    }
    await userEvent.selectOptions(
      screen.getByLabelText("Routing strategy"),
      String(RoutingStrategy.FALLBACK),
    );
    expect(
      within(dialog).queryByRole("group", { name: "JEV decision settings" }),
    ).toBeNull();
    // The first render in a file pays a cold-start cost under parallel load.
  }, 20000);

  it.each([
    ["Decision timeout (ms)", "0", true],
    ["Decision timeout (ms)", "1000", true],
    ["Decision timeout (ms)", "120000", true],
    ["Decision timeout (ms)", "999", false],
    ["Decision timeout (ms)", "120001", false],
    ["Decision timeout (ms)", "1500.5", false],
    ["Session pin duration (seconds)", "0", true],
    ["Session pin duration (seconds)", "60", true],
    ["Session pin duration (seconds)", "86400", true],
    ["Session pin duration (seconds)", "59", false],
    ["Session pin duration (seconds)", "86401", false],
    ["Session pin duration (seconds)", "60.5", false],
  ] as const)("%s = %s is valid: %s", async (label, value, valid) => {
    const mutate = rs.fn((_request: unknown) => ({}));
    mount(configured, mutate);
    await edit();
    setValue(screen.getByLabelText(label), value);
    if (valid) {
      expect(saveButton().disabled).toBe(false);
      expect(screen.getByLabelText(label).getAttribute("aria-invalid")).toBe(
        null,
      );
      await userEvent.click(saveButton());
      const config = lastRequest(mutate).jevDecision!;
      const key =
        label === "Decision timeout (ms)"
          ? "decisionTimeoutMs"
          : "sessionPinTtlSeconds";
      expect(config[key]).toBe(Number(value));
    } else {
      expectInvalid(
        label,
        label === "Decision timeout (ms)"
          ? "Decision timeout must be 0 or a whole number from 1000 to 120000."
          : "Session pin duration must be 0 or a whole number from 60 to 86400.",
      );
      await userEvent.click(saveButton());
      expect(mutate).not.toHaveBeenCalled();
    }
  });

  it("validates the decision service model length in characters", async () => {
    const mutate = rs.fn((_request: unknown) => ({}));
    mount(configured, mutate);
    await edit();
    setValue(screen.getByLabelText("Decision service model"), "m".repeat(512));
    expect(saveButton().disabled).toBe(false);
    setValue(screen.getByLabelText("Decision service model"), "m".repeat(513));
    expectInvalid(
      "Decision service model",
      "Decision service model must be 512 characters or fewer.",
    );
  });

  it("explains a missing decision source and clears only source fields on switch", async () => {
    const user = userEvent.setup();
    const mutate = rs.fn((_request: unknown) => ({}));
    mount(
      create(RouteSchema, {
        ...configured,
        jevDecision: create(JevDecisionConfigSchema, {
          ...configured.jevDecision!,
          decisionQuotaSignals: true,
          decisionQuotaTiers: create(JevQuotaTiersSchema, {
            moderate: 0,
            nearlyExhausted: 95.5,
          }),
        }),
      }),
      mutate,
    );
    await edit();
    await user.selectOptions(screen.getByLabelText("Decision source"), "route");
    expectInvalid("Decision route alias", "Choose a decision route alias.");
    expect(
      (screen.getByLabelText("Decision timeout (ms)") as HTMLInputElement)
        .value,
    ).toBe("8000");
    await user.selectOptions(
      screen.getByLabelText("Decision route alias"),
      "decision-alias",
    );
    await user.click(saveButton());
    const config = lastRequest(mutate).jevDecision!;
    expect(config).toMatchObject({
      decisionModel: "decision-alias",
      decisionConnectionId: "",
      decisionServiceModel: "",
      decisionTimeoutMs: 8000,
      sessionPinTtlSeconds: 120,
      decisionQuotaSignals: true,
    });
    expect(config.decisionQuotaTiers?.moderate).toBe(0);
    expect(config.decisionQuotaTiers?.limited).toBeUndefined();
    expect(config.decisionQuotaTiers?.nearlyExhausted).toBe(95.5);
  });

  it("quiets the required source error until the form changes", async () => {
    const user = userEvent.setup();
    mount(undefined, () => ({}));
    await screen.findByRole("heading", { name: "decision-alias" });
    await user.click(screen.getByRole("button", { name: "Create route" }));
    expect(saveButton().disabled).toBe(true);
    await user.selectOptions(
      screen.getByLabelText("Routing strategy"),
      String(RoutingStrategy.JEV),
    );
    expectInvalid("Decision connection", "Choose a decision connection.");
  });
});

describe("Advisory quota thresholds", () => {
  async function openQuota() {
    const user = userEvent.setup();
    const mutate = rs.fn((_request: unknown) => ({}));
    mount(configured, mutate);
    await edit();
    await user.click(screen.getByText("Advisory quota signals"));
    await user.click(screen.getByLabelText("Override quota thresholds"));
    return { user, mutate };
  }

  it("distinguishes an explicit zero moderate tier from omitted tiers", async () => {
    const { user, mutate } = await openQuota();
    expect(
      (screen.getByLabelText("Limited quota used (%)") as HTMLInputElement)
        .value,
    ).toBe("");
    setValue(screen.getByLabelText("Moderate quota used (%)"), "0");
    await user.click(saveButton());
    const tiers = lastRequest(mutate).jevDecision!.decisionQuotaTiers!;
    expect(tiers.moderate).toBe(0);
    expect(tiers.limited).toBeUndefined();
    expect(tiers.nearlyExhausted).toBeUndefined();
    expect(lastRequest(mutate).jevDecision!.decisionQuotaSignals).toBe(false);
  });

  it("serializes the present object with omitted thresholds when all are empty", async () => {
    const { user, mutate } = await openQuota();
    await user.click(saveButton());
    const tiers = lastRequest(mutate).jevDecision!.decisionQuotaTiers!;
    expect(tiers).toBeDefined();
    expect(tiers.moderate).toBeUndefined();
    expect(tiers.limited).toBeUndefined();
    expect(tiers.nearlyExhausted).toBeUndefined();
  });

  it.each([
    [
      "Moderate quota used (%)",
      "70",
      "Moderate must be below limited (effective 70%).",
    ],
    [
      "Limited quota used (%)",
      "90",
      "Limited must be below nearly exhausted (effective 90%).",
    ],
    [
      "Nearly exhausted quota used (%)",
      "70",
      "Nearly exhausted must be above limited (effective 70%).",
    ],
    ["Moderate quota used (%)", "-1", "Use a finite percentage from 0 to 100."],
    ["Limited quota used (%)", "101", "Use a finite percentage from 0 to 100."],
  ] as const)(
    "associates the %s = %s error while signals are off",
    async (label, value, message) => {
      const { mutate } = await openQuota();
      setValue(screen.getByLabelText(label), value);
      expectInvalid(label, message);
      await userEvent.click(saveButton());
      expect(mutate).not.toHaveBeenCalled();
    },
  );

  it("accepts ordered fractional thresholds", async () => {
    const { user, mutate } = await openQuota();
    setValue(screen.getByLabelText("Limited quota used (%)"), "69.5");
    setValue(screen.getByLabelText("Nearly exhausted quota used (%)"), "95.5");
    expect(saveButton().disabled).toBe(false);
    await user.click(saveButton());
    const tiers = lastRequest(mutate).jevDecision!.decisionQuotaTiers!;
    expect(tiers.limited).toBe(69.5);
    expect(tiers.nearlyExhausted).toBe(95.5);
  });

  it("opens the quota disclosure when a saved threshold is invalid", async () => {
    mount(
      create(RouteSchema, {
        ...configured,
        jevDecision: create(JevDecisionConfigSchema, {
          ...configured.jevDecision!,
          decisionQuotaTiers: create(JevQuotaTiersSchema, { moderate: 120 }),
        }),
      }),
      () => ({}),
    );
    await edit();
    const details = screen
      .getByText("Advisory quota signals")
      .closest("details")!;
    expect(details.open).toBe(true);
    expectInvalid(
      "Moderate quota used (%)",
      "Use a finite percentage from 0 to 100.",
    );
  });
});

describe("Reasoning retention group", () => {
  async function openRetention(retention?: ReasoningRetentionConfig) {
    const user = userEvent.setup();
    const mutate = rs.fn((_request: unknown) => ({}));
    mount(
      create(RouteSchema, { ...configured, reasoningRetention: retention }),
      mutate,
    );
    await edit();
    await user.click(screen.getByText("Reasoning retention"));
    return { user, mutate };
  }

  it.each([
    ["Context limit (%)", "101", true],
    ["Context limit (%)", "-1", true],
    ["Context limit (%)", "12.5", false],
    ["Context limit (%)", "100", false],
    ["Token limit", "4294967296", true],
    ["Token limit", "1.5", true],
    ["Token limit", "-1", true],
    ["Token limit", "4294967295", false],
  ] as const)(
    "validates %s = %s (invalid: %s) even while retention is disabled",
    async (label, value, invalid) => {
      const { mutate } = await openRetention(
        create(ReasoningRetentionConfigSchema, { enabled: false }),
      );
      setValue(screen.getByLabelText(label), value);
      if (invalid) {
        expectInvalid(
          label,
          label === "Token limit"
            ? "Token limit must be a whole number from 0 to 4,294,967,295."
            : "Context limit must be a percentage from 0 to 100.",
        );
        await userEvent.click(saveButton());
        expect(mutate).not.toHaveBeenCalled();
      } else {
        expect(saveButton().disabled).toBe(false);
      }
    },
  );

  it("preserves absent, explicit zero and disabled retention through editing", async () => {
    const { user, mutate } = await openRetention(
      create(ReasoningRetentionConfigSchema, {
        enabled: false,
        maxContextPercent: 0,
      }),
    );
    expect(
      (screen.getByLabelText("Context limit (%)") as HTMLInputElement).value,
    ).toBe("0");
    expect(
      (screen.getByLabelText("Token limit") as HTMLInputElement).value,
    ).toBe("");
    await user.click(saveButton());
    const retention = lastRequest(mutate).reasoningRetention!;
    expect(retention.enabled).toBe(false);
    expect(retention.maxContextPercent).toBe(0);
    expect(retention.maxTokens).toBeUndefined();
  });

  it("opens the disclosure on an invalid saved limit and links the summary to it", async () => {
    mount(
      create(RouteSchema, {
        ...configured,
        reasoningRetention: create(ReasoningRetentionConfigSchema, {
          maxTokens: 4294967295,
          maxContextPercent: 500,
        }),
      }),
      () => ({}),
    );
    await edit();
    expect(
      screen.getByText("Reasoning retention").closest("details")!.open,
    ).toBe(true);
    await userEvent.click(
      within(screen.getByTestId("error-summary")).getByText(
        "Context limit must be a percentage from 0 to 100.",
      ),
    );
    expect(document.activeElement).toBe(
      screen.getByLabelText("Context limit (%)"),
    );
  });

  it("reopens a collapsed disclosure when its error link is followed", async () => {
    const user = userEvent.setup();
    mount(
      create(RouteSchema, {
        ...configured,
        reasoningRetention: create(ReasoningRetentionConfigSchema, {
          maxTokens: 4294967295,
          maxContextPercent: 500,
        }),
      }),
      () => ({}),
    );
    await edit();
    const details = screen.getByText("Reasoning retention").closest("details")!;
    expect(details.open).toBe(true);
    await user.click(screen.getByText("Reasoning retention"));
    await waitFor(() => expect(details.open).toBe(false));
    await user.click(
      within(screen.getByTestId("error-summary")).getByText(
        "Context limit must be a percentage from 0 to 100.",
      ),
    );
    await waitFor(() => expect(details.open).toBe(true));
    expect(document.activeElement).toBe(
      screen.getByLabelText("Context limit (%)"),
    );
  });

  it("keeps decision, quota and retention drafts when the server rejects the save", async () => {
    const user = userEvent.setup();
    const mutate = rs.fn((_request: unknown) => {
      throw new ConnectError("Rejected", Code.InvalidArgument);
    });
    mount(
      create(RouteSchema, {
        ...configured,
        jevDecision: create(JevDecisionConfigSchema, {
          ...configured.jevDecision!,
          decisionQuotaTiers: create(JevQuotaTiersSchema, { moderate: 0 }),
        }),
        reasoningRetention: create(ReasoningRetentionConfigSchema, {
          enabled: false,
          maxTokens: 0,
        }),
      }),
      mutate,
    );
    await edit();
    await user.click(saveButton());
    await screen.findAllByRole("alert");
    expect(screen.getByRole("dialog", { name: "Edit routing" })).toBeTruthy();
    expect(
      (screen.getByLabelText("Decision timeout (ms)") as HTMLInputElement)
        .value,
    ).toBe("8000");
    expect(
      (screen.getByLabelText("Moderate quota used (%)") as HTMLInputElement)
        .value,
    ).toBe("0");
    expect(
      (screen.getByLabelText("Token limit") as HTMLInputElement).value,
    ).toBe("0");
    expect(saveButton().disabled).toBe(false);
  });
});

// SPDX-License-Identifier: Apache-2.0
import { render, screen, within } from "@testing-library/react";
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
  type ReasoningRetentionConfig,
  ReasoningRetentionConfigSchema,
  RoutingStrategy,
  ConnectionState,
  OrganizationSchema,
  GetSessionResponseSchema,
  type Route,
} from "@aiprouter/client";
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
      name: "Edit route",
    }),
  );
}
describe("JEV routing payloads", () => {
  it.each(["preserve", "zero", "omit-limits", "remove"] as const)(
    "retention edit %s preserves generated payload presence",
    async (mode) => {
      const user = userEvent.setup(),
        mutate = rs.fn((_request: unknown) => ({}));
      mount(
        create(RouteSchema, {
          ...configured,
          reasoningRetention: create(ReasoningRetentionConfigSchema, {
            enabled: false,
            maxContextPercent: 12.5,
            maxTokens: 500,
          }),
        }),
        mutate,
      );
      await edit();
      await user.click(screen.getByText("Reasoning retention"));
      if (mode === "remove")
        await user.click(
          screen.getByLabelText("Configure reasoning retention"),
        );
      if (mode === "zero" || mode === "omit-limits") {
        for (const label of ["Context limit (%)", "Token limit"]) {
          await user.clear(screen.getByLabelText(label));
          if (mode === "zero")
            await user.type(screen.getByLabelText(label), "0");
        }
      }
      await user.click(screen.getByRole("button", { name: "Save routing" }));
      const request = mutate.mock.calls[0]![0] as {
        reasoningRetention?: ReasoningRetentionConfig;
      };
      if (mode === "remove") expect(request.reasoningRetention).toBeUndefined();
      else {
        expect(request.reasoningRetention?.enabled).toBe(false);
        expect(request.reasoningRetention?.maxContextPercent).toBe(
          mode === "zero" ? 0 : mode === "omit-limits" ? undefined : 12.5,
        );
        expect(request.reasoningRetention?.maxTokens).toBe(
          mode === "zero" ? 0 : mode === "omit-limits" ? undefined : 500,
        );
      }
    },
  );
  it("creates enabled retention with default limits and validates numeric bounds", async () => {
    const user = userEvent.setup(),
      mutate = rs.fn((_request: unknown) => ({}));
    mount(undefined, mutate);
    await screen.findByRole("heading", { name: "decision-alias" });
    await user.click(screen.getByRole("button", { name: "Create route" }));
    await user.type(screen.getByLabelText("Model alias"), "retained");
    await user.click(screen.getByRole("button", { name: "Add target" }));
    await user.selectOptions(
      screen.getByLabelText("Connection or pool"),
      "connectionId:target",
    );
    await user.type(screen.getByLabelText("Provider model ID"), "model");
    await user.click(screen.getByText("Reasoning retention"));
    await user.click(screen.getByLabelText("Configure reasoning retention"));
    await user.click(
      screen.getByLabelText("Retain readable reasoning during compaction"),
    );
    for (const [label, invalid] of [
      ["Context limit (%)", "101"],
      ["Token limit", "4294967296"],
      ["Token limit", "1.5"],
    ]) {
      await user.type(screen.getByLabelText(label!), invalid!);
      expect(
        (
          screen.getByRole("button", {
            name: "Save routing",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true);
      await user.clear(screen.getByLabelText(label!));
    }
    await user.click(screen.getByRole("button", { name: "Save routing" }));
    expect(mutate.mock.calls[0]![0]).toMatchObject({
      reasoningRetention: { enabled: true },
    });
    const config = (
      mutate.mock.calls[0]![0] as {
        reasoningRetention: ReasoningRetentionConfig;
      }
    ).reasoningRetention;
    expect(config.maxContextPercent).toBeUndefined();
    expect(config.maxTokens).toBeUndefined();
  });

  it("validates effective quota order while signals are off and allows explicit override removal", async () => {
    const user = userEvent.setup(),
      mutate = rs.fn((_request: unknown) => ({}));
    mount(configured, mutate);
    await edit();
    await user.click(screen.getByText("Advisory quota signals"));
    await user.click(screen.getByLabelText("Override quota thresholds"));
    await user.type(screen.getByLabelText("Moderate quota used (%)"), "70");
    expect(
      screen
        .getByRole("button", { name: "Save routing" })
        .hasAttribute("disabled"),
    ).toBe(true);
    await user.clear(screen.getByLabelText("Moderate quota used (%)"));
    await user.type(screen.getByLabelText("Limited quota used (%)"), "90");
    expect(
      screen
        .getByRole("button", { name: "Save routing" })
        .hasAttribute("disabled"),
    ).toBe(true);
    await user.click(screen.getByLabelText("Override quota thresholds"));
    await user.click(screen.getByRole("button", { name: "Save routing" }));
    const request = mutate.mock.calls[0]?.[0] as {
      jevDecision: {
        decisionQuotaSignals: boolean;
        decisionQuotaTiers?: unknown;
      };
    };
    expect(request.jevDecision.decisionQuotaSignals).toBe(false);
    expect(request.jevDecision.decisionQuotaTiers).toBeUndefined();
  });
  it.each([NaN, Infinity, -1, 101])(
    "rejects invalid saved quota threshold %s until repaired",
    async (value) => {
      const mutate = rs.fn((_request: unknown) => ({}));
      mount(
        create(RouteSchema, {
          ...configured,
          jevDecision: create(JevDecisionConfigSchema, {
            ...configured.jevDecision!,
            decisionQuotaTiers: create(JevQuotaTiersSchema, {
              moderate: value,
            }),
          }),
        }),
        mutate,
      );
      await edit();
      expect(
        screen
          .getByRole("button", { name: "Save routing" })
          .hasAttribute("disabled"),
      ).toBe(true);
    },
  );

  it.each([true, false])(
    "preserves existing optional quota thresholds with signals %s on full route replacement",
    async (signals) => {
      const mutate = rs.fn((_request: unknown) => ({}));
      const route = create(RouteSchema, {
        ...configured,
        jevDecision: create(JevDecisionConfigSchema, {
          ...configured.jevDecision!,
          decisionQuotaSignals: signals,
          decisionQuotaTiers: create(JevQuotaTiersSchema, {
            moderate: 0,
            nearlyExhausted: 95,
          }),
        }),
      });
      mount(route, mutate);
      await edit();
      await userEvent.click(
        screen.getByRole("button", { name: "Save routing" }),
      );
      expect(mutate.mock.calls[0]?.[0]).toMatchObject({
        jevDecision: { ...route.jevDecision! },
        targets: [
          {
            reasoningEfforts: ["high", "max"],
            modelProfile: "Careful reasoning",
          },
        ],
      });
      const request = mutate.mock.calls[0]?.[0] as {
        jevDecision: {
          decisionQuotaTiers: {
            moderate?: number;
            limited?: number;
            nearlyExhausted?: number;
          };
        };
      };
      expect(request.jevDecision.decisionQuotaTiers.moderate).toBe(0);
      expect(request.jevDecision.decisionQuotaTiers.limited).toBeUndefined();
    },
  );

  it("creates explicit personal decision configuration and target hints with bounds", async () => {
    const user = userEvent.setup(),
      mutate = rs.fn((_request: unknown) => ({}));
    mount(undefined, mutate);
    await user.click(screen.getByRole("button", { name: "Create route" }));
    await user.type(screen.getByLabelText("Model alias"), "new-smart");
    await user.selectOptions(
      screen.getByLabelText("Routing strategy"),
      String(RoutingStrategy.JEV),
    );
    await user.selectOptions(
      screen.getByLabelText("Decision connection"),
      "decision",
    );
    await user.click(screen.getByText("Advisory quota signals"));
    await user.click(screen.getByLabelText("Enable advisory quota signals"));
    await user.click(screen.getByLabelText("Override quota thresholds"));
    await user.type(screen.getByLabelText("Moderate quota used (%)"), "0");
    await user.type(
      screen.getByLabelText("Nearly exhausted quota used (%)"),
      "95.5",
    );
    await user.type(
      screen.getByLabelText("Decision service model"),
      "custom-judge",
    );
    await user.clear(screen.getByLabelText("Decision timeout (ms)"));
    await user.type(screen.getByLabelText("Decision timeout (ms)"), "999");
    expect(
      screen
        .getByRole("button", { name: "Save routing" })
        .hasAttribute("disabled"),
    ).toBe(true);
    await user.clear(screen.getByLabelText("Decision timeout (ms)"));
    await user.type(screen.getByLabelText("Decision timeout (ms)"), "1000");
    await user.clear(screen.getByLabelText("Session pin duration (seconds)"));
    await user.type(
      screen.getByLabelText("Session pin duration (seconds)"),
      "60",
    );
    await user.click(screen.getByRole("button", { name: "Add target" }));
    await user.selectOptions(
      screen.getByLabelText("Connection or pool"),
      "connectionId:target",
    );
    await user.type(screen.getByLabelText("Provider model ID"), "model");
    await user.click(screen.getByText("Target reasoning and model profile"));
    await user.click(screen.getByLabelText("ultra"));
    await user.type(screen.getByLabelText("Model profile"), "Complex coding");
    await user.click(screen.getByRole("button", { name: "Save routing" }));
    expect(mutate.mock.calls[0]?.[0]).toMatchObject({
      organizationId: "org",
      modelId: "new-smart",
      strategy: RoutingStrategy.JEV,
      jevDecision: {
        decisionConnectionId: "decision",
        decisionQuotaSignals: true,
        decisionQuotaTiers: { moderate: 0, nearlyExhausted: 95.5 },
        decisionModel: "",
        decisionServiceModel: "custom-judge",
        decisionTimeoutMs: 1000,
        sessionPinTtlSeconds: 60,
      },
      targets: [
        {
          target: { case: "connectionId", value: "target" },
          reasoningEfforts: ["ultra"],
          modelProfile: "Complex coding",
        },
      ],
    });
  });
  it("preserves existing decision config, target hints and profiles on edit", async () => {
    const mutate = rs.fn((_request: unknown) => ({}));
    mount(configured, mutate);
    await edit();
    await userEvent.click(screen.getByRole("button", { name: "Save routing" }));
    expect(mutate.mock.calls[0]?.[0]).toMatchObject({
      routeId: "configured",
      jevDecision: configured.jevDecision,
      targets: [
        {
          reasoningEfforts: ["high", "max"],
          modelProfile: "Careful reasoning",
        },
      ],
    });
  });
  it("omits decision config when replacing a JEV route with an ordinary strategy", async () => {
    const user = userEvent.setup(),
      mutate = rs.fn((_request: unknown) => ({}));
    mount(configured, mutate);
    await edit();
    await user.selectOptions(
      screen.getByLabelText("Routing strategy"),
      String(RoutingStrategy.DIRECT),
    );
    expect(screen.queryByLabelText("Decision connection")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Save routing" }));
    const request = mutate.mock.calls[0]?.[0] as { jevDecision?: unknown };
    expect(request.jevDecision).toBeUndefined();
    expect(request).toMatchObject({
      strategy: RoutingStrategy.DIRECT,
      targets: [
        {
          reasoningEfforts: ["high", "max"],
          modelProfile: "Careful reasoning",
        },
      ],
    });
  });
  it("clears stale decision fields when changing source and preserves a server-rejected draft", async () => {
    const user = userEvent.setup(),
      mutate = rs.fn((_request: unknown) => {
        throw new ConnectError(
          "decision route chain rejected",
          Code.FailedPrecondition,
        );
      });
    mount(configured, mutate);
    await edit();
    await user.selectOptions(screen.getByLabelText("Decision source"), "route");
    await user.selectOptions(
      screen.getByLabelText("Decision route alias"),
      "decision-alias",
    );
    await user.click(screen.getByRole("button", { name: "Save routing" }));
    await screen.findAllByRole("alert");
    expect(
      (screen.getByLabelText("Decision route alias") as HTMLSelectElement)
        .value,
    ).toBe("decision-alias");
    expect(mutate.mock.calls[0]?.[0]).toMatchObject({
      jevDecision: {
        decisionModel: "decision-alias",
        decisionConnectionId: "",
        decisionServiceModel: "",
        decisionTimeoutMs: 8000,
        sessionPinTtlSeconds: 120,
      },
    });
    expect(screen.getByRole("dialog")).toBeTruthy();
  });
  it("does not restore invalid decision config when toggling away and back to JEV", async () => {
    const user = userEvent.setup(),
      mutate = rs.fn((_request: unknown) => ({}));
    mount(configured, mutate);
    await edit();
    await user.selectOptions(
      screen.getByLabelText("Routing strategy"),
      String(RoutingStrategy.FALLBACK),
    );
    await user.selectOptions(
      screen.getByLabelText("Routing strategy"),
      String(RoutingStrategy.JEV),
    );
    expect(
      (screen.getByLabelText("Decision connection") as HTMLSelectElement).value,
    ).toBe("");
    expect(
      (screen.getByLabelText("Decision timeout (ms)") as HTMLInputElement)
        .value,
    ).toBe("0");
    expect(
      screen
        .getByRole("button", { name: "Save routing" })
        .hasAttribute("disabled"),
    ).toBe(true);
    expect(mutate).not.toHaveBeenCalled();
  });
});

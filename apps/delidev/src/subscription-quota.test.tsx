// SPDX-License-Identifier: Apache-2.0
import { useState } from "react";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { TransportProvider } from "@connectrpc/connect-query";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { expect, it, vi } from "vitest";
import {
  ResourceSchema,
  EntityKind,
  SubscriptionService,
  SystemService,
  SystemCapability,
  newRequestId,
} from "@delinoio/delidev-api-client";
import { document, encode } from "./documents";
import { i18n, copy } from "./localization";
import { MutationIntents } from "./mutation";
import { SubscriptionQuotaControls } from "./subscription-quota";

function fixture(
  details: unknown = [
    { id: "credit_1", reset_type: "codexRateLimits", status: "available" },
  ],
  lease?: { action: string; machine_id: string },
  // Retained positional slot: a preferred Runner no longer selects any lane.
  _preferred = "",
  server = false,
  supported = true,
  phase = "",
  serverCredits = false,
  creditPhase = "",
  cleanup = false,
  workerUncertain = false,
  inventoryFields: Record<string, unknown> = {},
  quotaError = "",
  automaticConsent = false,
  automaticPhase = "uncertain",
  creditsV2 = true,
  savedQuota: Record<string, unknown> = {},
  quotaCapability = true,
) {
  const machine = newRequestId(),
    connection = newRequestId(),
    generation = newRequestId(),
    creditGeneration = newRequestId(),
    inventory = newRequestId();
  const data = {
    alias: "Quota fixture",
    type: "subscription",
    subscription_service: "chatgpt",
    health: "ready",
    recovery_notifications: false,
    connection: { id: connection },
    subscription: {
      automatic_credit_consent: automaticConsent
        ? { connection_id: connection, generation }
        : undefined,
      observation: workerUncertain
        ? { id: newRequestId(), action: "reset-credit", phase: automaticPhase }
        : undefined,
      generation,
      ...savedQuota,
      owner_machine_id: server ? "" : machine,
      server_quota_generation: server ? generation : undefined,
      server_credit: creditPhase
        ? {
            id: newRequestId(),
            // The retained captured generation differs from the live one after
            // an Execute rotation.
            generation: creditGeneration,
            phase: creditPhase,
            cleanup_confirmed: cleanup,
            outcome: "",
          }
        : undefined,
      server_quota: phase
        ? { id: newRequestId(), phase, error_code: quotaError }
        : undefined,
      lease,
      reset_credits: {
        observation_id: inventory,
        observed_at: new Date().toISOString(),
        available_count: "2",
        credits: details,
        ...inventoryFields,
      },
    },
  };
  let account = create(ResourceSchema, {
    id: newRequestId(),
    kind: EntityKind.ACCOUNT,
    revision: 1n,
    schemaVersion: 2,
    documentJson: encode(data),
  });
  const request = vi.fn(async (value) => ({
    account,
    operationId: value.mutation?.requestId,
  }));
  const reconcile = vi.fn(async (_value: unknown) => ({ account }));
  const consent = vi.fn(async (value) => {
    account = create(ResourceSchema, {
      ...account,
      revision: account.revision + 1n,
      documentJson: encode({
        ...data,
        subscription: {
          ...data.subscription,
          automatic_credit_consent: value.enabled
            ? {
                connection_id: value.connectionId,
                generation: value.generationId,
              }
            : undefined,
        },
      }),
    });
    return { account };
  });
  const status = vi.fn();
  const transport = createRouterTransport((router) => {
    router.service(SystemService, {
      getStatus: () => {
        status();
        return {
          capabilities: [
            SystemCapability.AUTOMATIC_RESET_CREDIT_CONSENT_V1,
            SystemCapability.SUBSCRIPTION_RESET_CREDITS_V1,
            ...(serverCredits
              ? [SystemCapability.SERVER_SUBSCRIPTION_RESET_CREDITS_V1]
              : []),
            ...(creditsV2
              ? [SystemCapability.SERVER_SUBSCRIPTION_RESET_CREDITS_V2]
              : []),
            ...(!quotaCapability
              ? []
              : supported
                ? [SystemCapability.SERVER_SUBSCRIPTION_QUOTA_V2]
                : [SystemCapability.SUBSCRIPTION_QUOTA_V1]),
          ],
        };
      },
    });
    router.service(SubscriptionService, {
      requestSubscriptionObservation: request,
      reconcileSubscriptionCredit: reconcile,
      setAutomaticResetCreditConsent: consent,
    });
  });
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  function Harness() {
    const [current, setCurrent] = useState(account),
      [, setBusy] = useState(false),
      [visible, setVisible] = useState(true);
    return (
      <QueryClientProvider client={queryClient}>
        <TransportProvider transport={transport}>
          <MutationIntents>
            <SubscriptionQuotaControls
              current={current}
              active
              accepted={setCurrent}
              busyChanged={setBusy}
              visible={visible}
              footer={(refresh) => (
                <button disabled={!refresh} onClick={() => void refresh?.()}>
                  Refresh account status and quota
                </button>
              )}
            />
            <button onClick={() => setVisible(false)}>
              Disconnect fixture account
            </button>
            <button onClick={() => setVisible(true)}>
              Reconnect fixture account
            </button>
            <button
              onClick={() => {
                account = create(ResourceSchema, {
                  ...account,
                  revision: account.revision + 1n,
                  documentJson: encode({
                    ...data,
                    padding: "x".repeat(1 << 20),
                  }),
                });
                setCurrent(account);
              }}
            >
              Oversize fixture document
            </button>
            <button
              onClick={() => {
                // The owner keeps `visible` true while a request is in flight,
                // so a poll can report the disconnection with `visible` unchanged.
                account = create(ResourceSchema, {
                  ...account,
                  revision: account.revision + 1n,
                  documentJson: encode({ ...data, connection: undefined }),
                });
                setCurrent(account);
              }}
            >
              Drop fixture connection
            </button>
            <button
              onClick={() => {
                account = create(ResourceSchema, {
                  ...account,
                  revision: account.revision + 1n,
                  documentJson: encode({
                    ...data,
                    subscription: {
                      ...data.subscription,
                      reset_credits: {
                        ...data.subscription.reset_credits,
                        credits: Array.isArray(details)
                          ? [...details].reverse()
                          : details,
                      },
                    },
                  }),
                });
                setCurrent(account);
              }}
            >
              Reorder fixture credits
            </button>
            <button
              onClick={() => {
                account = create(ResourceSchema, {
                  ...account,
                  revision: account.revision + 1n,
                });
                setCurrent(account);
              }}
            >
              Change fixture account revision
            </button>
            <button
              onClick={() => {
                account = create(ResourceSchema, {
                  ...account,
                  revision: account.revision + 1n,
                  documentJson: encode({
                    ...data,
                    subscription: {
                      ...data.subscription,
                      reset_credits: {
                        ...data.subscription.reset_credits,
                        available_count: "0",
                      },
                    },
                  }),
                });
                setCurrent(account);
              }}
            >
              Remove fixture credits
            </button>
            <button
              onClick={() => {
                account = create(ResourceSchema, {
                  ...account,
                  revision: account.revision + 1n,
                  documentJson: encode({
                    ...data,
                    subscription: {
                      ...data.subscription,
                      reset_credits: {
                        ...data.subscription.reset_credits,
                        credits: [
                          {
                            id: "credit_2",
                            reset_type: "codexRateLimits",
                            status: "available",
                          },
                        ],
                      },
                    },
                  }),
                });
                setCurrent(account);
              }}
            >
              Replace fixture credit
            </button>
          </MutationIntents>
        </TransportProvider>
      </QueryClientProvider>
    );
  }
  return {
    Harness,
    request,
    reconcile,
    consent,
    status,
    account,
    machine,
    connection,
    generation,
    creditGeneration,
    inventory,
  };
}

async function selectExactCredit() {
  const use = await screen.findByRole("button", { name: "Use" });
  await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(use);
  fireEvent.click(
    screen.getByRole("button", { name: "Select Reset credit 1" }),
  );
}

it("preserves authoritative count, confirms the selected credit and retains the exact lost request", async () => {
  const value = fixture();
  value.request.mockRejectedValueOnce(
    new ConnectError("lost acknowledgment", Code.Unavailable),
  );
  render(<value.Harness />);
  await selectExactCredit();
  expect(screen.getByText("2 available reset credits")).toBeTruthy();
  expect(screen.getByText("1 returned details")).toBeTruthy();
  expect(value.request).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Consume reset credit" }));
  fireEvent.click(
    await screen.findByRole("button", {
      name: "Retry original quota or credit request",
    }),
  );
  await waitFor(() => expect(value.request).toHaveBeenCalledTimes(2));
  expect(value.request.mock.calls[1][0]).toEqual(
    value.request.mock.calls[0][0],
  );
  expect(value.request.mock.calls[0][0]).toMatchObject({
    creditId: "credit_1",
    nextCredit: false,
    confirmed: true,
    connectionId: value.connection,
    generationId: value.generation,
    creditsObservationId: value.inventory,
  });
});

it("requires an explicit count-only next-credit confirmation and fences stale revisions", async () => {
  const value = fixture(null);
  render(<value.Harness />);
  const use = await screen.findByRole("button", { name: "Use" });
  await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(use);
  fireEvent.click(
    screen.getByRole("button", { name: "Change fixture account revision" }),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Consume reset credit",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(value.request).not.toHaveBeenCalled();
  expect(document(value.account).recovery_notifications).toBe(false);
});

it("explicit quota refresh does not consume credits or refresh authentication", async () => {
  const value = fixture();
  render(<value.Harness />);
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Refresh account status and quota",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh account status and quota" }),
  );
  await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
  expect(value.request.mock.calls[0][0]).toMatchObject({
    creditId: "",
    nextCredit: false,
    confirmed: false,
  });
  expect(value.reconcile).not.toHaveBeenCalled();
});

it("refreshes detail quota on the server during execution despite a different selected device", async () => {
  const runner = newRequestId(),
    value = fixture(
      undefined,
      { action: "execute", machine_id: runner },
      newRequestId(),
    );
  render(<value.Harness />);
  await waitFor(() =>
    expect(
      (
        screen.getByRole("button", {
          name: "Refresh account status and quota",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Refresh account status and quota" }),
  );
  await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
  expect(value.request.mock.calls[0]?.[0]).toMatchObject({ machineId: "" });
});
it("blocks detail quota refresh while a different native lease kind owns the account", async () => {
  const value = fixture(
    undefined,
    { action: "refresh", machine_id: newRequestId() },
    newRequestId(),
  );
  render(<value.Harness />);
  await screen.findByText(/Last successful observation/);
  expect(
    (
      screen.getByRole("button", {
        name: "Refresh account status and quota",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(value.request).not.toHaveBeenCalled();
});

it.each([true, false])(
  "negotiates server quota without a Runner Device (supported: %s)",
  async (supported) => {
    const value = fixture(
      undefined,
      undefined,
      "",
      true,
      supported,
      "",
      false,
      "",
      false,
      false,
      {},
      "",
      false,
      "uncertain",
      false,
    );
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    const button = screen.getByRole("button", {
      name: "Refresh account status and quota",
    }) as HTMLButtonElement;
    await waitFor(() => expect(button.disabled).toBe(!supported));
    fireEvent.click(button);
    if (supported) {
      await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
      expect(value.request.mock.calls[0][0]).toMatchObject({
        machineId: "",
        connectionId: value.connection,
        generationId: value.generation,
        mutation: { id: value.account.id, expectedRevision: 1n },
      });
    } else {
      expect(value.request).not.toHaveBeenCalled();
    }
    expect(
      (screen.getByRole("button", { name: "Use" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
  },
);
it.each(["queued", "sending", "uncertain"])(
  "retains the server quota %s fence in detail controls",
  async (phase) => {
    const value = fixture(undefined, undefined, "", true, true, phase);
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    expect(
      (
        screen.getByRole("button", {
          name: "Refresh account status and quota",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(value.request).not.toHaveBeenCalled();
  },
);

it.each([undefined, null])(
  "confirms server reset credits with the original omitted-machine selector",
  async (details) => {
    const value = fixture(details, undefined, "", true, true, "", true);
    render(<value.Harness />);
    const next = details === null;
    const review = await screen.findByRole("button", { name: "Use" });
    await waitFor(() =>
      expect((review as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(review);
    if (!next)
      fireEvent.click(
        screen.getByRole("button", { name: "Select Reset credit 1" }),
      );
    fireEvent.click(
      screen.getByRole("button", { name: "Consume reset credit" }),
    );
    await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
    expect(value.request.mock.calls[0][0]).toMatchObject({
      machineId: "",
      creditId: next ? "" : "credit_1",
      nextCredit: next,
      confirmed: true,
      connectionId: value.connection,
      generationId: value.generation,
      creditsObservationId: value.inventory,
    });
  },
);
it.each(["queued", "sending", "uncertain"])(
  "server credit %s blocks competing refresh and consumption",
  async (phase) => {
    const value = fixture(
      undefined,
      undefined,
      "",
      true,
      true,
      "",
      true,
      phase,
    );
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    expect(
      (
        screen.getByRole("button", {
          name: "Refresh account status and quota",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
    expect(
      (screen.getByRole("button", { name: "Use" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(value.request).not.toHaveBeenCalled();
  },
);
it.each([false, true])(
  "server credit reconciliation requires independently confirmed cleanup (%s)",
  async (cleanup) => {
    const value = fixture(
      undefined,
      undefined,
      "",
      true,
      true,
      "",
      true,
      "uncertain",
      cleanup,
    );
    render(<value.Harness />);
    const reconcile = await screen.findByRole("button", {
      name: "Check original operation result",
    });
    await waitFor(() =>
      expect((reconcile as HTMLButtonElement).disabled).toBe(!cleanup),
    );
    expect(
      screen
        .getByRole("region", { name: "Results and recovery" })
        .contains(reconcile),
    ).toBe(true);
    expect(
      screen.getByRole("heading", {
        name: "The consumption result could not be confirmed",
      }),
    ).toBeTruthy();
    if (!cleanup)
      expect(
        screen.getByText(
          "You can check the result after the original operation finishes cleanup.",
        ),
      ).toBeTruthy();
    expect(
      screen.getByText(
        "Reset credit consumption and quota recovery are verified separately.",
      ),
    ).toBeTruthy();
    fireEvent.click(reconcile);
    if (cleanup) {
      await waitFor(() => expect(value.reconcile).toHaveBeenCalledTimes(1));
      expect(value.reconcile.mock.calls[0][0]).toMatchObject({
        connectionId: value.connection,
        generationId: value.creditGeneration,
      });
    } else {
      expect(value.reconcile).not.toHaveBeenCalled();
    }
  },
);

it("preserves the server confirmation selectors in Korean", async () => {
  await i18n.changeLanguage("ko");
  try {
    const value = fixture(undefined, undefined, "", true, true, "", true);
    render(<value.Harness />);
    const use = await screen.findByRole("button", { name: "사용하기" });
    await waitFor(() =>
      expect((use as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(use);
    const review = await screen.findByRole("button", { name: "리셋권 1 선택" });
    await waitFor(() =>
      expect((review as HTMLButtonElement).disabled).toBe(false),
    );
    fireEvent.click(review);
    fireEvent.click(
      screen.getByRole("button", {
        name: copy("subscription-quota.confirmCreditConsumption_251822"),
      }),
    );
    await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
    expect(value.request.mock.calls[0][0]).toMatchObject({
      machineId: "",
      creditId: "credit_1",
      confirmed: true,
      connectionId: value.connection,
      generationId: value.generation,
      creditsObservationId: value.inventory,
    });
  } finally {
    await i18n.changeLanguage("en");
  }
});

it("retains original Worker reconciliation after a terminal server-credit history", async () => {
  const value = fixture(
    undefined,
    undefined,
    "",
    false,
    true,
    "",
    true,
    "succeeded",
    true,
    true,
  );
  render(<value.Harness />);
  const button = await screen.findByRole("button", {
    name: "Check original operation result",
  });
  await waitFor(() =>
    expect((button as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(button);
  await waitFor(() => expect(value.reconcile).toHaveBeenCalledTimes(1));
  const state = document(value.account).subscription as Record<string, unknown>;
  expect(value.reconcile.mock.calls[0][0]).toMatchObject({
    operationId: (state.observation as Record<string, unknown>).id,
  });
});

it("starts collapsed, uses authoritative count, and restores selection focus without sending", async () => {
  const value = fixture();
  render(<value.Harness />);
  const use = await screen.findByRole("button", { name: "Use" });
  await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
  expect(
    screen
      .getByRole("button", { name: "View details" })
      .getAttribute("aria-expanded"),
  ).toBe("false");
  expect(
    screen.queryByRole("button", { name: "Select Reset credit 1" }),
  ).toBeNull();
  fireEvent.click(use);
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("heading", { name: "Credit details" }),
  );
  const select = screen.getByRole("button", { name: "Select Reset credit 1" });
  fireEvent.click(select);
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("heading", { name: "Use a reset credit?" }),
  );
  expect(screen.getByText("Quota fixture")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(globalThis.document.activeElement).toBe(select);
  expect(value.request).not.toHaveBeenCalled();
});
it("retains original confirmation outside collapsed details and restores section fallback focus", async () => {
  const value = fixture();
  render(<value.Harness />);
  await selectExactCredit();
  fireEvent.click(screen.getByRole("button", { name: "Hide details" }));
  expect(
    screen.getByRole("button", { name: "Consume reset credit" }),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("heading", { name: "Reset credits" }),
  );
  expect(value.request).not.toHaveBeenCalled();
});
it.each([
  {},
  "invalid",
  [
    {
      id: "expired",
      reset_type: "codexRateLimits",
      status: "available",
      expires_at: "2020-01-01T00:00:00Z",
    },
  ],
])(
  "never invents next-credit selection for malformed or ineligible details",
  async (details) => {
    const value = fixture(details);
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    expect(
      (screen.getByRole("button", { name: "Use" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "View details" }));
    expect(
      screen.queryByRole("button", { name: "Consume reset credit" }),
    ).toBeNull();
    expect(value.request).not.toHaveBeenCalled();
  },
);

it.each([
  { available_count: "0" },
  { available_count: "not-a-count" },
  { observed_at: "2020-01-01T00:00:00Z" },
  { observed_at: "2999-01-01T00:00:00Z" },
])(
  "keeps unknown, zero and stale inventory disabled with visible guidance",
  async (fields) => {
    const value = fixture(
      undefined,
      undefined,
      "",
      false,
      true,
      "",
      false,
      "",
      false,
      false,
      fields,
    );
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    expect(
      (screen.getByRole("button", { name: "Use" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    expect(
      screen.getByText(
        fields.available_count === "0"
          ? "No reset credits are available."
          : fields.available_count
            ? "Available credit count unknown"
            : "Refresh the account to review a current credit inventory.",
        { selector: "p" },
      ),
    ).toBeTruthy();
    expect(value.request).not.toHaveBeenCalled();
  },
);
it("locale changes and collapsed details retain the original exact selection without sending", async () => {
  const value = fixture();
  render(<value.Harness />);
  await selectExactCredit();
  fireEvent.click(screen.getByRole("button", { name: "Hide details" }));
  try {
    await i18n.changeLanguage("ko");
    expect(
      await screen.findByRole("button", { name: "리셋권 사용" }),
    ).toBeTruthy();
    expect(
      within(
        globalThis.document.querySelector<HTMLElement>(
          ".reset-credit-confirmation",
        )!,
      ).getByText("리셋권 1"),
    ).toBeTruthy();
    expect(value.request).not.toHaveBeenCalled();
  } finally {
    await i18n.changeLanguage("en");
  }
  expect(
    screen.getByRole("button", { name: "Consume reset credit" }),
  ).toBeTruthy();
});

it("restores the section heading when background inventory disables the original selection opener", async () => {
  const value = fixture();
  render(<value.Harness />);
  await selectExactCredit();
  fireEvent.click(
    screen.getByRole("button", { name: "Remove fixture credits" }),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Select Reset credit 1",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("heading", { name: "Reset credits" }),
  );
  expect(value.request).not.toHaveBeenCalled();
});
it("restores the section heading instead of an inert original selection opener", async () => {
  const value = fixture();
  render(<value.Harness />);
  await selectExactCredit();
  screen
    .getByRole("button", { name: "Select Reset credit 1" })
    .closest(".reset-credit-rows")!
    .setAttribute("inert", "");
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("heading", { name: "Reset credits" }),
  );
  expect(value.request).not.toHaveBeenCalled();
});

it("renders expiry only for a valid supplied RFC3339 timestamp", async () => {
  const value = fixture([
    {
      id: "credit-bad-date",
      reset_type: "codexRateLimits",
      status: "available",
      expires_at: "2030",
    },
    {
      id: "credit-valid-date",
      reset_type: "codexRateLimits",
      status: "available",
      expires_at: "2030-10-31T00:00:00Z",
    },
  ]);
  render(<value.Harness />);
  const use = await screen.findByRole("button", { name: "Use" });
  await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(use);
  expect(
    globalThis.document.querySelectorAll(".reset-credit-row time").length,
  ).toBe(1);
  expect(
    globalThis.document
      .querySelector(".reset-credit-row time")
      ?.getAttribute("datetime"),
  ).toBe("2030-10-31T00:00:00Z");
  expect(value.request).not.toHaveBeenCalled();
});
it.each(["unsupported", "unavailable"])(
  "shows selected-server quota troubleshooting for %s without Worker fallback",
  async (code) => {
    const value = fixture(
      undefined,
      undefined,
      "",
      false,
      true,
      "failed",
      false,
      "",
      false,
      false,
      {},
      code,
    );
    render(<value.Harness />);
    await screen.findByText(
      code === "unsupported"
        ? /Check the selected server’s Codex installation/
        : /Check the selected server connection/,
    );
    expect(value.request).not.toHaveBeenCalled();
    fireEvent.click(
      screen.getByRole("button", { name: "Refresh account status and quota" }),
    );
    await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
    expect(value.request.mock.calls[0][0]).toMatchObject({ machineId: "" });
  },
);

it.each(["2030", "2030-02-30T00:00:00Z", "", true, 0])(
  "rejects malformed expiry %s for selection and confirmation",
  async (expires_at) => {
    const value = fixture([
      {
        id: "credit_1",
        reset_type: "codexRateLimits",
        status: "available",
        expires_at,
      },
    ]);
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    expect(
      (screen.getByRole("button", { name: "Use" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true);
    fireEvent.click(screen.getByRole("button", { name: "View details" }));
    expect(
      screen.queryByRole("button", { name: "Select Reset credit 1" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Consume reset credit" }),
    ).toBeNull();
    expect(value.request).not.toHaveBeenCalled();
  },
);
it.each([undefined, null, "2999-01-01T00:00:00Z"])(
  "preserves omitted and valid future expiry selection (%s)",
  async (expires_at) => {
    const value = fixture([
      {
        id: "credit_1",
        reset_type: "codexRateLimits",
        status: "available",
        expires_at,
      },
    ]);
    render(<value.Harness />);
    await selectExactCredit();
    expect(
      (
        screen.getByRole("button", {
          name: "Consume reset credit",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(false);
    expect(value.request).not.toHaveBeenCalled();
  },
);

it("restores the heading after another credit replaces the selected row", async () => {
  const value = fixture();
  render(<value.Harness />);
  await selectExactCredit();
  const original = screen.getByRole("button", {
    name: "Select Reset credit 1",
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Replace fixture credit" }),
  );
  const replacement = screen.getByRole("button", {
    name: "Select Reset credit 1",
  });
  expect((replacement as HTMLButtonElement).disabled).toBe(false);
  expect(original.isConnected).toBe(false);
  expect(
    (
      screen.getByRole("button", {
        name: "Consume reset credit",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("heading", { name: "Reset credits" }),
  );
  expect(globalThis.document.activeElement).not.toBe(replacement);
  expect(value.request).not.toHaveBeenCalled();
});

const numberedDetails = [
  {
    id: "RateLimitResetCredit_fixture_unavailable",
    reset_type: "codexRateLimits",
    status: "unavailable",
    title: "Private native title",
    description: "Private native description",
  },
  {
    id: "RateLimitResetCredit_fixture_a",
    reset_type: "codexRateLimits",
    status: "available",
    expires_at: "2099-01-01T00:00:00Z",
  },
  {
    id: "RateLimitResetCredit_fixture_b",
    reset_type: "codexRateLimits",
    status: "available",
    expires_at: "2099-01-01T00:00:00Z",
  },
];
it.each(["en", "ko"])(
  "numbers every returned detail in %s while sending only the original selected identity",
  async (language) => {
    await i18n.changeLanguage(language);
    try {
      const value = fixture(numberedDetails);
      value.request.mockRejectedValueOnce(
        new ConnectError("lost acknowledgment", Code.Unavailable),
      );
      render(<value.Harness />);
      const use = await screen.findByRole("button", {
        name: language === "en" ? "Use" : "사용하기",
      });
      await waitFor(() =>
        expect((use as HTMLButtonElement).disabled).toBe(false),
      );
      fireEvent.click(use);
      for (let ordinal = 1; ordinal <= 3; ordinal++)
        expect(
          screen.getByText(
            language === "en" ? `Reset credit ${ordinal}` : `리셋권 ${ordinal}`,
            { selector: "strong" },
          ),
        ).toBeTruthy();
      for (const credit of numberedDetails)
        expect(
          screen.getByText(
            `${language === "en" ? "ID" : "식별자"}: ${credit.id}`,
          ),
        ).toBeTruthy();
      expect(screen.queryByText("Private native title")).toBeNull();
      expect(screen.queryByText("Private native description")).toBeNull();
      expect(
        screen.queryByRole("button", {
          name: language === "en" ? "Select Reset credit 1" : "리셋권 1 선택",
        }),
      ).toBeNull();
      expect(
        globalThis.document.querySelectorAll(".reset-credit-row time").length,
      ).toBe(2);
      fireEvent.click(
        screen.getByRole("button", {
          name: language === "en" ? "Select Reset credit 3" : "리셋권 3 선택",
        }),
      );
      expect(value.request).not.toHaveBeenCalled();
      expect(
        within(
          globalThis.document.querySelector<HTMLElement>(
            ".reset-credit-confirmation",
          )!,
        ).getByText(language === "en" ? "Reset credit 3" : "리셋권 3"),
      ).toBeTruthy();
      fireEvent.click(
        screen.getByRole("button", {
          name: copy("subscription-quota.confirmCreditConsumption_251822"),
        }),
      );
      fireEvent.click(
        await screen.findByRole("button", {
          name: copy(
            "subscription-quota.retryOriginalQuotaOrCreditRequest_071535",
          ),
        }),
      );
      await waitFor(() => expect(value.request).toHaveBeenCalledTimes(2));
      expect(value.request.mock.calls[1][0]).toEqual(
        value.request.mock.calls[0][0],
      );
      expect(value.request.mock.calls[0][0]).toMatchObject({
        creditId: numberedDetails[2].id,
        confirmed: true,
        nextCredit: false,
        connectionId: value.connection,
        generationId: value.generation,
        creditsObservationId: value.inventory,
        mutation: { id: value.account.id, expectedRevision: 1n },
      });
    } finally {
      await i18n.changeLanguage("en");
    }
  },
);
it("retains captured ordinal and full identity through reorder, replacement, locale and focus changes", async () => {
  const value = fixture(numberedDetails);
  render(<value.Harness />);
  const use = await screen.findByRole("button", { name: "Use" });
  await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(false));
  fireEvent.click(use);
  fireEvent.click(
    screen.getByRole("button", { name: "Select Reset credit 3" }),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Reorder fixture credits" }),
  );
  const confirmation = globalThis.document.querySelector(
    ".reset-credit-confirmation",
  )!;
  expect(confirmation.textContent).toContain("Reset credit 3");
  expect(confirmation.textContent).toContain(`ID: ${numberedDetails[2].id}`);
  expect(
    (
      screen.getByRole("button", {
        name: "Consume reset credit",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(
    screen.getByRole("button", { name: "Select Reset credit 1" }),
  ).toBeTruthy();
  try {
    await i18n.changeLanguage("ko");
    expect(
      await within(confirmation as HTMLElement).findByText("리셋권 3"),
    ).toBeTruthy();
    expect(confirmation.textContent).toContain(numberedDetails[2].id);
    expect(
      (
        screen.getByRole("button", {
          name: "리셋권 사용",
        }) as HTMLButtonElement
      ).disabled,
    ).toBe(true);
  } finally {
    await i18n.changeLanguage("en");
  }
  fireEvent.click(
    screen.getByRole("button", { name: "Replace fixture credit" }),
  );
  expect(confirmation.textContent).toContain("Reset credit 3");
  expect(confirmation.textContent).toContain(numberedDetails[2].id);
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(globalThis.document.activeElement).toBe(
    screen.getByRole("heading", { name: "Reset credits" }),
  );
  expect(value.request).not.toHaveBeenCalled();
  expect(value.reconcile).not.toHaveBeenCalled();
});

it("keeps automatic credits off until exact confirmation and disables them explicitly", async () => {
  const value = fixture();
  render(<value.Harness />);
  const checkbox = (await screen.findByRole("checkbox", {
    name: "Automatic use",
  })) as HTMLInputElement;
  await waitFor(() => expect(checkbox.disabled).toBe(false));
  expect(checkbox.checked).toBe(false);
  fireEvent.click(checkbox);
  expect(value.consent).not.toHaveBeenCalled();
  expect(
    await screen.findByRole("heading", {
      name: "Enable automatic reset credit use?",
    }),
  ).toBeTruthy();
  expect(screen.getByText("Quota fixture")).toBeTruthy();
  expect(
    screen.getByText("Uses the earliest-expiring eligible reset credit first."),
  ).toBeTruthy();
  expect(
    screen.getByText(
      "If individual details are unavailable, Codex selects the next reset credit.",
    ),
  ).toBeTruthy();
  expect(
    screen.getByText("Automatic use continues after this dialog closes."),
  ).toBeTruthy();
  expect(
    screen.getByText("Failed sessions do not resume automatically."),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Enable automatic use" }));
  await waitFor(() => expect(value.consent).toHaveBeenCalledTimes(1));
  expect(value.consent.mock.calls[0][0]).toMatchObject({
    connectionId: value.connection,
    generationId: value.generation,
    enabled: true,
    confirmed: true,
    mutation: { id: value.account.id, expectedRevision: 1n },
  });
  const enabled = (await screen.findByRole("checkbox", {
    name: "Automatic use",
  })) as HTMLInputElement;
  await waitFor(() => expect(enabled.checked).toBe(true));
  fireEvent.click(enabled);
  await waitFor(() => expect(value.consent).toHaveBeenCalledTimes(2));
  expect(value.consent.mock.calls[1][0]).toMatchObject({
    enabled: false,
    confirmed: false,
  });
  expect(value.request).not.toHaveBeenCalled();
});
it("fences a consent confirmation after account revision replacement", async () => {
  const value = fixture();
  render(<value.Harness />);
  const checkbox = await screen.findByRole("checkbox", {
    name: "Automatic use",
  });
  await waitFor(() =>
    expect((checkbox as HTMLInputElement).disabled).toBe(false),
  );
  fireEvent.click(checkbox);
  fireEvent.click(
    screen.getByRole("button", { name: "Change fixture account revision" }),
  );
  expect(
    (
      screen.getByRole("button", {
        name: "Enable automatic use",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(value.consent).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  expect(
    (
      (await screen.findByRole("checkbox", {
        name: "Automatic use",
      })) as HTMLInputElement
    ).checked,
  ).toBe(false);
});

it("leaves the consent confirmation when the account disconnects and does not restore it on reconnect", async () => {
  const value = fixture();
  render(<value.Harness />);
  const checkbox = await screen.findByRole("checkbox", {
    name: "Automatic use",
  });
  await waitFor(() =>
    expect((checkbox as HTMLInputElement).disabled).toBe(false),
  );
  fireEvent.click(checkbox);
  await screen.findByRole("heading", {
    name: "Enable automatic reset credit use?",
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Disconnect fixture account" }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", {
        name: "Enable automatic reset credit use?",
      }),
    ).toBeNull(),
  );
  // The read-only presentation keeps the status footer and admits no control.
  expect(
    screen.getByRole("button", { name: "Refresh account status and quota" }),
  ).toBeTruthy();
  expect(screen.queryByRole("checkbox")).toBeNull();
  expect(value.consent).not.toHaveBeenCalled();
  fireEvent.click(
    screen.getByRole("button", { name: "Reconnect fixture account" }),
  );
  const reconnected = (await screen.findByRole("checkbox", {
    name: "Automatic use",
  })) as HTMLInputElement;
  expect(reconnected.checked).toBe(false);
  expect(
    screen.queryByRole("heading", {
      name: "Enable automatic reset credit use?",
    }),
  ).toBeNull();
  expect(value.consent).not.toHaveBeenCalled();
});

it("closes the consent confirmation when the account disconnects while the owner keeps the controls visible", async () => {
  const value = fixture();
  render(<value.Harness />);
  const checkbox = await screen.findByRole("checkbox", {
    name: "Automatic use",
  });
  await waitFor(() =>
    expect((checkbox as HTMLInputElement).disabled).toBe(false),
  );
  fireEvent.click(checkbox);
  await screen.findByRole("heading", {
    name: "Enable automatic reset credit use?",
  });
  fireEvent.click(
    screen.getByRole("button", { name: "Drop fixture connection" }),
  );
  await waitFor(() =>
    expect(
      screen.queryByRole("heading", {
        name: "Enable automatic reset credit use?",
      }),
    ).toBeNull(),
  );
  expect(value.consent).not.toHaveBeenCalled();
  // `visible` stayed true, so the original controls remain mounted and the
  // confirmation is not restored on its own.
  expect(
    await screen.findByRole("checkbox", { name: "Automatic use" }),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Enable automatic use" }),
  ).toBeNull();
});

it("keeps the saved quota observation visible for a disconnected account on a server without quota support", async () => {
  const value = fixture(
    undefined,
    undefined,
    "",
    false,
    true,
    "",
    false,
    "",
    false,
    false,
    {},
    "",
    false,
    "uncertain",
    true,
    { quota_observed_at: new Date().toISOString(), quota_state: "failed" },
    // Neither the server nor the Worker quota capability is advertised.
    false,
  );
  render(<value.Harness />);
  fireEvent.click(
    await screen.findByRole("button", { name: "Disconnect fixture account" }),
  );
  await waitFor(() => expect(screen.queryByRole("checkbox")).toBeNull());
  // Wait for the status read so an unread capability set cannot hide the gate.
  await waitFor(() => expect(value.status).toHaveBeenCalled());
  expect(await screen.findByText(/Last successful observation/)).toBeTruthy();
  expect(screen.getByText(/The latest refresh failed/)).toBeTruthy();
});

it("keeps a local preference save failure visible after the account disconnects", async () => {
  const value = fixture();
  render(<value.Harness />);
  const checkbox = await screen.findByRole("checkbox", {
    name: /Notify me of observed quota recovery/,
  });
  await waitFor(() =>
    expect((checkbox as HTMLInputElement).disabled).toBe(false),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Oversize fixture document" }),
  );
  fireEvent.click(checkbox);
  const failure =
    "Account preferences could not be saved. Reload the current account before saving.";
  await screen.findByText(failure);
  fireEvent.click(
    screen.getByRole("button", { name: "Disconnect fixture account" }),
  );
  await waitFor(() => expect(screen.queryByRole("checkbox")).toBeNull());
  expect((await screen.findByText(failure)).getAttribute("role")).toBe("alert");
});

it("keeps every independent retained failure visible after the account disconnects", async () => {
  const value = fixture();
  value.request.mockRejectedValueOnce(
    new ConnectError("private-quota-fixture", Code.InvalidArgument),
  );
  value.consent.mockRejectedValueOnce(
    new ConnectError("private-consent-fixture", Code.InvalidArgument),
  );
  render(<value.Harness />);
  const refresh = await screen.findByRole("button", {
    name: "Refresh account status and quota",
  });
  await waitFor(() =>
    expect((refresh as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(refresh);
  await waitFor(() => expect(value.request).toHaveBeenCalledOnce());
  await screen.findByText("DeliDev could not complete this request.");
  const checkbox = await screen.findByRole("checkbox", {
    name: "Automatic use",
  });
  await waitFor(() =>
    expect((checkbox as HTMLInputElement).disabled).toBe(false),
  );
  fireEvent.click(checkbox);
  fireEvent.click(
    await screen.findByRole("button", { name: "Enable automatic use" }),
  );
  await waitFor(() => expect(value.consent).toHaveBeenCalledOnce());
  await waitFor(() =>
    expect(
      screen.getAllByText("DeliDev could not complete this request."),
    ).toHaveLength(1),
  );
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await waitFor(() =>
    expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull(),
  );
  fireEvent.click(
    screen.getByRole("button", { name: "Disconnect fixture account" }),
  );
  await waitFor(() => expect(screen.queryByRole("checkbox")).toBeNull());
  expect(
    screen.getAllByText("DeliDev could not complete this request."),
  ).toHaveLength(2);
  expect(screen.queryByText(/private-(quota|consent)-fixture/)).toBeNull();
  expect(value.request).toHaveBeenCalledOnce();
  expect(value.consent).toHaveBeenCalledOnce();
});

it.each(["light", "dark"])(
  "localizes automatic credit consent in Korean using the %s theme",
  async (theme) => {
    globalThis.document.documentElement.dataset.theme = theme;
    try {
      await i18n.changeLanguage("ko");
      const value = fixture();
      render(<value.Harness />);
      const checkbox = await screen.findByRole("checkbox", {
        name: "자동 사용",
      });
      await waitFor(() =>
        expect((checkbox as HTMLInputElement).disabled).toBe(false),
      );
      fireEvent.click(checkbox);
      expect(
        await screen.findByRole("heading", {
          name: "리셋권 자동 사용을 켤까요?",
        }),
      ).toBeTruthy();
      expect(
        screen.getByText(
          "서버 실행 중에 동작합니다. 다시 로그인하면 재승인이 필요합니다.",
        ),
      ).toBeTruthy();
      fireEvent.click(screen.getByRole("button", { name: "취소" }));
      expect(value.consent).not.toHaveBeenCalled();
    } finally {
      await i18n.changeLanguage("en");
      delete globalThis.document.documentElement.dataset.theme;
    }
  },
);

it.each(["queued", "sending", "uncertain"])(
  "permits consent revocation while the original automatic credit is %s",
  async (phase) => {
    const value = fixture(
      undefined,
      undefined,
      "",
      false,
      true,
      "",
      false,
      "",
      false,
      true,
      {},
      "",
      true,
      phase,
    );
    render(<value.Harness />);
    const checkbox = (await screen.findByRole("checkbox", {
      name: "Automatic use",
    })) as HTMLInputElement;
    await waitFor(() => expect(checkbox.disabled).toBe(false));
    expect(checkbox.checked).toBe(true);
    fireEvent.click(checkbox);
    await waitFor(() => expect(value.consent).toHaveBeenCalledTimes(1));
    expect(value.consent.mock.calls[0][0]).toMatchObject({
      enabled: false,
      confirmed: false,
      connectionId: value.connection,
      generationId: value.generation,
    });
    expect(value.request).not.toHaveBeenCalled();
    expect(value.reconcile).not.toHaveBeenCalled();
  },
);

it("consumes beside an active Execute without selecting a Runner", async () => {
  const runner = newRequestId(),
    value = fixture(
      undefined,
      { action: "execute", machine_id: runner },
      "",
      false,
      true,
      "",
      false,
    );
  render(<value.Harness />);
  await selectExactCredit();
  fireEvent.click(screen.getByRole("button", { name: "Consume reset credit" }));
  await waitFor(() => expect(value.request).toHaveBeenCalledTimes(1));
  expect(value.request.mock.calls[0][0]).toMatchObject({
    machineId: "",
    creditId: "credit_1",
    confirmed: true,
    generationId: value.generation,
  });
});

it.each(["login", "refresh", "logout"])(
  "keeps reset-credit use disabled during a %s lease",
  async (action) => {
    const value = fixture(
      undefined,
      { action, machine_id: newRequestId() },
      "",
      false,
      true,
      "",
      false,
    );
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    const use = screen.getByRole("button", { name: "Use" });
    await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(true));
    expect(value.request).not.toHaveBeenCalled();
  },
);

it.each([
  ["no credit capability", false, false],
  ["historical server capability only", true, false],
])(
  "shows server-update guidance without a Runner fallback (%s)",
  async (_name, historicalServer) => {
    const value = fixture(
      undefined,
      undefined,
      "",
      false,
      true,
      "",
      historicalServer,
      "",
      false,
      false,
      {},
      "",
      false,
      "uncertain",
      false,
    );
    render(<value.Harness />);
    await screen.findByText(/Last successful observation/);
    const use = screen.getByRole("button", { name: "Use" });
    await waitFor(() => expect((use as HTMLButtonElement).disabled).toBe(true));
    expect(
      screen.getAllByText(
        "Update the server to consume reset credits without a Runner Device.",
      ).length,
    ).toBeGreaterThan(0);
    expect(value.request).not.toHaveBeenCalled();
  },
);

it("keeps historical server-credit reconciliation on its original capability and captured generation", async () => {
  const value = fixture(
    undefined,
    undefined,
    "",
    true,
    true,
    "",
    true,
    "uncertain",
    true,
    false,
    {},
    "",
    false,
    "uncertain",
    false,
  );
  render(<value.Harness />);
  const reconcile = await screen.findByRole("button", {
    name: "Check original operation result",
  });
  await waitFor(() =>
    expect((reconcile as HTMLButtonElement).disabled).toBe(false),
  );
  fireEvent.click(reconcile);
  await waitFor(() => expect(value.reconcile).toHaveBeenCalledTimes(1));
  expect(value.reconcile.mock.calls[0][0]).toMatchObject({
    connectionId: value.connection,
    generationId: value.creditGeneration,
  });
  expect(value.request).not.toHaveBeenCalled();
});

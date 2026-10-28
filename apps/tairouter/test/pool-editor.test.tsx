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
  PoolService,
  ProviderService,
  RoutingService,
  PoolSchema,
  PoolStrategy,
  ProviderAuthKind,
  ConnectionState,
  OrganizationSchema,
  GetSessionResponseSchema,
  type Pool,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { PoolsPage } from "../src/routing";

// Typing and clicking through jsdom dialogs is slow on shared CI machines.
const SLOW = 30_000;
const SERVER_REJECTION = "pool name and 1 to 100 members required";

type Connection = {
  id: string;
  name: string;
  state?: ConnectionState;
};
const ready = (id: string, name = id): Connection => ({
  id,
  name,
  state: ConnectionState.READY,
});

function mount(options: {
  pools?: Pool[];
  connections: Connection[];
  mutate?: (request: unknown) => object | Promise<object>;
  /** Delays the connection list so the loading state can be observed. */
  connectionsGate?: Promise<void>;
}) {
  const mutate = options.mutate ?? (() => ({}));
  const transport = createRouterTransport((r) => {
    r.service(PoolService, {
      listPools: () => ({ pools: options.pools ?? [] }),
      createPool: mutate,
      updatePool: mutate,
    });
    r.service(ProviderService, {
      listProviderConnections: async () => ({
        connections: (await options.connectionsGate, options.connections).map(
          (c) => ({
            providerId: "openai",
            authKind: ProviderAuthKind.OAUTH,
            ...c,
          }),
        ),
      }),
    });
    r.service(RoutingService, { listRoutes: () => ({}) });
  });
  render(
    <ApiProvider
      rpcTransport={transport}
      queryClient={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <PoolsPage
        organization={create(OrganizationSchema, { id: "org", name: "Studio" })}
        session={create(GetSessionResponseSchema, { userId: "person" })}
      />
    </ApiProvider>,
  );
}
function pool(
  members: { id: string; weight?: number; nativeOptions?: object }[],
  extra: Partial<Pool> = {},
): Pool {
  return create(PoolSchema, {
    id: "pool",
    name: "Saved pool",
    strategy: PoolStrategy.PRIORITY,
    members: members.map((m, i) => ({
      connectionId: m.id,
      priority: i,
      weight: m.weight ?? 1,
      nativeOptions: m.nativeOptions,
    })),
    ...extra,
  } as never);
}
async function openCreate(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: "Create pool" }));
  return await screen.findByRole("dialog");
}
async function openEdit(user: ReturnType<typeof userEvent.setup>) {
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByLabelText("Add a connection");
  return dialog;
}
async function addConnection(
  user: ReturnType<typeof userEvent.setup>,
  dialog: HTMLElement,
  id: string,
) {
  await user.selectOptions(
    await within(dialog).findByLabelText("Add a connection"),
    id,
  );
}
const saveButton = (dialog: HTMLElement) =>
  within(dialog).getByRole("button", {
    name: /Save pool|Saving…/,
  }) as HTMLButtonElement;
const setName = (dialog: HTMLElement, value: string) =>
  fireEvent.change(within(dialog).getByLabelText("Pool name"), {
    target: { value },
  });

describe("pool editor chrome", () => {
  it(
    "names the dialog, scopes it to the organization and orders the groups",
    async () => {
      const user = userEvent.setup();
      mount({ connections: [ready("a", "First")] });
      const dialog = await openCreate(user);
      expect(
        within(dialog).getByRole("heading", { name: "Create pool" }),
      ).toBeTruthy();
      expect(within(dialog).getByText("Only yours in Studio.")).toBeTruthy();
      expect(
        within(dialog).getByRole("button", { name: "Close dialog" }),
      ).toBeTruthy();
      expect(dialog.className).toContain("editor");
      expect(dialog.getAttribute("aria-describedby")).toBeTruthy();
      const headings = within(dialog)
        .getAllByRole("heading", { level: 3 })
        .map((h) => h.textContent);
      expect(headings).toEqual([
        "Pool details",
        "Session affinity",
        "Your connections, in priority order",
      ]);
      const text = dialog.textContent ?? "";
      expect(text.indexOf("Your connections, in priority order")).toBeLessThan(
        text.indexOf("Native OAuth pool settings"),
      );
      const labels = within(dialog)
        .getAllByRole("button")
        .map((b) => b.textContent);
      expect(labels.slice(-2)).toEqual(["Cancel", "Save pool"]);
      expect(
        within(dialog).getByText("Top connection has first priority."),
      ).toBeTruthy();
      expect(
        within(dialog).getByText(
          "Opt in for providers that support generic OAuth session affinity. Codex and Anthropic retain their provider-specific session rules.",
        ),
      ).toBeTruthy();
      expect(
        (
          within(dialog).getByLabelText(
            "Keep generic OAuth sessions on the same account",
          ) as HTMLInputElement
        ).checked,
      ).toBe(false);
    },
    SLOW,
  );
  it(
    "titles the edit dialog and keeps the saved strategy and affinity",
    async () => {
      const user = userEvent.setup();
      mount({
        connections: [ready("a", "First")],
        pools: [
          pool([{ id: "a" }], {
            strategy: PoolStrategy.LEAST_RECENTLY_USED,
            affinityEnabled: true,
          }),
        ],
      });
      const dialog = await openEdit(user);
      expect(
        within(dialog).getByRole("heading", { name: "Edit pool" }),
      ).toBeTruthy();
      expect(
        (within(dialog).getByLabelText("Strategy") as HTMLSelectElement).value,
      ).toBe(String(PoolStrategy.LEAST_RECENTLY_USED));
      expect(
        (
          within(dialog).getByLabelText(
            "Keep generic OAuth sessions on the same account",
          ) as HTMLInputElement
        ).checked,
      ).toBe(true);
    },
    SLOW,
  );
  it(
    "sends every strategy exactly and the unchecked affinity as false",
    async () => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      mount({
        connections: [ready("a", "First")],
        pools: [pool([{ id: "a" }], { affinityEnabled: true })],
        mutate,
      });
      for (const strategy of [
        PoolStrategy.PRIORITY,
        PoolStrategy.ROUND_ROBIN,
        PoolStrategy.LEAST_RECENTLY_USED,
      ]) {
        const dialog = await openEdit(user);
        await user.selectOptions(
          within(dialog).getByLabelText("Strategy"),
          String(strategy),
        );
        await user.click(
          within(dialog).getByLabelText(
            "Keep generic OAuth sessions on the same account",
          ),
        );
        await user.click(saveButton(dialog));
        await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
      }
      expect(mutate).toHaveBeenCalledTimes(3);
      expect(
        mutate.mock.calls.map((c) => {
          const r = c[0] as { poolId: string; strategy: number };
          return [r.poolId, r.strategy];
        }),
      ).toEqual([
        ["pool", PoolStrategy.PRIORITY],
        ["pool", PoolStrategy.ROUND_ROBIN],
        ["pool", PoolStrategy.LEAST_RECENTLY_USED],
      ]);
      expect(
        mutate.mock.calls.every(
          (c) =>
            (c[0] as { affinityEnabled: boolean }).affinityEnabled === false,
        ),
      ).toBe(true);
    },
    SLOW * 2,
  );
});

describe("pool name validation", () => {
  const cases: [string, string, boolean][] = [
    ["200 ASCII bytes", "a".repeat(200), true],
    ["201 ASCII bytes", "a".repeat(201), false],
    ["100 two-byte characters (200 bytes)", "é".repeat(100), true],
    [
      "100 two-byte characters plus one byte (201 bytes)",
      "é".repeat(100) + "a",
      false,
    ],
  ];
  it.each(cases)(
    "%s",
    async (_label, value, accepted) => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      mount({ connections: [ready("a", "First")], mutate });
      const dialog = await openCreate(user);
      await addConnection(user, dialog, "a");
      // Surrounding spaces are trimmed before the byte bound is evaluated.
      setName(dialog, `  ${value}  `);
      if (accepted) {
        expect(saveButton(dialog).disabled).toBe(false);
        await user.click(saveButton(dialog));
        await waitFor(() => expect(mutate).toHaveBeenCalledOnce());
        expect((mutate.mock.calls[0]![0] as { name: string }).name).toBe(value);
      } else {
        expect(saveButton(dialog).disabled).toBe(true);
        const input = within(dialog).getByLabelText("Pool name");
        expect(input.getAttribute("aria-invalid")).toBe("true");
        expect(input.hasAttribute("maxlength")).toBe(false);
        expect(
          within(dialog).getAllByText(
            "Pool name must be 200 UTF-8 bytes or fewer.",
          ).length,
        ).toBeGreaterThan(0);
        expect((input as HTMLInputElement).value).toBe(`  ${value}  `);
        expect(mutate).not.toHaveBeenCalled();
      }
    },
    SLOW,
  );
  it(
    "requires a nonblank name and compares duplicates exactly, excluding the edited pool",
    async () => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      mount({
        connections: [ready("a", "First")],
        pools: [pool([{ id: "a" }], { name: "Taken" })],
        mutate,
      });
      const dialog = await openCreate(user);
      await addConnection(user, dialog, "a");
      setName(dialog, "   ");
      expect(saveButton(dialog).disabled).toBe(true);
      const summary = within(dialog).getByTestId("error-summary");
      expect(
        within(summary).getByRole("link", { name: "Pool name is required." }),
      ).toBeTruthy();
      setName(dialog, " Taken ");
      expect(saveButton(dialog).disabled).toBe(true);
      expect(
        within(dialog).getAllByText(
          "This name is already in use. Choose a different name.",
        ).length,
      ).toBe(2);
      setName(dialog, "taken");
      expect(saveButton(dialog).disabled).toBe(false);
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
      // Editing the pool that owns the name is not a duplicate.
      const edit = await openEdit(user);
      expect(saveButton(edit).disabled).toBe(false);
    },
    SLOW,
  );
});

describe("connection member cards", () => {
  it(
    "lists only ready unselected connections and explains empty selectors",
    async () => {
      const user = userEvent.setup();
      mount({
        connections: [
          ready("a", "First"),
          ready("b", "Second"),
          { id: "c", name: "Broken", state: ConnectionState.DISABLED },
        ],
      });
      const dialog = await openCreate(user);
      const select = await within(dialog).findByLabelText("Add a connection");
      const options = () =>
        within(select as HTMLElement)
          .getAllByRole("option")
          .map((o) => o.textContent);
      expect(options()).toEqual(["Choose your connection", "First", "Second"]);
      await addConnection(user, dialog, "a");
      expect(options()).toEqual(["Choose your connection", "Second"]);
      await addConnection(user, dialog, "b");
      expect(options()).toEqual(["Choose your connection"]);
      expect(
        within(dialog).getByText("All available connections are in this pool."),
      ).toBeTruthy();
    },
    SLOW,
  );
  it(
    "explains when no ready connection exists",
    async () => {
      const user = userEvent.setup();
      mount({
        connections: [
          { id: "c", name: "Broken", state: ConnectionState.DISABLED },
        ],
      });
      const dialog = await openCreate(user);
      expect(
        await within(dialog).findByText("No available connections to add."),
      ).toBeTruthy();
    },
    SLOW,
  );
  it(
    "numbers cards, names actions after the connection and reassigns priorities in displayed order",
    async () => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      const kept = {
        autoSwitchThreshold: 0,
        priority: -5,
        allowCredits: false,
      };
      mount({
        connections: [ready("a", "First"), ready("b", "Second")],
        pools: [
          pool([
            { id: "a", weight: 3, nativeOptions: kept },
            { id: "b", weight: 5 },
          ]),
        ],
        mutate,
      });
      const dialog = await openEdit(user);
      const cards = within(dialog).getAllByRole("group", {
        name: /^\d · /,
      });
      expect(cards.map((c) => c.querySelector("legend")?.textContent)).toEqual([
        "1 · First",
        "2 · Second",
      ]);
      expect(
        (
          within(dialog).getByRole("button", {
            name: "Move up First",
          }) as HTMLButtonElement
        ).disabled,
      ).toBe(true);
      expect(
        within(dialog).getByRole("button", { name: "Remove First" }),
      ).toBeTruthy();
      await user.click(
        within(dialog).getByRole("button", { name: "Move up Second" }),
      );
      expect(dialog.querySelector('[aria-live="polite"]')?.textContent).toBe(
        "Moved Second up to position 1.",
      );
      // Weight edits keep the member's native options.
      const weights = within(dialog).getAllByLabelText("Weight");
      fireEvent.change(weights[1]!, { target: { value: "7" } });
      await user.click(saveButton(dialog));
      await waitFor(() => expect(mutate).toHaveBeenCalledOnce());
      expect(mutate.mock.calls[0]![0]).toMatchObject({
        poolId: "pool",
        members: [
          { connectionId: "b", priority: 0, weight: 5 },
          {
            connectionId: "a",
            priority: 1,
            weight: 7,
            nativeOptions: {
              autoSwitchThreshold: 0,
              priority: -5,
              allowCredits: false,
            },
          },
        ],
      });
    },
    SLOW,
  );
  it(
    "announces moves and removals and restores focus to a logical control",
    async () => {
      const user = userEvent.setup();
      mount({
        connections: [ready("a", "First"), ready("b", "Second")],
        pools: [pool([{ id: "a" }, { id: "b" }])],
      });
      const dialog = await openEdit(user);
      const live = dialog.querySelector('[aria-live="polite"]')!;
      await user.click(
        within(dialog).getByRole("button", { name: "Move up Second" }),
      );
      expect(live.textContent).toBe("Moved Second up to position 1.");
      expect(document.activeElement).toBe(
        within(dialog).getByRole("button", { name: "Remove Second" }),
      );
      await user.click(
        within(dialog).getByRole("button", { name: "Remove Second" }),
      );
      expect(live.textContent).toBe("Removed Second. 1 connection remaining.");
      expect(document.activeElement).toBe(
        within(dialog).getByRole("button", { name: "Remove First" }),
      );
      await user.click(
        within(dialog).getByRole("button", { name: "Remove First" }),
      );
      expect(document.activeElement).toBe(
        within(dialog).getByLabelText("Add a connection"),
      );
      expect(
        within(dialog).getAllByText("Add at least one connection.").length,
      ).toBeGreaterThan(0);
      expect(saveButton(dialog).disabled).toBe(true);
    },
    SLOW,
  );
  it(
    "blocks weights outside 1 to 1000 and fractions with associated errors",
    async () => {
      const user = userEvent.setup();
      mount({
        connections: [ready("a", "First")],
        pools: [pool([{ id: "a" }])],
      });
      const dialog = await openEdit(user);
      const weight = within(dialog).getByLabelText("Weight");
      for (const [value, blocked] of [
        ["0", true],
        ["1001", true],
        ["1.5", true],
        ["1000", false],
        ["1", false],
      ] as const) {
        fireEvent.change(weight, { target: { value } });
        expect(saveButton(dialog).disabled).toBe(blocked);
        expect(weight.getAttribute("aria-invalid") === "true").toBe(blocked);
      }
      fireEvent.change(weight, { target: { value: "0" } });
      expect(
        within(within(dialog).getByTestId("error-summary")).getByRole("link", {
          name: "Connection 1 (First): Weight must be a whole number from 1 to 1000.",
        }),
      ).toBeTruthy();
    },
    SLOW,
  );
  it(
    "keeps a missing reference visible and blocks save until it is removed",
    async () => {
      const user = userEvent.setup();
      mount({
        connections: [ready("a", "First")],
        pools: [pool([{ id: "gone" }, { id: "a" }])],
      });
      const dialog = await openEdit(user);
      expect(
        within(dialog).getByText("1 · Unavailable connection"),
      ).toBeTruthy();
      expect(saveButton(dialog).disabled).toBe(true);
      expect(
        within(within(dialog).getByTestId("error-summary")).getByRole("link", {
          name: /Connection 1 \(Unavailable connection\): This connection is unavailable/,
        }),
      ).toBeTruthy();
      await user.click(
        within(dialog).getByRole("button", {
          name: "Remove Unavailable connection",
        }),
      );
      expect(saveButton(dialog).disabled).toBe(false);
    },
    SLOW,
  );
});

describe("dependency loading", () => {
  it(
    "labels the loading connections group and blocks save until they arrive",
    async () => {
      const user = userEvent.setup();
      let release: () => void = () => {};
      const connectionsGate = new Promise<void>((resolve) => {
        release = resolve;
      });
      mount({
        connections: [ready("a", "First")],
        pools: [pool([{ id: "a" }])],
        connectionsGate,
      });
      await user.click(
        await screen.findByRole("button", { name: "Edit pool" }),
      );
      const dialog = await screen.findByRole("dialog");
      expect(
        within(dialog).getAllByText("Loading your connections…").length,
      ).toBeGreaterThan(0);
      expect(dialog.querySelector("legend")?.textContent).toBe(
        "1 · Loading connection",
      );
      expect(saveButton(dialog).disabled).toBe(true);
      expect(within(dialog).queryByText(/Unavailable connection/)).toBeNull();
      release();
      await waitFor(() =>
        expect(dialog.querySelector("legend")?.textContent).toBe("1 · First"),
      );
      expect(saveButton(dialog).disabled).toBe(false);
    },
    SLOW,
  );
});

describe("save behavior", () => {
  it(
    "shows Saving…, blocks duplicate submits and preserves the draft on server rejection",
    async () => {
      const user = userEvent.setup();
      let rejectSave: (error: unknown) => void = () => {};
      const mutate = rs.fn(
        (_r: unknown) =>
          new Promise<object>((_resolve, reject) => {
            rejectSave = reject;
          }),
      );
      const kept = { priority: 0, useRemainingQuota: false };
      mount({
        connections: [ready("a", "First"), ready("b", "Second")],
        pools: [
          pool(
            [
              { id: "a", nativeOptions: kept },
              { id: "b", weight: 9 },
            ],
            {
              affinityEnabled: true,
            },
          ),
        ],
        mutate,
      });
      const dialog = await openEdit(user);
      await user.click(
        within(dialog).getByRole("button", { name: "Move up Second" }),
      );
      await user.click(saveButton(dialog));
      await waitFor(() =>
        expect(saveButton(dialog).textContent).toBe("Saving…"),
      );
      expect(saveButton(dialog).disabled).toBe(true);
      fireEvent.submit(dialog.querySelector("form")!);
      expect(mutate).toHaveBeenCalledOnce();
      rejectSave(new ConnectError(SERVER_REJECTION, Code.InvalidArgument));
      // The console shows a fixed safe message for invalid-argument rejections.
      await within(dialog).findByText(
        "Check the entered values and the allowed destination policy, then try again.",
      );
      // Draft order, weights, affinity and member native options survive.
      expect(
        within(dialog)
          .getAllByRole("group", { name: /^\d · / })
          .map((c) => c.querySelector("legend")?.textContent),
      ).toEqual(["1 · Second", "2 · First"]);
      expect(
        (within(dialog).getAllByLabelText("Weight")[0] as HTMLInputElement)
          .value,
      ).toBe("9");
      expect(
        (
          within(dialog).getByLabelText(
            "Keep generic OAuth sessions on the same account",
          ) as HTMLInputElement
        ).checked,
      ).toBe(true);
      expect(saveButton(dialog).disabled).toBe(false);
      await user.click(saveButton(dialog));
      expect(mutate).toHaveBeenCalledTimes(2);
      expect(mutate.mock.calls[1]![0]).toMatchObject({
        members: [
          { connectionId: "b", priority: 0, weight: 9 },
          { connectionId: "a", priority: 1, nativeOptions: kept },
        ],
      });
    },
    SLOW,
  );
  it(
    "sends nothing when the dialog is cancelled",
    async () => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      mount({ connections: [ready("a", "First")], mutate });
      const dialog = await openCreate(user);
      setName(dialog, "Draft");
      await addConnection(user, dialog, "a");
      await user.click(within(dialog).getByRole("button", { name: "Cancel" }));
      expect(screen.queryByRole("dialog")).toBeNull();
      expect(mutate).not.toHaveBeenCalled();
    },
    SLOW,
  );
});

describe("member count bounds", () => {
  const many = (count: number) =>
    Array.from({ length: count }, (_, i) => ready(`c${i}`, `Conn ${i}`));
  const members = (count: number) =>
    Array.from({ length: count }, (_, i) => ({
      id: `c${i}`,
      weight: 1 + (i % 9),
    }));
  it(
    "saves exactly 100 ordered members on update",
    async () => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      mount({
        connections: many(101),
        pools: [pool(members(100))],
        mutate,
      });
      const dialog = await openEdit(user);
      expect(saveButton(dialog).disabled).toBe(false);
      await user.click(saveButton(dialog));
      await waitFor(() => expect(mutate).toHaveBeenCalledOnce());
      const request = mutate.mock.calls[0]![0] as {
        poolId: string;
        members: { connectionId: string; priority: number }[];
      };
      expect(request.poolId).toBe("pool");
      expect(request.members).toHaveLength(100);
      expect(request.members.map((m) => m.priority)).toEqual(
        Array.from({ length: 100 }, (_, i) => i),
      );
      expect(request.members[99]!.connectionId).toBe("c99");
    },
    SLOW,
  );
  it(
    "keeps the 101st draft member, blocks save with a group error and recovers after one removal",
    async () => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      mount({
        connections: many(101),
        pools: [pool(members(100))],
        mutate,
      });
      const dialog = await openEdit(user);
      await addConnection(user, dialog, "c100");
      expect(
        within(dialog).getAllByRole("group", { name: /^\d+ · / }),
      ).toHaveLength(101);
      expect(saveButton(dialog).disabled).toBe(true);
      expect(
        within(within(dialog).getByTestId("error-summary")).getByRole("link", {
          name: "A pool can contain at most 100 connections. Remove connections before saving.",
        }),
      ).toBeTruthy();
      await user.click(
        within(dialog).getByRole("button", { name: "Remove Conn 100" }),
      );
      expect(saveButton(dialog).disabled).toBe(false);
      await user.click(saveButton(dialog));
      await waitFor(() => expect(mutate).toHaveBeenCalledOnce());
      expect(
        (mutate.mock.calls[0]![0] as { members: unknown[] }).members,
      ).toHaveLength(100);
    },
    SLOW,
  );
  it(
    "blocks zero members and accepts one on create",
    async () => {
      const user = userEvent.setup();
      const mutate = rs.fn((_r: unknown) => ({}));
      mount({ connections: many(1), mutate });
      const dialog = await openCreate(user);
      setName(dialog, "Solo");
      expect(saveButton(dialog).disabled).toBe(true);
      await addConnection(user, dialog, "c0");
      expect(saveButton(dialog).disabled).toBe(false);
      await user.click(saveButton(dialog));
      await waitFor(() => expect(mutate).toHaveBeenCalledOnce());
      expect(mutate.mock.calls[0]![0]).toMatchObject({
        organizationId: "org",
        name: "Solo",
        members: [{ connectionId: "c0", priority: 0, weight: 1 }],
      });
      expect(mutate.mock.calls[0]![0]).not.toHaveProperty("poolId");
    },
    SLOW,
  );
});

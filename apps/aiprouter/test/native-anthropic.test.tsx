// SPDX-License-Identifier: Apache-2.0
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, it, rs } from "@rstest/core";
import { createRouterTransport } from "@connectrpc/connect";
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
  NativePoolStrategy,
  NativeQuotaWindow,
  NativePoolOptionsSchema,
  NativeModelRouteSchema,
  ProviderConnectionSchema,
  type Pool,
} from "@aiprouter/client";
import { ApiProvider } from "../src/api";
import { PoolsPage } from "../src/routing";
import { nativePoolError, nativeProvider } from "../src/native-pool";

afterEach(cleanup);

type Conn = { id: string; name: string; providerId: string };
const family = (provider: string, count = 3): Conn[] => [
  ...[...Array(count).keys()].map((i) => ({
    id: `c${i + 1}`,
    name: ["First", "Second", "Third"][i] ?? `Account ${i + 1}`,
    providerId: provider,
  })),
];
function mount(
  pool: Pool | undefined,
  connections: Conn[],
  mutation: (request: unknown) => object,
) {
  const transport = createRouterTransport((r) => {
    r.service(PoolService, {
      listPools: () => ({ pools: pool ? [pool] : [] }),
      createPool: mutation,
      updatePool: mutation,
    });
    r.service(ProviderService, {
      listProviderConnections: () => ({
        connections: connections.map((c) => ({
          ...c,
          authKind: ProviderAuthKind.OAUTH,
          state: ConnectionState.READY,
        })),
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
type User = ReturnType<typeof userEvent.setup>;
const save = () => screen.getByRole("button", { name: "Save pool" });
const saveDisabled = () => save().hasAttribute("disabled");
async function openCreate(user: User, ids: string[], configure = true) {
  await user.click(screen.getByRole("button", { name: "Create pool" }));
  await user.type(screen.getByLabelText("Pool name"), "Claude pool");
  for (const id of ids)
    await user.selectOptions(
      await screen.findByLabelText("Add a connection"),
      id,
    );
  await user.click(screen.getByText("Native OAuth pool settings"));
  if (configure)
    await user.click(
      screen.getByLabelText("Configure pool-level native settings"),
    );
}
const anthropicFamilies = ["anthropic", "anthropic2"];

type Request = {
  poolId?: string;
  strategy: PoolStrategy;
  affinityEnabled: boolean;
  members: {
    connectionId: string;
    priority: number;
    weight: number;
    nativeOptions?: {
      autoSwitchThreshold?: number;
      priority?: number;
      useRemainingQuota: boolean;
      allowCredits: boolean;
    };
  }[];
  nativeOptions?: {
    strategy: NativePoolStrategy;
    quotaWindow: NativeQuotaWindow;
    nativeMessages?: boolean;
    routes: {
      name: string;
      match: string;
      connectionIds: string[];
      fallback: boolean;
    }[];
  };
};
const lastRequest = (m: { mock: { calls: unknown[][] } }) =>
  m.mock.calls[0]?.[0] as Request;

const windows = [
  NativeQuotaWindow.UNSPECIFIED,
  NativeQuotaWindow.FIVE_HOUR,
  NativeQuotaWindow.WEEKLY,
  NativeQuotaWindow.MAX_UTILIZATION,
];
const strategies = [
  NativePoolStrategy.UNSPECIFIED,
  NativePoolStrategy.QUOTA,
  NativePoolStrategy.ROUND_ROBIN,
  NativePoolStrategy.FILL_FIRST,
];
const messageStates = ["", "true", "false"];
const cases = anthropicFamilies.flatMap((provider) =>
  windows.map((window, i) => ({
    provider,
    window,
    strategy: strategies[i]!,
    messages: messageStates[i % 3]!,
  })),
);

it.each(cases)(
  "$provider create: window $window, strategy $strategy and native messages '$messages' serialize unchanged",
  async ({ provider, window, strategy, messages }) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    mount(undefined, family(provider), mutation);
    await openCreate(user, ["c1"]);
    expect(
      screen.getByRole("group", {
        name: `${provider === "anthropic2" ? "Anthropic 2" : "Anthropic"} quota and messages`,
      }),
    ).toBeTruthy();
    // Codex-only controls never render for an Anthropic family.
    for (const label of [
      "Upstream failover threshold",
      "Failure window",
      "Pinned transient policy",
      "Cache affinity",
      "Excluded plans",
    ])
      expect(screen.queryByLabelText(label)).toBeNull();
    expect(screen.queryByRole("option", { name: "Reset first" })).toBeNull();
    await user.selectOptions(
      screen.getByLabelText("Native quota window"),
      String(window),
    );
    await user.selectOptions(
      screen.getByLabelText("Native selection strategy"),
      String(strategy),
    );
    await user.selectOptions(
      screen.getByLabelText("Native messages"),
      messages,
    );
    await user.click(save());
    const options = lastRequest(mutation).nativeOptions!;
    expect(options.quotaWindow).toBe(window);
    expect(options.strategy).toBe(strategy);
    expect(options.nativeMessages).toBe(
      messages === "" ? undefined : messages === "true",
    );
    expect(options.routes).toEqual([]);
  },
);

async function addRule(
  user: User,
  n: number,
  name: string,
  match: string,
  connections: string[],
) {
  await user.click(screen.getByRole("button", { name: "Add model rule" }));
  await user.type(screen.getByLabelText(`Rule name ${n}`), name);
  await user.type(screen.getByLabelText(`Model pattern ${n}`), match);
  for (const id of connections)
    await user.selectOptions(
      screen.getByLabelText(`Add rule connection ${n}`),
      id,
    );
}

it.each(anthropicFamilies)(
  "%s: moves rules and rule subsets independently and keeps the explicit false fallback",
  async (provider) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    mount(undefined, family(provider), mutation);
    await openCreate(user, ["c1", "c2"]);
    await addRule(user, 1, "Alpha", "claude-a*", ["c1", "c2"]);
    await addRule(user, 2, "Beta", "claude-b*", ["c2", "c1"]);
    expect(
      screen
        .getByRole("button", { name: "Move rule up: model rule 1" })
        .hasAttribute("disabled"),
    ).toBe(true);
    // Reorder the first-match rules only.
    await user.click(
      screen.getByRole("button", { name: "Move rule up: model rule 2" }),
    );
    expect(
      (screen.getByLabelText("Rule name 1") as HTMLInputElement).value,
    ).toBe("Beta");
    // Reorder only the Alpha rule's subset (now rule 2): Second above First.
    await user.click(
      screen.getByRole("button", {
        name: "Move connection up: Second in model rule 2",
      }),
    );
    expect(
      screen
        .getByRole("button", {
          name: "Move connection up: Second in model rule 2",
        })
        .hasAttribute("disabled"),
    ).toBe(true);
    expect(saveDisabled()).toBe(false);
    await user.click(save());
    const request = lastRequest(mutation);
    expect(request.nativeOptions!.routes).toEqual([
      expect.objectContaining({
        name: "Beta",
        match: "claude-b*",
        connectionIds: ["c2", "c1"],
        fallback: false,
      }),
      expect.objectContaining({
        name: "Alpha",
        match: "claude-a*",
        connectionIds: ["c2", "c1"],
        fallback: false,
      }),
    ]);
    // Generic member order is untouched by rule movement.
    expect(request.members.map((m) => m.connectionId)).toEqual(["c1", "c2"]);
  },
);

it("keeps focus on the moved rule connection and announces the new position", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    family("anthropic"),
    rs.fn((_r: unknown) => ({})),
  );
  await openCreate(user, ["c1", "c2", "c3"]);
  await addRule(user, 1, "Alpha", "claude-*", ["c1", "c2", "c3"]);
  const up = screen.getByRole("button", {
    name: "Move connection up: Third in model rule 1",
  });
  await user.click(up);
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("button", {
        name: "Move connection up: Third in model rule 1",
      }),
    ),
  );
  expect(screen.getByTestId("model-rule-announcement").textContent).toContain(
    "Moved Third up to position 2 in model rule 1.",
  );
  // Moving to the top disables Move up, so focus lands on Remove for it.
  await user.click(
    screen.getByRole("button", {
      name: "Move connection up: Third in model rule 1",
    }),
  );
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("button", {
        name: "Remove rule connection: Third from model rule 1",
      }),
    ),
  );
  // Removing a rule connection hands focus to the next one.
  await user.click(
    screen.getByRole("button", {
      name: "Remove rule connection: Third from model rule 1",
    }),
  );
  await waitFor(() =>
    expect(document.activeElement).toBe(
      screen.getByRole("button", {
        name: "Remove rule connection: First from model rule 1",
      }),
    ),
  );
});

it("excludes selected connections from the rule selector and explains a full roster", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    family("anthropic", 2),
    rs.fn((_r: unknown) => ({})),
  );
  await openCreate(user, ["c1", "c2"]);
  await user.click(screen.getByRole("button", { name: "Add model rule" }));
  const select = screen.getByLabelText("Add rule connection 1");
  expect(
    within(select)
      .getAllByRole("option")
      .map((o) => o.textContent),
  ).toEqual(["Choose a pool connection", "First", "Second"]);
  await user.selectOptions(select, "c1");
  expect(
    within(screen.getByLabelText("Add rule connection 1"))
      .getAllByRole("option")
      .map((o) => o.textContent),
  ).toEqual(["Choose a pool connection", "Second"]);
  await user.selectOptions(
    screen.getByLabelText("Add rule connection 1"),
    "c2",
  );
  screen.getByText("Every connection in this pool is already listed.");
});

it("reports rule field errors on their fields and blocks Save", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, family("anthropic2"), mutation);
  await openCreate(user, ["c1", "c2"]);
  // A new rule is empty, so it blocks Save until filled.
  await user.click(screen.getByRole("button", { name: "Add model rule" }));
  expect(saveDisabled()).toBe(true);
  screen.getByText("Enter a rule name.");
  screen.getByText("Enter a model pattern.");
  screen.getByText("Add at least one connection to this rule.");
  const name = screen.getByLabelText("Rule name 1");
  const match = screen.getByLabelText("Model pattern 1");
  expect(name.getAttribute("aria-invalid")).toBe("true");
  expect(name.getAttribute("aria-describedby")).toContain("-error");
  // Unsafe and too-long names.
  await user.type(name, "-bad");
  expect(name.getAttribute("aria-invalid")).toBe("true");
  await user.clear(name);
  await user.type(name, "a".repeat(65));
  expect(name.getAttribute("aria-invalid")).toBe("true");
  await user.clear(name);
  await user.type(name, "a".repeat(64));
  expect(name.getAttribute("aria-invalid")).toBeNull();
  // Unsafe and too-long patterns.
  await user.type(match, "bad pattern");
  expect(match.getAttribute("aria-invalid")).toBe("true");
  await user.clear(match);
  await user.type(match, "m".repeat(129));
  expect(match.getAttribute("aria-invalid")).toBe("true");
  await user.clear(match);
  await user.type(match, "claude-*");
  expect(match.getAttribute("aria-invalid")).toBeNull();
  await user.selectOptions(
    screen.getByLabelText("Add rule connection 1"),
    "c1",
  );
  expect(saveDisabled()).toBe(false);
  // Duplicate names and patterns are reported on the later rule.
  await addRule(user, 2, "a".repeat(64), "claude-*", ["c2"]);
  expect(
    screen.getByLabelText("Rule name 2").getAttribute("aria-invalid"),
  ).toBe("true");
  expect(
    screen.getByLabelText("Model pattern 2").getAttribute("aria-invalid"),
  ).toBe("true");
  expect(saveDisabled()).toBe(true);
  await user.clear(screen.getByLabelText("Rule name 2"));
  await user.type(screen.getByLabelText("Rule name 2"), "Other");
  expect(saveDisabled()).toBe(true);
  await user.clear(screen.getByLabelText("Model pattern 2"));
  await user.type(screen.getByLabelText("Model pattern 2"), "claude-2?");
  expect(saveDisabled()).toBe(false);
  expect(mutation).not.toHaveBeenCalled();
});

it("blocks Save when a rule references a connection removed from the pool", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, family("anthropic"), mutation);
  await openCreate(user, ["c1", "c2"]);
  await addRule(user, 1, "Alpha", "claude-*", ["c1", "c2"]);
  expect(saveDisabled()).toBe(false);
  await user.click(screen.getByRole("button", { name: "Remove First" }));
  screen.getByText(/no longer in this pool/);
  screen.getByText("First (not in this pool)");
  expect(saveDisabled()).toBe(true);
  // The rule is not silently rewritten; the user repairs it explicitly.
  await user.click(
    screen.getByRole("button", {
      name: "Remove rule connection: First from model rule 1",
    }),
  );
  expect(saveDisabled()).toBe(false);
  await user.click(save());
  expect(lastRequest(mutation).nativeOptions!.routes[0]!.connectionIds).toEqual(
    ["c2"],
  );
});

it("limits a pool to 32 model rules", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    family("anthropic", 1),
    rs.fn((_r: unknown) => ({})),
  );
  await openCreate(user, ["c1"]);
  const add = screen.getByRole("button", { name: "Add model rule" });
  for (let i = 0; i < 32; i++) await user.click(add);
  expect(add.hasAttribute("disabled")).toBe(true);
  screen.getByText(/32 of 32 rules\. Remove a rule to add another\./);
  expect(
    screen.getAllByRole("group", { name: /^Model rule \d+$/ }),
  ).toHaveLength(32);
  await user.click(
    screen.getByRole("button", { name: "Remove model rule 32" }),
  );
  expect(add.hasAttribute("disabled")).toBe(false);
}, 60000);

it("limits a rule subset to 32 distinct pool connections", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    family("anthropic", 33),
    rs.fn((_r: unknown) => ({})),
  );
  await user.click(screen.getByRole("button", { name: "Create pool" }));
  for (let i = 1; i <= 33; i++)
    await user.selectOptions(
      await screen.findByLabelText("Add a connection"),
      `c${i}`,
    );
  await user.click(screen.getByText("Native OAuth pool settings"));
  await user.click(
    screen.getByLabelText("Configure pool-level native settings"),
  );
  await user.click(screen.getByRole("button", { name: "Add model rule" }));
  for (let i = 1; i <= 32; i++)
    await user.selectOptions(
      screen.getByLabelText("Add rule connection 1"),
      `c${i}`,
    );
  expect(
    screen.getByLabelText("Add rule connection 1").hasAttribute("disabled"),
  ).toBe(true);
  screen.getByText("A rule can list at most 32 connections.");
}, 120000);

it("validates rule count and subset bounds in the shared native validator", () => {
  const accounts = family("anthropic", 34).map((c) =>
    create(ProviderConnectionSchema, {
      ...c,
      authKind: ProviderAuthKind.OAUTH,
      state: ConnectionState.READY,
    }),
  );
  const members = accounts.map((a, i) => ({
    connectionId: a.id,
    priority: i,
    weight: 1,
  }));
  const provider = nativeProvider(members, accounts);
  expect(provider).toBe("anthropic");
  const route = (i: number, ids: string[]) =>
    create(NativeModelRouteSchema, {
      name: `rule-${i}`,
      match: `model-${i}`,
      connectionIds: ids,
    });
  const rules = (n: number) =>
    [...Array(n).keys()].map((i) => route(i, ["c1"]));
  const withRoutes = (routes: ReturnType<typeof route>[]) =>
    create(NativePoolOptionsSchema, { routes });
  expect(
    nativePoolError(withRoutes(rules(32)), members, provider),
  ).toBeUndefined();
  expect(nativePoolError(withRoutes(rules(33)), members, provider)).toMatch(
    /at most 32 model rules/,
  );
  const ids = (n: number) => accounts.slice(0, n).map((a) => a.id);
  expect(
    nativePoolError(withRoutes([route(1, ids(32))]), members, provider),
  ).toBeUndefined();
  expect(
    nativePoolError(withRoutes([route(1, ids(33))]), members, provider),
  ).toMatch(/1.32 distinct current pool connections/);
  expect(
    nativePoolError(withRoutes([route(1, [])]), members, provider),
  ).toMatch(/1.32 distinct current pool connections/);
});

it("blocks Anthropic and Anthropic 2 mixing until explicitly cleared", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(
    undefined,
    [
      { id: "a1", name: "Claude one", providerId: "anthropic" },
      { id: "b1", name: "Claude two", providerId: "anthropic2" },
    ],
    mutation,
  );
  await openCreate(user, ["a1"]);
  expect(saveDisabled()).toBe(false);
  await user.selectOptions(screen.getByLabelText("Add a connection"), "b1");
  expect(saveDisabled()).toBe(true);
  expect(
    screen.getAllByText(/These providers cannot be mixed\./).length,
  ).toBeGreaterThan(0);
  // The incompatible draft is kept, not coerced.
  expect(
    (
      screen.getByLabelText(
        "Configure pool-level native settings",
      ) as HTMLInputElement
    ).checked,
  ).toBe(true);
  await user.click(
    screen.getByRole("button", { name: "Clear all native settings" }),
  );
  expect(saveDisabled()).toBe(false);
  await user.click(save());
  expect(lastRequest(mutation).nativeOptions).toBeUndefined();
});

const savedWithOverrides = (provider: string) =>
  create(PoolSchema, {
    id: "pool",
    name: "Overrides",
    strategy: PoolStrategy.PRIORITY,
    affinityEnabled: true,
    members: [
      {
        connectionId: "c1",
        priority: 0,
        weight: 3,
        nativeOptions: {
          autoSwitchThreshold: 0,
          priority: -100,
          useRemainingQuota: false,
          allowCredits: false,
        },
      },
      {
        connectionId: "c2",
        priority: 1,
        weight: 1,
        nativeOptions: {
          priority: 100,
          useRemainingQuota: true,
          allowCredits: provider === "openai",
        },
      },
      { connectionId: "c3", priority: 2, weight: 1 },
    ],
  });

it.each(["openai", ...anthropicFamilies])(
  "%s: member overrides survive collapse, reorder and save without pool-level options",
  async (provider) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    mount(savedWithOverrides(provider), family(provider), mutation);
    await user.click(await screen.findByRole("button", { name: "Edit pool" }));
    // Pool-level object is absent, yet member overrides stay available.
    await user.click(screen.getByText("Native OAuth pool settings"));
    expect(
      (
        screen.getByLabelText(
          "Configure pool-level native settings",
        ) as HTMLInputElement
      ).checked,
    ).toBe(false);
    const cards = screen.getAllByRole("group", {
      name: /^Native options for /,
    });
    expect(cards.map((c) => c.querySelector("legend")!.textContent)).toEqual([
      "Native options for First",
      "Native options for Second",
      "Native options for Third",
    ]);
    within(cards[0]!).getByText("Override configured");
    within(cards[2]!).getByText("No override");
    expect(
      (
        screen.getByLabelText(
          "Switching threshold for First (%)",
        ) as HTMLInputElement
      ).value,
    ).toBe("0");
    expect(
      (screen.getByLabelText("Native priority for First") as HTMLInputElement)
        .value,
    ).toBe("-100");
    expect(!!screen.queryByLabelText("Allow credits for Second")).toBe(
      provider === "openai",
    );
    // Collapse and reopen the outer disclosure: nothing is cleared.
    await user.click(screen.getByText("Native OAuth pool settings"));
    await user.click(screen.getByText("Native OAuth pool settings"));
    // Reorder the generic members: Second moves above First.
    await user.click(screen.getByRole("button", { name: "Move up Second" }));
    expect(
      screen
        .getAllByRole("group", { name: /^Native options for / })
        .map((c) => c.querySelector("legend")!.textContent),
    ).toEqual([
      "Native options for Second",
      "Native options for First",
      "Native options for Third",
    ]);
    await user.click(save());
    const request = lastRequest(mutation);
    expect(request.nativeOptions).toBeUndefined();
    expect(request.members.map((m) => [m.connectionId, m.priority])).toEqual([
      ["c2", 0],
      ["c1", 1],
      ["c3", 2],
    ]);
    expect(request.members[1]!.nativeOptions).toMatchObject({
      autoSwitchThreshold: 0,
      priority: -100,
      useRemainingQuota: false,
      allowCredits: false,
    });
    expect(request.members[0]!.nativeOptions).toMatchObject({
      priority: 100,
      useRemainingQuota: true,
      allowCredits: provider === "openai",
    });
    expect(
      request.members[0]!.nativeOptions!.autoSwitchThreshold,
    ).toBeUndefined();
    expect(request.members[2]!.nativeOptions).toBeUndefined();
  },
);

it("removes only one member override and keeps the rest", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(savedWithOverrides("anthropic"), family("anthropic"), mutation);
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  await user.click(screen.getByText("Native OAuth pool settings"));
  await user.click(screen.getByLabelText("Override native options for First"));
  expect(screen.queryByLabelText("Native priority for First")).toBeNull();
  within(
    screen.getAllByRole("group", { name: /^Native options for / })[0]!,
  ).getByText("No override");
  await user.click(save());
  const request = lastRequest(mutation);
  expect(request.members[0]!.nativeOptions).toBeUndefined();
  expect(request.members[1]!.nativeOptions).toMatchObject({ priority: 100 });
});

it("explains Use remaining quota without erasing the entered threshold", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, family("openai"), mutation);
  await openCreate(user, ["c1"], false);
  await user.click(screen.getByLabelText("Override native options for First"));
  await user.type(
    screen.getByLabelText("Switching threshold for First (%)"),
    "40",
  );
  await user.type(screen.getByLabelText("Native priority for First"), "-5");
  await user.click(screen.getByLabelText("Use remaining quota for First"));
  screen.getByText(
    "The effective switching threshold is 0%. The entered threshold of 40% is kept.",
  );
  await user.click(save());
  expect(lastRequest(mutation).members[0]!.nativeOptions).toMatchObject({
    autoSwitchThreshold: 40,
    priority: -5,
    useRemainingQuota: true,
    allowCredits: false,
  });
  // Pool-level options stay absent when only a member override exists.
  expect(lastRequest(mutation).nativeOptions).toBeUndefined();
});

it("flags member threshold and native priority range errors on their fields", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    family("anthropic"),
    rs.fn((_r: unknown) => ({})),
  );
  await openCreate(user, ["c1"], false);
  await user.click(screen.getByLabelText("Override native options for First"));
  const threshold = screen.getByLabelText("Switching threshold for First (%)");
  const priority = screen.getByLabelText("Native priority for First");
  for (const bad of ["101", "-1"]) {
    await user.clear(threshold);
    await user.type(threshold, bad);
    expect(threshold.getAttribute("aria-invalid")).toBe("true");
    expect(saveDisabled()).toBe(true);
  }
  await user.clear(threshold);
  for (const bad of ["101", "-101"]) {
    await user.clear(priority);
    await user.type(priority, bad);
    expect(priority.getAttribute("aria-invalid")).toBe("true");
    expect(saveDisabled()).toBe(true);
  }
  for (const ok of ["100", "-100"]) {
    await user.clear(priority);
    await user.type(priority, ok);
    expect(priority.getAttribute("aria-invalid")).toBeNull();
    expect(saveDisabled()).toBe(false);
  }
});

it("keeps rules and quota settings across an edit round trip of an Anthropic 2 pool", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  const pool = create(PoolSchema, {
    ...savedWithOverrides("anthropic2"),
    nativeOptions: create(NativePoolOptionsSchema, {
      quotaWindow: NativeQuotaWindow.MAX_UTILIZATION,
      nativeMessages: false,
      routes: [
        {
          name: "B",
          match: "b-*",
          connectionIds: ["c2", "c1"],
          fallback: true,
        },
        { name: "A", match: "a-*", connectionIds: ["c1"], fallback: false },
      ],
    }),
  });
  mount(pool, family("anthropic2"), mutation);
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  await user.click(save());
  expect(lastRequest(mutation).nativeOptions).toMatchObject({
    quotaWindow: NativeQuotaWindow.MAX_UTILIZATION,
    nativeMessages: false,
    routes: [
      { name: "B", connectionIds: ["c2", "c1"], fallback: true },
      { name: "A", connectionIds: ["c1"], fallback: false },
    ],
  });
});

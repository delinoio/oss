// SPDX-License-Identifier: Apache-2.0
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { it, expect, rs } from "@rstest/core";
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
  NativePinnedTransientPolicy,
  NativePoolOptionsSchema,
  type Pool,
} from "@tairouter/client";
import { ApiProvider } from "../src/api";
import { PoolsPage } from "../src/routing";
function mount(
  pool: Pool | undefined,
  provider: string,
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
        connections: [
          {
            id: "one",
            name: "First",
            providerId: provider,
            authKind: ProviderAuthKind.OAUTH,
            state: ConnectionState.READY,
          },
          {
            id: "two",
            name: "Second",
            providerId: provider,
            authKind: ProviderAuthKind.OAUTH,
            state: ConnectionState.READY,
          },
          {
            id: "other",
            name: "Other provider",
            providerId: provider === "openai" ? "anthropic" : "openai",
            authKind: ProviderAuthKind.OAUTH,
            state: ConnectionState.READY,
          },
        ],
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
async function start(user: ReturnType<typeof userEvent.setup>) {
  await user.click(screen.getByRole("button", { name: "Create pool" }));
  await user.type(screen.getByLabelText("Pool name"), "Native pool");
  await user.selectOptions(
    await screen.findByLabelText("Add a connection"),
    "one",
  );
  await user.click(screen.getByText("Native OAuth pool settings"));
  await user.click(
    screen.getByLabelText("Configure pool-level native settings"),
  );
}
it.each(["", "false"])(
  "creates Codex options preserving enabled %s and member zero/false values",
  async (enabled) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    mount(undefined, "openai", mutation);
    await start(user);
    await user.selectOptions(screen.getByLabelText("Native pooling"), enabled);
    await user.type(screen.getByLabelText("Upstream failover threshold"), "0");
    await user.selectOptions(screen.getByLabelText("Failure window"), "false");
    await user.selectOptions(
      screen.getByLabelText("Pinned transient policy"),
      String(NativePinnedTransientPolicy.DETOUR_NEW_THREADS),
    );
    await user.selectOptions(
      screen.getByLabelText("Native selection strategy"),
      String(NativePoolStrategy.RESET_FIRST),
    );
    await user.type(screen.getByLabelText("Pool switching threshold (%)"), "0");
    await user.selectOptions(screen.getByLabelText("Cache affinity"), "false");
    await user.type(screen.getByLabelText("Excluded plans"), "Free\nTrial");
    await user.click(
      screen.getByLabelText("Override native options for First"),
    );
    await user.type(
      screen.getByLabelText("Switching threshold for First (%)"),
      "0",
    );
    await user.type(screen.getByLabelText("Native priority for First"), "-100");
    await user.click(screen.getByLabelText("Use remaining quota for First"));
    await user.click(screen.getByLabelText("Allow credits for First"));
    await user.click(screen.getByRole("button", { name: "Save pool" }));
    const request = mutation.mock.calls[0]?.[0] as {
      nativeOptions: { enabled?: boolean; stickyLimit?: number };
    };
    expect(request).toMatchObject({
      organizationId: "org",
      nativeOptions: {
        strategy: NativePoolStrategy.RESET_FIRST,
        upstreamFailoverThreshold: 0,
        failureWindow: false,
        pinnedTransientPolicy: NativePinnedTransientPolicy.DETOUR_NEW_THREADS,
        autoSwitchThreshold: 0,
        cacheAffinity: false,
        excludedPlans: ["Free", "Trial"],
      },
      members: [
        {
          nativeOptions: {
            autoSwitchThreshold: 0,
            priority: -100,
            useRemainingQuota: true,
            allowCredits: true,
          },
        },
      ],
    });
    expect(request.nativeOptions.enabled).toBe(
      enabled === "" ? undefined : false,
    );
    expect(request.nativeOptions.stickyLimit).toBeUndefined();
  },
);
const saved = create(PoolSchema, {
  id: "pool",
  name: "Saved native",
  strategy: PoolStrategy.PRIORITY,
  affinityEnabled: true,
  members: [
    {
      connectionId: "one",
      priority: 0,
      weight: 1,
      nativeOptions: {
        autoSwitchThreshold: 0,
        priority: 100,
        useRemainingQuota: false,
      },
    },
    { connectionId: "two", priority: 1, weight: 1 },
  ],
  nativeOptions: {
    quotaWindow: NativeQuotaWindow.MAX_UTILIZATION,
    nativeMessages: false,
    stickyLimit: 2,
    routes: [
      {
        name: "Claude",
        match: "claude-*",
        connectionIds: ["two", "one"],
        fallback: false,
      },
    ],
  },
});
it("preserves imported absent enablement, false native messages and rule/member order on edit", async () => {
  const mutation = rs.fn((_r: unknown) => ({}));
  mount(saved, "anthropic2", mutation);
  await userEvent.click(
    await screen.findByRole("button", { name: "Edit pool" }),
  );
  await userEvent.click(screen.getByRole("button", { name: "Save pool" }));
  const request = mutation.mock.calls[0]?.[0] as {
    nativeOptions: { enabled?: boolean; cacheAffinity?: boolean };
    members: { nativeOptions?: unknown }[];
  };
  expect(request).toMatchObject({
    poolId: "pool",
    affinityEnabled: true,
    nativeOptions: saved.nativeOptions,
    members: [
      { nativeOptions: saved.members[0].nativeOptions },
      { connectionId: "two" },
    ],
  });
  expect(request.nativeOptions.enabled).toBeUndefined();
  expect(request.nativeOptions.cacheAffinity).toBeUndefined();
  expect(request.members[1].nativeOptions).toBeUndefined();
});
it("clears native pool and member options without changing generic affinity", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(saved, "anthropic", mutation);
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  await user.click(screen.getByText("Native OAuth pool settings"));
  await user.click(
    screen.getByRole("button", { name: "Clear all native settings" }),
  );
  await user.click(screen.getByRole("button", { name: "Save pool" }));
  const request = mutation.mock.calls[0]?.[0] as {
    nativeOptions?: unknown;
    affinityEnabled: boolean;
    members: { nativeOptions?: unknown }[];
  };
  expect(request.nativeOptions).toBeUndefined();
  expect(request.members.every((m) => m.nativeOptions === undefined)).toBe(
    true,
  );
  expect(request.affinityEnabled).toBe(true);
});
it("creates ordered Anthropic model rules and rejects duplicate patterns", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, "anthropic", mutation);
  await start(user);
  expect(screen.queryByLabelText("Upstream failover threshold")).toBeNull();
  expect(screen.queryByLabelText("Failure window")).toBeNull();
  expect(screen.queryByLabelText("Pinned transient policy")).toBeNull();
  await user.selectOptions(
    screen.getByLabelText("Native quota window"),
    String(NativeQuotaWindow.WEEKLY),
  );
  await user.selectOptions(screen.getByLabelText("Native messages"), "false");
  await user.click(screen.getByRole("button", { name: "Add model rule" }));
  await user.type(screen.getByLabelText("Rule name 1"), "Coding");
  await user.type(screen.getByLabelText("Model pattern 1"), "claude-*");
  await user.selectOptions(
    screen.getByLabelText("Add rule connection 1"),
    "one",
  );
  await user.click(screen.getByRole("button", { name: "Add model rule" }));
  await user.type(screen.getByLabelText("Rule name 2"), "Duplicate");
  await user.type(screen.getByLabelText("Model pattern 2"), "claude-*");
  await user.selectOptions(
    screen.getByLabelText("Add rule connection 2"),
    "one",
  );
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(true);
  await user.click(screen.getByRole("button", { name: "Remove model rule 2" }));
  await user.click(screen.getByRole("button", { name: "Save pool" }));
  expect(mutation.mock.calls[0]?.[0]).toMatchObject({
    nativeOptions: {
      nativeMessages: false,
      quotaWindow: NativeQuotaWindow.WEEKLY,
      routes: [
        {
          name: "Coding",
          match: "claude-*",
          connectionIds: ["one"],
          fallback: false,
        },
      ],
    },
  });
});
it("blocks mixed-provider native settings until explicitly cleared", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, "openai", mutation);
  await start(user);
  await user.selectOptions(screen.getByLabelText("Add a connection"), "other");
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(true);
  expect(mutation).not.toHaveBeenCalled();
  await user.click(
    screen.getByRole("button", { name: "Clear all native settings" }),
  );
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(false);
});

it.each(["preserve", "clear"])(
  "%s optional Codex health settings during full replacement",
  async (mode) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    const pool = create(PoolSchema, {
      ...saved,
      nativeOptions: create(NativePoolOptionsSchema, {
        enabled: false,
        upstreamFailoverThreshold: 0,
        failureWindow: false,
        pinnedTransientPolicy: NativePinnedTransientPolicy.KEEP,
      }),
    });
    mount(pool, "openai", mutation);
    await user.click(await screen.findByRole("button", { name: "Edit pool" }));
    if (mode === "clear") {
      await user.click(screen.getByText("Native OAuth pool settings"));
      await user.clear(screen.getByLabelText("Upstream failover threshold"));
      await user.selectOptions(screen.getByLabelText("Failure window"), "");
      await user.selectOptions(
        screen.getByLabelText("Pinned transient policy"),
        "",
      );
    }
    await user.click(screen.getByRole("button", { name: "Save pool" }));
    const request = mutation.mock.calls[0]?.[0] as {
      nativeOptions: {
        enabled?: boolean;
        upstreamFailoverThreshold?: number;
        failureWindow?: boolean;
        pinnedTransientPolicy?: number;
      };
    };
    expect(request.nativeOptions.enabled).toBe(false);
    expect(request.nativeOptions.upstreamFailoverThreshold).toBe(
      mode === "clear" ? undefined : 0,
    );
    expect(request.nativeOptions.failureWindow).toBe(
      mode === "clear" ? undefined : false,
    );
    expect(request.nativeOptions.pinnedTransientPolicy).toBe(
      mode === "clear" ? undefined : NativePinnedTransientPolicy.KEEP,
    );
  },
);
it("blocks out-of-range health settings", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, "openai", mutation);
  await start(user);
  await user.type(screen.getByLabelText("Upstream failover threshold"), "21");
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(true);
  await user.clear(screen.getByLabelText("Upstream failover threshold"));
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(false);
});
it("rejects explicit Codex health zero/false/unspecified in an Anthropic pool until cleared", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  const pool = create(PoolSchema, {
    ...saved,
    nativeOptions: create(NativePoolOptionsSchema, {
      ...saved.nativeOptions!,
      upstreamFailoverThreshold: 0,
      failureWindow: false,
      pinnedTransientPolicy: NativePinnedTransientPolicy.UNSPECIFIED,
    }),
  });
  mount(pool, "anthropic", mutation);
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  // The blocking error opens the disclosure without a click.
  await waitFor(() => expect(nativeDetails().open).toBe(true));
  expect(screen.queryByLabelText("Failure window")).toBeNull();
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(true);
  expect(mutation).not.toHaveBeenCalled();
  await screen.findByText(
    "Upstream failover, failure window, and pinned transient policy require a Codex pool. Clear incompatible settings before saving.",
  );
  await user.click(
    screen.getByRole("button", { name: "Clear all native settings" }),
  );
  await user.click(screen.getByRole("button", { name: "Save pool" }));
  expect(
    (mutation.mock.calls[0]?.[0] as { nativeOptions?: unknown }).nativeOptions,
  ).toBeUndefined();
});

const createNote =
  "Clearing removes draft native settings. Creating an eligible OAuth pool enables native pooling by default; configure Native pooling as Disabled to opt out. Generic affinity is unchanged.";
const editNote =
  "Saving replaces all native pool and member options. Clearing them removes saved settings; it does not change generic affinity.";
const nativeDetails = () =>
  screen.getByText("Native OAuth pool settings").closest("details")!;
const providers = ["openai", "anthropic", "anthropic2"];

async function startCreate(
  user: ReturnType<typeof userEvent.setup>,
  open: boolean,
) {
  await user.click(screen.getByRole("button", { name: "Create pool" }));
  await user.type(screen.getByLabelText("Pool name"), "Native pool");
  await user.selectOptions(
    await screen.findByLabelText("Add a connection"),
    "one",
  );
  if (open) await user.click(screen.getByText("Native OAuth pool settings"));
}

it("keeps one closed native disclosure and the create note visible while unchecked", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    "openai",
    rs.fn((_r: unknown) => ({})),
  );
  await startCreate(user, false);
  expect(document.querySelectorAll("details")).toHaveLength(1);
  expect(nativeDetails().open).toBe(false);
  expect(screen.getByText(createNote)).toBeTruthy();
  await user.click(screen.getByText("Native OAuth pool settings"));
  expect(nativeDetails().open).toBe(true);
  expect(
    (
      screen.getByLabelText(
        "Configure pool-level native settings",
      ) as HTMLInputElement
    ).checked,
  ).toBe(false);
  expect(screen.getByText(createNote)).toBeTruthy();
  // Collapse and expand never submit or clear anything.
  await user.click(screen.getByText("Native OAuth pool settings"));
  expect(nativeDetails().open).toBe(false);
  expect(screen.getByText(createNote)).toBeTruthy();
});

it("shows the edit note and the enablement help for an unspecified saved value", async () => {
  const user = userEvent.setup();
  mount(
    saved,
    "anthropic2",
    rs.fn((_r: unknown) => ({})),
  );
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  expect(nativeDetails().open).toBe(false);
  expect(screen.getByText(editNote)).toBeTruthy();
  expect(screen.queryByText(createNote)).toBeNull();
  await user.click(screen.getByText("Native OAuth pool settings"));
  expect(
    screen.getByText(
      "An unspecified saved value is disabled. Choose Enabled to opt in.",
    ),
  ).toBeTruthy();
});

it("explains create enablement in the Native pooling help", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    "openai",
    rs.fn((_r: unknown) => ({})),
  );
  await startCreate(user, true);
  await user.click(
    screen.getByLabelText("Configure pool-level native settings"),
  );
  expect(
    screen.getByText(
      "Eligible OAuth pools enable native pooling on create, even without pool-level settings. Keep configuration checked and choose Disabled to opt out.",
    ),
  ).toBeTruthy();
});

it("opens the disclosure on a blocking error and links the summary to it", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    "openai",
    rs.fn((_r: unknown) => ({})),
  );
  await startCreate(user, true);
  await user.click(
    screen.getByLabelText("Configure pool-level native settings"),
  );
  // Collapsing keeps the draft; the disclosure never clears values.
  await user.click(screen.getByText("Native OAuth pool settings"));
  expect(nativeDetails().open).toBe(false);
  await user.selectOptions(screen.getByLabelText("Add a connection"), "other");
  await waitFor(() => expect(nativeDetails().open).toBe(true));
  const summary = await screen.findByTestId("error-summary");
  expect(summary.textContent).toContain(
    "Native OAuth pool settings: Native settings require OAuth connections from one provider",
  );
  await user.click(
    within(summary).getByRole("link", { name: /Native OAuth pool settings:/ }),
  );
  expect(document.activeElement?.textContent).toBe(
    "Native OAuth pool settings",
  );
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(true);
  // The user may collapse it again; it stays collapsed until the next change.
  await user.click(screen.getByText("Native OAuth pool settings"));
  expect(nativeDetails().open).toBe(false);
});

it("auto-opens an edit pool that already contains an incompatible native setting", async () => {
  const pool = create(PoolSchema, {
    ...saved,
    nativeOptions: create(NativePoolOptionsSchema, {
      ...saved.nativeOptions!,
      stickyLimit: 101,
    }),
  });
  const user = userEvent.setup();
  mount(
    pool,
    "anthropic",
    rs.fn((_r: unknown) => ({})),
  );
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  await waitFor(() => expect(nativeDetails().open).toBe(true));
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(true);
  expect(screen.getByText("Enter a whole number from 1 to 100.")).toBeTruthy();
});

it("shows the ineligible guidance and keeps presence unchecked and disabled", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    "openai",
    rs.fn((_r: unknown) => ({})),
  );
  await user.click(screen.getByRole("button", { name: "Create pool" }));
  await user.click(screen.getByText("Native OAuth pool settings"));
  expect(
    screen.getByText(
      "Add connections from one supported OAuth provider to configure native pooling.",
    ),
  ).toBeTruthy();
  expect(
    (
      screen.getByLabelText(
        "Configure pool-level native settings",
      ) as HTMLInputElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByText(createNote)).toBeTruthy();
});

it("flags sticky limit and switching threshold range errors on their fields", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    "openai",
    rs.fn((_r: unknown) => ({})),
  );
  await startCreate(user, true);
  await user.click(
    screen.getByLabelText("Configure pool-level native settings"),
  );
  const sticky = screen.getByLabelText("Sticky account limit");
  const threshold = screen.getByLabelText("Pool switching threshold (%)");
  await user.type(sticky, "101");
  expect(sticky.getAttribute("aria-invalid")).toBe("true");
  await user.clear(sticky);
  await user.type(sticky, "0");
  expect(sticky.getAttribute("aria-invalid")).toBe("true");
  await user.clear(sticky);
  await user.type(sticky, "100");
  expect(sticky.getAttribute("aria-invalid")).toBeNull();
  await user.type(threshold, "101");
  expect(threshold.getAttribute("aria-invalid")).toBe("true");
  expect(
    screen.getByRole("button", { name: "Save pool" }).hasAttribute("disabled"),
  ).toBe(true);
  await user.clear(threshold);
  await user.type(threshold, "0");
  expect(threshold.getAttribute("aria-invalid")).toBeNull();
  await user.click(screen.getByRole("button", { name: "Save pool" }));
});

it.each(providers)(
  "%s create: unchecking or clearing sends absent options and Disabled stays a present object",
  async (provider) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    mount(undefined, provider, mutation);
    await startCreate(user, true);
    const configure = screen.getByLabelText(
      "Configure pool-level native settings",
    );
    // Check then uncheck: pool object absent, member override survives.
    await user.click(configure);
    await user.click(
      screen.getByLabelText("Override native options for First"),
    );
    await user.type(screen.getByLabelText("Native priority for First"), "-5");
    await user.click(configure);
    expect(
      (
        screen.getByLabelText(
          "Override native options for First",
        ) as HTMLInputElement
      ).checked,
    ).toBe(true);
    await user.click(screen.getByRole("button", { name: "Save pool" }));
    let request = mutation.mock.calls[0]?.[0] as {
      nativeOptions?: unknown;
      affinityEnabled: boolean;
      members: { nativeOptions?: { priority?: number } }[];
    };
    expect(request.nativeOptions).toBeUndefined();
    expect(request.members[0]?.nativeOptions?.priority).toBe(-5);
  },
);

it.each(providers)(
  "%s create: Clear all removes pool and member objects, and Disabled sends enabled=false",
  async (provider) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    mount(undefined, provider, mutation);
    await startCreate(user, true);
    await user.click(
      screen.getByLabelText("Configure pool-level native settings"),
    );
    await user.click(
      screen.getByLabelText("Override native options for First"),
    );
    await user.click(
      screen.getByRole("button", { name: "Clear all native settings" }),
    );
    expect(
      screen.queryByRole("button", { name: "Clear all native settings" }),
    ).toBeNull();
    expect(screen.getByText(createNote)).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Save pool" }));
    const cleared = mutation.mock.calls[0]?.[0] as {
      nativeOptions?: unknown;
      members: { nativeOptions?: unknown }[];
    };
    expect(cleared.nativeOptions).toBeUndefined();
    expect(cleared.members[0]?.nativeOptions).toBeUndefined();
  },
);

it.each(providers)(
  "%s create: opting out keeps a present object with enabled=false",
  async (provider) => {
    const user = userEvent.setup(),
      mutation = rs.fn((_r: unknown) => ({}));
    mount(undefined, provider, mutation);
    await startCreate(user, true);
    await user.click(
      screen.getByLabelText("Configure pool-level native settings"),
    );
    await user.selectOptions(screen.getByLabelText("Native pooling"), "false");
    await user.click(screen.getByRole("button", { name: "Save pool" }));
    const request = mutation.mock.calls[0]?.[0] as {
      nativeOptions?: { enabled?: boolean };
    };
    expect(request.nativeOptions).toBeDefined();
    expect(request.nativeOptions?.enabled).toBe(false);
  },
);

it.each(providers)(
  "%s update: unchecking drops only the pool object and Clear all drops both",
  async (provider) => {
    for (const mode of ["uncheck", "clear"]) {
      const user = userEvent.setup(),
        mutation = rs.fn((_r: unknown) => ({}));
      mount(saved, provider, mutation);
      await user.click(
        await screen.findByRole("button", { name: "Edit pool" }),
      );
      await user.click(screen.getByText("Native OAuth pool settings"));
      if (mode === "uncheck")
        await user.click(
          screen.getByLabelText("Configure pool-level native settings"),
        );
      else
        await user.click(
          screen.getByRole("button", { name: "Clear all native settings" }),
        );
      expect(screen.getByText(editNote)).toBeTruthy();
      await user.click(screen.getByRole("button", { name: "Save pool" }));
      const request = mutation.mock.calls[0]?.[0] as {
        poolId: string;
        affinityEnabled: boolean;
        nativeOptions?: unknown;
        members: { nativeOptions?: unknown; weight: number }[];
      };
      expect(request.poolId).toBe("pool");
      expect(request.affinityEnabled).toBe(true);
      expect(request.nativeOptions).toBeUndefined();
      if (mode === "uncheck")
        expect(request.members[0]?.nativeOptions).toMatchObject({
          autoSwitchThreshold: 0,
          priority: 100,
        });
      else
        expect(
          request.members.every((m) => m.nativeOptions === undefined),
        ).toBe(true);
      cleanup();
    }
  },
);

const thresholdHelp =
  "Empty uses 3. Zero disables consecutive-failure avoidance only.";
const windowHelp =
  "Default: sliding 60-second failure ratio. Steering is independent of the threshold.";
const openaiSaved = create(PoolSchema, {
  id: "pool",
  name: "Saved codex",
  strategy: PoolStrategy.PRIORITY,
  members: [
    { connectionId: "one", priority: 0, weight: 1 },
    { connectionId: "two", priority: 1, weight: 1 },
  ],
  nativeOptions: {
    enabled: true,
    upstreamFailoverThreshold: 0,
    failureWindow: true,
    pinnedTransientPolicy: NativePinnedTransientPolicy.UNSPECIFIED,
    cacheAffinity: false,
    excludedPlans: ["Free", "Trial"],
  },
});
const describedBy = (element: HTMLElement) =>
  document.getElementById(element.getAttribute("aria-describedby")!)
    ?.textContent;

it("PC1: shows the corrected Codex health copy in its own group and serializes 0 and true unchanged", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(openaiSaved, "openai", mutation);
  await user.click(await screen.findByRole("button", { name: "Edit pool" }));
  await user.click(await screen.findByText("Native OAuth pool settings"));
  const group = screen
    .getByText("Codex failover and affinity")
    .closest("fieldset")!;
  const threshold = within(group).getByLabelText("Upstream failover threshold");
  const windowSelect = within(group).getByLabelText("Failure window");
  expect(describedBy(threshold)).toBe(thresholdHelp);
  expect(describedBy(windowSelect)).toBe(windowHelp);
  // No copy may claim that zero stops all failure-based switching.
  expect(
    screen.queryByText(/disables (upstream-)?failure-based failover/i),
  ).toBeNull();
  expect(screen.queryByText(/Disabled uses only the consecutive/i)).toBeNull();
  expect((threshold as HTMLInputElement).value).toBe("0");
  expect((windowSelect as HTMLSelectElement).value).toBe("true");
  await user.click(screen.getByRole("button", { name: "Save pool" }));
  const request = mutation.mock.calls[0]?.[0] as {
    poolId: string;
    nativeOptions: {
      upstreamFailoverThreshold?: number;
      failureWindow?: boolean;
      pinnedTransientPolicy?: number;
      cacheAffinity?: boolean;
      excludedPlans: string[];
    };
  };
  expect(request.poolId).toBe("pool");
  expect(request.nativeOptions.upstreamFailoverThreshold).toBe(0);
  expect(request.nativeOptions.failureWindow).toBe(true);
  expect(request.nativeOptions.pinnedTransientPolicy).toBe(
    NativePinnedTransientPolicy.UNSPECIFIED,
  );
  expect(request.nativeOptions.cacheAffinity).toBe(false);
  expect(request.nativeOptions.excludedPlans).toEqual(["Free", "Trial"]);
});

it("PC1: keeps failureWindow=false with threshold 0 and the pinned policy distinctions", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, "openai", mutation);
  await start(user);
  const policy = screen.getByLabelText("Pinned transient policy");
  expect(
    within(policy).queryByRole("option", { name: "Unspecified (keep)" }),
  ).toBeNull();
  await user.type(screen.getByLabelText("Upstream failover threshold"), "0");
  await user.selectOptions(screen.getByLabelText("Failure window"), "false");
  await user.selectOptions(policy, String(NativePinnedTransientPolicy.KEEP));
  await user.click(screen.getByRole("button", { name: "Save pool" }));
  const first = mutation.mock.calls[0]?.[0] as {
    nativeOptions: {
      upstreamFailoverThreshold?: number;
      failureWindow?: boolean;
      pinnedTransientPolicy?: number;
    };
  };
  expect(first.nativeOptions.upstreamFailoverThreshold).toBe(0);
  expect(first.nativeOptions.failureWindow).toBe(false);
  expect(first.nativeOptions.pinnedTransientPolicy).toBe(
    NativePinnedTransientPolicy.KEEP,
  );
});

it("validates excluded plans with a field error and keeps the values as typed", async () => {
  const user = userEvent.setup(),
    mutation = rs.fn((_r: unknown) => ({}));
  mount(undefined, "openai", mutation);
  await start(user);
  const plans = screen.getByLabelText("Excluded plans");
  const save = () => screen.getByRole("button", { name: "Save pool" });
  const invalid = () => plans.getAttribute("aria-invalid") === "true";
  await user.type(plans, "Free{Enter}Trial");
  expect(invalid()).toBe(false);
  expect(save().hasAttribute("disabled")).toBe(false);
  // Case-insensitive duplicate.
  await user.type(plans, "{Enter}free");
  expect(invalid()).toBe(true);
  expect(
    screen.getByText(
      "Line 3 repeats line 1. Plan names are compared case-insensitively.",
    ),
  ).toBeTruthy();
  expect(save().hasAttribute("disabled")).toBe(true);
  // Blank line.
  await user.clear(plans);
  await user.type(plans, "Free{Enter}{Enter}Trial");
  expect(screen.getByText("Line 2 is blank. Remove empty lines.")).toBeTruthy();
  expect(save().hasAttribute("disabled")).toBe(true);
  // 128 characters pass, 129 fail.
  await user.clear(plans);
  await user.click(plans);
  await user.paste("x".repeat(128));
  expect(invalid()).toBe(false);
  await user.paste("y");
  expect(
    screen.getByText("Line 1 is longer than 128 characters."),
  ).toBeTruthy();
  expect(save().hasAttribute("disabled")).toBe(true);
  // 100 plans pass, 101 fail.
  await user.clear(plans);
  await user.click(plans);
  await user.paste(Array.from({ length: 100 }, (_, i) => `p${i}`).join("\n"));
  expect(invalid()).toBe(false);
  await user.paste("\np100");
  expect(screen.getByText("Use at most 100 excluded plans.")).toBeTruthy();
  expect(save().hasAttribute("disabled")).toBe(true);
  // Fixing it saves the plans exactly as typed, without case folding.
  await user.clear(plans);
  await user.type(plans, "Free{Enter}Trial");
  await user.click(save());
  const request = mutation.mock.calls[0]?.[0] as {
    nativeOptions: { excludedPlans: string[] };
  };
  expect(request.nativeOptions.excludedPlans).toEqual(["Free", "Trial"]);
});

it("hides the Codex group and its excluded plans for Anthropic pools", async () => {
  const user = userEvent.setup();
  mount(
    undefined,
    "anthropic",
    rs.fn((_r: unknown) => ({})),
  );
  await start(user);
  expect(screen.queryByText("Codex failover and affinity")).toBeNull();
  expect(screen.queryByLabelText("Excluded plans")).toBeNull();
});

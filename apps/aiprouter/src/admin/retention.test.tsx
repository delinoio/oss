// SPDX-License-Identifier: Apache-2.0
import { expect, it, rs } from "@rstest/core";
import {
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  OperatorService,
  type InstanceSettings,
  type UpdateInstanceSettingsRequest,
} from "@aiprouter/client";
import { ApiProvider, DirtyContext } from "../api";
import { InstanceConfiguration } from "./operator";

const destination = {
  id: "stable-rule",
  origin: "https://api.example.test",
  allowedCidrs: ["192.0.2.0/24", "2001:db8::/32"],
};
const emptyDestination = {
  id: "empty-rule",
  origin: "https://empty.example.test",
  allowedCidrs: [],
};
const limits = { requestsPerMinute: 120, concurrentRequests: 10 };

type Options = {
  failure?: Code;
  missingRetention?: boolean;
  noSettings?: boolean;
  settingsFailure?: boolean;
  delay?: Promise<void>;
  operator?: boolean;
};

function fixture(options: Options = {}) {
  let revision = "1";
  let retention = { requestDays: 30, aggregateDays: 365, auditDays: 90 };
  let fresh = { ...limits };
  let freshDestinations = [destination, emptyDestination];
  let reloadFails = false;
  let operator = options.operator ?? true;
  const update = rs.fn<(r: UpdateInstanceSettingsRequest) => void>();
  const fetches = rs.fn<() => void>();
  const dirty = rs.fn<(dirty: boolean) => void>();
  const settings = () =>
    ({
      revision,
      requestLimits: fresh,
      retention: options.missingRetention ? undefined : retention,
      allowedDestinations: freshDestinations,
    }) as unknown as InstanceSettings;
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: operator }),
    });
    router.service(OperatorService, {
      getInstanceSettings: () => {
        fetches();
        if (options.settingsFailure || reloadFails)
          throw new ConnectError("down", Code.Unavailable);
        return { settings: options.noSettings ? undefined : settings() };
      },
      updateInstanceSettings: async (request) => {
        update(request);
        await options.delay;
        if (options.failure !== undefined)
          throw new ConnectError("Rejected", options.failure);
        return { settings: { ...request.settings!, revision: "9" } };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <DirtyContext.Provider value={dirty}>
        <InstanceConfiguration section="retention" />
      </DirtyContext.Provider>
    </ApiProvider>,
  );
  return {
    update,
    fetches,
    dirty,
    client,
    // Simulates another operator saving revision 2 with different values.
    advance: () => {
      revision = "2";
      fresh = { requestsPerMinute: 777, concurrentRequests: 77 };
      freshDestinations = [{ ...destination, allowedCidrs: [] }];
      retention = { requestDays: 20, aggregateDays: 400, auditDays: 100 };
    },
    failReload: (value: boolean) => {
      reloadFails = value;
    },
    setOperator: (value: boolean) => {
      operator = value;
    },
  };
}
type User = ReturnType<typeof userEvent.setup>;
const names = {
  request: "Request metadata (days)",
  aggregate: "Daily aggregates (days)",
  audit: "Audit records (days)",
};
const field = (name: string) =>
  screen.findByRole("spinbutton", { name }) as Promise<HTMLInputElement>;
async function replace(user: User, input: HTMLElement, value: string) {
  await user.clear(input);
  if (value) await user.type(input, value);
}
const saveButton = () =>
  screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement;
const review = () => screen.getByRole("button", { name: "Review reductions" });
async function reduce(user: User) {
  await replace(user, await field(names.request), "14");
  await replace(
    user,
    screen.getByRole("spinbutton", { name: names.audit }),
    "60",
  );
}

it("renders the retention page with saved values, helpers and a disabled save", async () => {
  fixture();
  const request = await field(names.request);
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(
    screen.getByRole("heading", { level: 1, name: "Retention" }),
  ).toBeTruthy();
  expect(
    screen.getByText("Set how long instance records remain available."),
  ).toBeTruthy();
  expect(screen.getByText("Instance policy")).toBeTruthy();
  expect(
    screen.getByRole("heading", { level: 2, name: "Record retention" }),
  ).toBeTruthy();
  expect(
    screen.getByText("Enter whole days within each allowed range."),
  ).toBeTruthy();
  const inputs = screen.getAllByRole("spinbutton") as HTMLInputElement[];
  expect(inputs.map((input) => input.getAttribute("aria-label") ?? "")).toEqual(
    ["", "", ""],
  );
  expect(inputs.map((i) => i.value)).toEqual(["30", "365", "90"]);
  expect(request.required).toBe(true);
  expect([request.min, request.max, request.step]).toEqual(["1", "90", "1"]);
  expect(inputs[1]!.max).toBe("730");
  expect(inputs[2]!.max).toBe("365");
  for (const text of [
    "Request metadata",
    "Daily aggregates",
    "Audit records",
    "1–90 days · Default: 30",
    "30–730 days · Default: 365",
    "30–365 days · Default: 90",
    "Saved: 30 days",
    "Saved: 365 days",
    "Saved: 90 days",
    "Before reducing retention",
  ])
    expect(screen.getByText(text)).toBeTruthy();
  expect(screen.getAllByText("days")).toHaveLength(3);
  expect(
    screen.getByText(
      "Records older than the new limits become unavailable and are eligible for deletion. Existing backups follow operator policy.",
    ),
  ).toBeTruthy();
  expect(screen.getByText("No unsaved changes")).toBeTruthy();
  expect(saveButton().disabled).toBe(true);
  expect(screen.queryByRole("button", { name: /reload/i })).toBeNull();
  expect(screen.queryByText(/unlimited|reset|restore defaults/i)).toBeNull();
});

it("reviews reductions against the saved snapshot and sends one full-settings mutation", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await reduce(user);
  expect(
    screen.getByText("Unsaved changes · 2 reductions to review"),
  ).toBeTruthy();
  expect(f.update).not.toHaveBeenCalled();
  await user.click(review());
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).getByRole("heading", { name: "Reduce retention" }),
  ).toBeTruthy();
  expect(
    within(dialog).getByText("Review these changes before saving."),
  ).toBeTruthy();
  const headers = within(dialog)
    .getAllByRole("columnheader")
    .map((h) => h.textContent);
  expect(headers).toEqual(["Record type", "Saved", "New", "Change"]);
  const rows = within(dialog)
    .getAllByRole("row")
    .slice(1)
    .map((row) => Array.from(row.children).map((c) => c.textContent));
  expect(rows).toEqual([
    ["Request metadata", "30 days", "14 days", "Reduced"],
    ["Daily aggregates", "365 days", "365 days", "Unchanged"],
    ["Audit records", "90 days", "60 days", "Reduced"],
  ]);
  expect(
    within(dialog).getByText(
      "Records older than the new limits become unavailable and are eligible for deletion.",
    ),
  ).toBeTruthy();
  expect(
    within(dialog).getByText("Existing backups follow operator policy."),
  ).toBeTruthy();
  expect(document.activeElement).toBe(
    within(dialog).getByRole("button", { name: "Cancel" }),
  );
  expect(
    within(dialog).getByRole("button", { name: "Close dialog" }),
  ).toBeTruthy();
  await user.click(
    within(dialog).getByRole("button", { name: "Reduce retention" }),
  );
  await screen.findByText("Changes saved.");
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(f.update.mock.lastCall?.[0]).toMatchObject({
    expectedRevision: "1",
    settings: {
      requestLimits: limits,
      retention: { requestDays: 14, aggregateDays: 365, auditDays: 60 },
      allowedDestinations: [destination, emptyDestination],
    },
  });
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(screen.getByText("No unsaved changes")).toBeTruthy();
  expect(screen.getByText("Saved: 14 days")).toBeTruthy();
  expect(screen.getByText("Saved: 60 days")).toBeTruthy();
  expect(saveButton().disabled).toBe(true);
  expect(f.dirty).toHaveBeenLastCalledWith(false);
});

it.each([
  [
    "Cancel",
    async (user: User) =>
      user.click(
        within(screen.getByRole("dialog")).getByRole("button", {
          name: "Cancel",
        }),
      ),
  ],
  ["Escape", async (user: User) => user.keyboard("{Escape}")],
  [
    "Close dialog",
    async (user: User) =>
      user.click(
        within(screen.getByRole("dialog")).getByRole("button", {
          name: "Close dialog",
        }),
      ),
  ],
])(
  "dismisses the review with %s, keeps the draft and returns focus",
  async (_name, dismiss) => {
    const f = fixture();
    const user = userEvent.setup();
    await reduce(user);
    await user.click(review());
    await dismiss(user);
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(f.update).not.toHaveBeenCalled();
    expect(
      (
        screen.getByRole("spinbutton", {
          name: names.request,
        }) as HTMLInputElement
      ).value,
    ).toBe("14");
    expect(document.activeElement).toBe(review());
    // Reopening recomputes the comparison from the current draft.
    await replace(
      user,
      screen.getByRole("spinbutton", { name: names.audit }),
      "90",
    );
    expect(
      screen.getByText("Unsaved changes · 1 reduction to review"),
    ).toBeTruthy();
    await user.click(review());
    const rows = within(screen.getByRole("dialog")).getAllByRole("row");
    expect(rows[3]!.textContent).toContain("Unchanged");
  },
);

it("saves increase-only changes directly without a dialog", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await replace(user, await field(names.request), "40");
  expect(screen.getByText("Unsaved changes")).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Review reductions" }),
  ).toBeNull();
  await user.click(saveButton());
  expect(screen.queryByRole("dialog")).toBeNull();
  await screen.findByText("Changes saved.");
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(f.update.mock.lastCall?.[0]).toMatchObject({
    expectedRevision: "1",
    settings: {
      retention: { requestDays: 40, aggregateDays: 365, auditDays: 90 },
    },
  });
});

it("reviews mixed increases and reductions together in one mutation", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await replace(user, await field(names.request), "40");
  await replace(
    user,
    screen.getByRole("spinbutton", { name: names.audit }),
    "60",
  );
  expect(
    screen.getByText("Unsaved changes · 1 reduction to review"),
  ).toBeTruthy();
  await user.click(review());
  const rows = within(screen.getByRole("dialog")).getAllByRole("row").slice(1);
  expect(rows.map((r) => r.lastElementChild?.textContent)).toEqual([
    "Increased",
    "Unchanged",
    "Reduced",
  ]);
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Reduce retention",
    }),
  );
  await screen.findByText("Changes saved.");
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(f.update.mock.lastCall?.[0].settings?.retention).toMatchObject({
    requestDays: 40,
    auditDays: 60,
  });
});

it("disables save after every field is reverted to its saved value", async () => {
  const f = fixture();
  const user = userEvent.setup();
  const input = await field(names.request);
  await replace(user, input, "14");
  expect(f.dirty).toHaveBeenLastCalledWith(true);
  await replace(user, input, "30");
  expect(screen.getByText("No unsaved changes")).toBeTruthy();
  expect(saveButton().disabled).toBe(true);
  expect(f.dirty).toHaveBeenLastCalledWith(false);
  expect(f.update).not.toHaveBeenCalled();
});

it.each([
  [names.request, "1", true],
  [names.request, "90", true],
  [names.request, "91", false],
  [names.aggregate, "30", true],
  [names.aggregate, "730", true],
  [names.aggregate, "731", false],
  [names.audit, "30", true],
  [names.audit, "365", true],
  [names.audit, "366", false],
  [names.request, "", false],
  [names.request, "0", false],
  [names.request, "-1", false],
  [names.request, "1.5", false],
  [names.aggregate, "29", false],
  [names.audit, "29", false],
])("validates %s = %s (valid: %s)", async (name, value, valid) => {
  const f = fixture();
  const user = userEvent.setup();
  const input = await field(name);
  await replace(user, input, value);
  expect(input.value).toBe(value);
  if (valid) {
    expect(input.getAttribute("aria-invalid")).toBeNull();
    expect(screen.queryByRole("alert")).toBeNull();
    return;
  }
  expect(input.getAttribute("aria-invalid")).toBe("true");
  const message = {
    [names.request]: "Enter a whole number from 1 to 90 days.",
    [names.aggregate]: "Enter a whole number from 30 to 730 days.",
    [names.audit]: "Enter a whole number from 30 to 365 days.",
  }[name]!;
  const described = input.getAttribute("aria-describedby")!.split(" ");
  expect(
    described.some(
      (id) => document.getElementById(id)?.textContent === message,
    ),
  ).toBe(true);
  const summary = screen.getByRole("alert");
  const link = within(summary).getByRole("link", {
    name: name.replace(" (days)", ""),
  });
  await user.click(link);
  expect(document.activeElement).toBe(input);
  expect(
    (
      screen.getByRole("button", {
        name: /Save changes|Review reductions/,
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  fireEvent.submit(screen.getByRole("form", { name: "Record retention" }));
  await waitFor(() => expect(document.activeElement).toBe(summary));
  expect(f.update).not.toHaveBeenCalled();
});

it("keeps non-finite text editable and never submits it", async () => {
  const f = fixture();
  const input = await field(names.request);
  fireEvent.change(input, { target: { value: "Infinity" } });
  fireEvent.submit(screen.getByRole("form", { name: "Record retention" }));
  expect(f.update).not.toHaveBeenCalled();
  expect(
    screen.getByText("Enter a whole number from 1 to 90 days.", {
      selector: "p.admin-field-error",
    }),
  ).toBeTruthy();
});

it("shows loading, failure, empty and missing-retention states without defaults", async () => {
  fixture({ settingsFailure: true });
  expect(await screen.findByRole("button", { name: "Try again" })).toBeTruthy();
  expect(screen.queryByRole("spinbutton")).toBeNull();
});

it("renders an accessible loading state before values arrive", () => {
  fixture();
  const loading = screen.getByRole("status");
  expect(loading.getAttribute("aria-busy")).toBe("true");
  expect(loading.textContent).toBe("Loading…");
  expect(screen.queryByRole("spinbutton")).toBeNull();
});

it.each([
  ["no settings", { noSettings: true }],
  ["no retention", { missingRetention: true }],
] as const)(
  "shows unavailable settings with retry when there is %s",
  async (_name, options) => {
    const f = fixture(options);
    await screen.findByText("Instance settings are unavailable.");
    expect(screen.queryByRole("spinbutton")).toBeNull();
    expect(
      screen.queryByRole("button", { name: /Save changes|Review reductions/ }),
    ).toBeNull();
    const before = f.fetches.mock.calls.length;
    await userEvent
      .setup()
      .click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() =>
      expect(f.fetches.mock.calls.length).toBeGreaterThan(before),
    );
  },
);

it("sends a single mutation while pending and keeps the final result after dismissing", async () => {
  let release: () => void = () => {};
  const delay = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = fixture({ delay });
  const user = userEvent.setup();
  await reduce(user);
  await user.click(review());
  const dialog = screen.getByRole("dialog");
  await user.click(
    within(dialog).getByRole("button", { name: "Reduce retention" }),
  );
  const pendingAction = await within(dialog).findByRole("button", {
    name: "Saving…",
  });
  expect((pendingAction as HTMLButtonElement).disabled).toBe(true);
  await user.click(pendingAction);
  expect(
    screen.getAllByText(
      "Saving… Closing a dialog does not cancel a submitted change.",
    ).length,
  ).toBeGreaterThan(0);
  expect(screen.queryByText("Changes saved.")).toBeNull();
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Close dialog",
    }),
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: /Review reductions/,
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  release();
  await screen.findByText("Changes saved.");
  expect(f.update).toHaveBeenCalledTimes(1);
});

it("keeps the draft and reports failure without claiming success", async () => {
  const f = fixture({ failure: Code.Unavailable });
  const user = userEvent.setup();
  await reduce(user);
  await user.click(review());
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Reduce retention",
    }),
  );
  await within(screen.getByRole("dialog")).findByText(/service is unavailable/);
  expect(screen.queryByText("Changes saved.")).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Reload current settings" }),
  ).toBeNull();
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(
    (
      screen.getByRole("spinbutton", {
        name: names.request,
      }) as HTMLInputElement
    ).value,
  ).toBe("14");
});

it("keeps the draft and original revision across refetches and recovers only after an explicit reload", async () => {
  const f = fixture({ failure: Code.Aborted });
  const user = userEvent.setup();
  await reduce(user);
  f.advance();
  await f.client.invalidateQueries();
  expect(
    (
      screen.getByRole("spinbutton", {
        name: names.request,
      }) as HTMLInputElement
    ).value,
  ).toBe("14");
  expect(screen.getByText("Saved: 30 days")).toBeTruthy();
  await user.click(review());
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Reduce retention",
    }),
  );
  const dialog = screen.getByRole("dialog");
  await within(dialog).findByText(
    "This record changed. Refresh and review before saving again.",
  );
  expect(
    within(dialog).getByText(
      "Your retention draft is preserved. Reload the current settings and review your changes before submitting again.",
    ),
  ).toBeTruthy();
  expect(screen.queryByText(/network entries/)).toBeNull();
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(f.update.mock.lastCall?.[0].expectedRevision).toBe("1");
  // The conflicted review cannot resubmit the stale revision, even by form
  // submission, until an explicit reload and a new review.
  const blocked = within(dialog).getByRole("button", {
    name: "Reduce retention",
  }) as HTMLButtonElement;
  expect(blocked.disabled).toBe(true);
  fireEvent.submit(blocked.closest("form")!);
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(
    (screen.getByRole("spinbutton", { name: names.audit }) as HTMLInputElement)
      .value,
  ).toBe("60");

  // A failed reload keeps the original revision and draft, and stays blocked.
  f.failReload(true);
  await user.click(
    within(dialog).getByRole("button", { name: "Reload current settings" }),
  );
  await within(screen.getByRole("dialog")).findAllByText(
    /service is unavailable/,
  );
  expect(screen.getByRole("dialog")).toBeTruthy();
  await user.keyboard("{Escape}");
  expect(
    (
      screen.getByRole("button", {
        name: "Review reductions",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  expect(screen.getByText("Saved: 30 days")).toBeTruthy();

  // A successful reload adopts fresh untouched fields and requires a new review.
  f.failReload(false);
  await user.click(
    screen.getByRole("button", { name: "Reload current settings" }),
  );
  await screen.findByText(
    "Current settings loaded. Review your draft before saving.",
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(
    screen.queryByRole("button", { name: "Reload current settings" }),
  ).toBeNull();
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(
    (
      screen.getByRole("spinbutton", {
        name: names.request,
      }) as HTMLInputElement
    ).value,
  ).toBe("14");
  expect(
    (screen.getByRole("spinbutton", { name: names.audit }) as HTMLInputElement)
      .value,
  ).toBe("60");
  expect(screen.getByText("Saved: 20 days")).toBeTruthy();
  expect(screen.getByText("Saved: 400 days")).toBeTruthy();
  expect(screen.getByText("Saved: 100 days")).toBeTruthy();
  // Typed 14/365/60 against fresh 20/400/100: three reductions.
  expect(
    screen.getByText("Unsaved changes · 3 reductions to review"),
  ).toBeTruthy();
  await user.click(review());
  const rows = within(screen.getByRole("dialog")).getAllByRole("row").slice(1);
  expect(rows.map((r) => r.children[1]?.textContent)).toEqual([
    "20 days",
    "400 days",
    "100 days",
  ]);
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", {
      name: "Reduce retention",
    }),
  );
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(2));
  expect(f.update.mock.lastCall?.[0]).toMatchObject({
    expectedRevision: "2",
    settings: {
      requestLimits: { requestsPerMinute: 777, concurrentRequests: 77 },
      retention: { requestDays: 14, aggregateDays: 365, auditDays: 60 },
      allowedDestinations: [{ id: "stable-rule", allowedCidrs: [] }],
    },
  });
});

it("shows the reload action on the page when the conflict follows a direct save", async () => {
  const f = fixture({ failure: Code.Aborted });
  const user = userEvent.setup();
  await replace(user, await field(names.request), "40");
  await user.click(saveButton());
  await screen.findByText(
    "Your retention draft is preserved. Reload the current settings and review your changes before submitting again.",
  );
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(
    screen.getByRole("button", { name: "Reload current settings" }),
  ).toBeTruthy();
  expect(saveButton().disabled).toBe(true);
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(f.update).toHaveBeenCalledTimes(1);
});

it("preserves empty destination lists and request limits in the payload", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await replace(user, await field(names.aggregate), "400");
  await user.click(saveButton());
  await screen.findByText("Changes saved.");
  const settings = f.update.mock.lastCall?.[0].settings;
  expect(settings?.requestLimits).toMatchObject(limits);
  expect(settings?.allowedDestinations).toEqual([
    expect.objectContaining(destination),
    expect.objectContaining(emptyDestination),
  ]);
});

it("shows a permission failure without replaying the mutation", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await replace(user, await field(names.request), "40");
  f.setOperator(false);
  await user.click(saveButton());
  await screen.findByText(
    "Your permissions changed. This action is no longer available.",
  );
  expect(f.update).not.toHaveBeenCalled();
  expect(screen.queryByText("Changes saved.")).toBeNull();
});

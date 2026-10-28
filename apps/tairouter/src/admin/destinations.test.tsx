// SPDX-License-Identifier: Apache-2.0
import { expect, it, rs } from "@rstest/core";
import {
  cleanup,
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
  type UpdateInstanceSettingsRequest,
} from "@tairouter/client";
import { ApiProvider, DirtyContext } from "../api";
import { InstanceConfiguration } from "./operator";
const original = {
  id: "stable-rule",
  origin: "https://api.example.test",
  allowedCidrs: ["192.0.2.0/24", "2001:db8::/32"],
};
const editName = `Edit approval for ${original.origin}`;
const removeName = `Remove approval for ${original.origin}`;
function fixture(
  section: "limits" | "retention" | "destinations" = "destinations",
  failure?: Code,
  initial: Array<{ id: string; origin: string; allowedCidrs: string[] }> = [
    original,
  ],
  options: {
    revision?: string;
    requestsPerMinute?: number;
    // Holds the mutation open until resolved, to observe the pending state.
    gate?: Promise<void>;
  } = {},
) {
  let revision = options.revision ?? "1";
  const dirtyReports = rs.fn<(dirty: boolean) => void>();
  const update = rs.fn<(r: UpdateInstanceSettingsRequest) => void>();
  const settings = () => ({
    revision,
    requestLimits: {
      requestsPerMinute: options.requestsPerMinute ?? 120,
      concurrentRequests: 10,
    },
    retention: { requestDays: 30, aggregateDays: 365, auditDays: 90 },
    allowedDestinations: initial,
  });
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    router.service(OperatorService, {
      getInstanceSettings: () => ({ settings: settings() }),
      updateInstanceSettings: async (request) => {
        update(request);
        await options.gate;
        if (!request.settings)
          throw new ConnectError("Settings required", Code.InvalidArgument);
        if (failure !== undefined) throw new ConnectError("Rejected", failure);
        return { settings: { ...request.settings, revision: "2" } };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <DirtyContext.Provider value={dirtyReports}>
        <InstanceConfiguration section={section} />
      </DirtyContext.Provider>
    </ApiProvider>,
  );
  return {
    update,
    dirtyReports,
    client,
    advance: () => {
      revision = "2";
    },
  };
}
async function confirm(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    screen.getByRole("button", { name: "Review and save changes" }),
  );
  const dialog = screen.getByRole("dialog");
  await user.click(
    within(dialog).getByRole("button", { name: "Save destination policy" }),
  );
}
it("creates a destination with generated CIDR payload and shows network consequences before save", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await user.type(
    await screen.findByRole("textbox", { name: "Exact origin" }),
    "http://private.example.test:8080",
  );
  await user.type(
    screen.getByRole("textbox", { name: "Allowed networks (optional)" }),
    "10.0.0.0/8,\n2001:db8:1::/48",
  );
  await user.click(screen.getByRole("button", { name: "Add to review" }));
  await user.click(
    screen.getByRole("button", { name: "Review and save changes" }),
  );
  const dialog = screen.getByRole("dialog");
  expect(within(dialog).getByText("10.0.0.0/8").tagName).toBe("LI");
  expect(within(dialog).getByText("2001:db8:1::/48")).toBeTruthy();
  expect(within(dialog).getByText("Added")).toBeTruthy();
  expect(
    within(dialog).getAllByText(/including public HTTPS/).length,
  ).toBeGreaterThan(0);
  expect(f.update).not.toHaveBeenCalled();
  await user.click(
    within(dialog).getByRole("button", { name: "Save destination policy" }),
  );
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  expect(f.update.mock.lastCall?.[0]).toMatchObject({
    expectedRevision: "1",
    settings: {
      allowedDestinations: [
        original,
        {
          id: "",
          origin: "http://private.example.test:8080",
          allowedCidrs: ["10.0.0.0/8", "2001:db8:1::/48"],
        },
      ],
    },
  });
});
it("preserves stable ID and networks when editing only the origin", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: editName }));
  const input = screen.getByRole("textbox", { name: "Exact origin" });
  await user.clear(input);
  await user.type(input, "https://replacement.example.test");
  await user.click(screen.getByRole("button", { name: "Update review" }));
  await confirm(user);
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  expect(f.update.mock.lastCall?.[0].settings?.allowedDestinations).toEqual([
    expect.objectContaining({
      ...original,
      origin: "https://replacement.example.test",
    }),
  ]);
});
it("explicitly clears CIDRs without removing the stable origin approval", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: editName }));
  await user.clear(
    screen.getByRole("textbox", { name: "Allowed networks (optional)" }),
  );
  await user.click(screen.getByRole("button", { name: "Update review" }));
  await user.click(
    screen.getByRole("button", { name: "Review and save changes" }),
  );
  const dialog = screen.getByRole("dialog");
  expect(
    within(dialog).getByText(
      "Network restrictions removed. Clearing previous networks broadens access for this origin.",
    ),
  ).toBeTruthy();
  await user.click(
    within(dialog).getByRole("button", { name: "Save destination policy" }),
  );
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  expect(f.update.mock.lastCall?.[0].settings?.allowedDestinations).toEqual([
    expect.objectContaining({ ...original, allowedCidrs: [] }),
  ]);
});
it.each(["limits", "retention"] as const)(
  "preserves all destination fields on %s changes",
  async (section) => {
    const f = fixture(section);
    const user = userEvent.setup();
    const input = await screen.findByRole("spinbutton", {
      name: section === "limits" ? "Requests per minute" : /Request metadata/,
    });
    await user.clear(input);
    await user.type(input, section === "limits" ? "200" : "40");
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
    expect(f.update.mock.lastCall?.[0].settings?.allowedDestinations).toEqual([
      expect.objectContaining(original),
    ]);
  },
);
it("preserves CIDR drafts and the original CAS revision across conflicts and refetches", async () => {
  const f = fixture("destinations", Code.Aborted);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: editName }));
  const networks = screen.getByRole("textbox", {
    name: "Allowed networks (optional)",
  });
  await user.clear(networks);
  await user.type(networks, "198.51.100.0/24");
  await user.click(screen.getByRole("button", { name: "Update review" }));
  f.advance();
  await f.client.invalidateQueries();
  await confirm(user);
  await within(screen.getByRole("dialog")).findByText(
    "Your draft and network entries are preserved while you stay on this page. The saved policy changed. Reloading discards your unsaved draft. After reload, re-enter your changes and review the complete policy before saving.",
  );
  // The conflict never offers adoption of the latest revision.
  expect(screen.queryByRole("button", { name: /reload|latest/i })).toBeNull();
  expect(f.update.mock.lastCall?.[0]).toMatchObject({
    expectedRevision: "1",
    settings: {
      allowedDestinations: [
        { id: original.id, allowedCidrs: ["198.51.100.0/24"] },
      ],
    },
  });
  await f.client.invalidateQueries();
  expect(screen.getByRole("dialog")).toBeTruthy();
  expect(f.update).toHaveBeenCalledTimes(1);
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }),
  );
  await user.click(screen.getByRole("button", { name: editName }));
  expect(
    (
      screen.getByRole("textbox", {
        name: "Allowed networks (optional)",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("198.51.100.0/24");
});
it("keeps rejected network input available for correction instead of claiming success", async () => {
  const f = fixture("destinations", Code.InvalidArgument);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: editName }));
  await user.clear(
    screen.getByRole("textbox", { name: "Allowed networks (optional)" }),
  );
  await user.type(
    screen.getByRole("textbox", { name: "Allowed networks (optional)" }),
    "invalid-prefix",
  );
  await user.click(screen.getByRole("button", { name: "Update review" }));
  await confirm(user);
  await within(screen.getByRole("dialog")).findByRole("alert");
  expect(
    within(screen.getByRole("dialog")).getByText(
      "Check the exact origins and CIDR prefixes. Duplicate canonical origins and invalid network prefixes are not accepted.",
    ),
  ).toBeTruthy();
  expect(
    f.update.mock.lastCall?.[0].settings?.allowedDestinations[0].allowedCidrs,
  ).toEqual(["invalid-prefix"]);
  expect(screen.queryByText("Changes saved.")).toBeNull();
  await user.click(
    within(screen.getByRole("dialog")).getByRole("button", { name: "Cancel" }),
  );
  await user.click(screen.getByRole("button", { name: editName }));
  expect(
    (
      screen.getByRole("textbox", {
        name: "Allowed networks (optional)",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("invalid-prefix");
});

async function typeOrigin(
  user: ReturnType<typeof userEvent.setup>,
  value: string,
) {
  await user.type(
    await screen.findByRole("textbox", { name: "Exact origin" }),
    value,
  );
}
it("renders the page, policy card state and each staged rule with full values", async () => {
  fixture("destinations", undefined, [
    original,
    { id: "open-rule", origin: "http://localhost:8080", allowedCidrs: [] },
  ]);
  expect(
    await screen.findByRole("heading", {
      level: 1,
      name: "Approved destinations",
    }),
  ).toBeTruthy();
  expect(
    screen.getByText("Control additional origins for the whole instance."),
  ).toBeTruthy();
  expect(screen.getByText("Instance policy")).toBeTruthy();
  const notice = screen.getByRole("region", {
    name: "Public HTTPS is allowed by default",
  });
  expect(notice.textContent).toMatch(/every resolved address/);
  expect(notice.textContent).toMatch(
    /cannot fall back to the default public-HTTPS rule/,
  );
  expect(
    await screen.findByRole("heading", { name: "Policy draft" }),
  ).toBeTruthy();
  expect(screen.getByText("Matches saved policy")).toBeTruthy();
  expect(
    screen.getByText("Changes take effect only after review and save."),
  ).toBeTruthy();
  expect(screen.getByText("Restricted networks")).toBeTruthy();
  expect(screen.getByText("192.0.2.0/24").tagName).toBe("LI");
  expect(screen.getByText("2001:db8::/32")).toBeTruthy();
  expect(screen.getByText("Exact-origin approval")).toBeTruthy();
  expect(screen.getByText("No CIDR narrowing.")).toBeTruthy();
  expect(screen.queryByText("stable-rule")).toBeNull();
  expect(
    screen.getByRole("button", {
      name: "Edit approval for http://localhost:8080",
    }),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", {
      name: "Remove approval for http://localhost:8080",
    }),
  ).toBeTruthy();
  expect(
    (
      screen.getByRole("button", {
        name: "Review and save changes",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  // No control takes focus on page load.
  expect(document.activeElement).toBe(document.body);
});
it("derives the draft state from staged changes and editor inputs", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await typeOrigin(user, "http://p");
  expect(screen.getByText("Destination edit in progress")).toBeTruthy();
  expect(screen.queryByText("Unsaved changes")).toBeNull();
  await user.clear(screen.getByRole("textbox", { name: "Exact origin" }));
  expect(screen.getByText("Matches saved policy")).toBeTruthy();
  await user.click(screen.getByRole("button", { name: removeName }));
  expect(screen.getByText("Unsaved changes")).toBeTruthy();
  expect(screen.getByText("No additional destinations approved.")).toBeTruthy();
  expect(
    screen.getByText(/Public HTTPS remains allowed by default/),
  ).toBeTruthy();
  expect(f.update).not.toHaveBeenCalled();
});
it("keeps review disabled and ignores Enter while a destination edit is active", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: removeName }));
  const review = screen.getByRole("button", {
    name: "Review and save changes",
  });
  expect((review as HTMLButtonElement).disabled).toBe(false);
  await typeOrigin(user, "http://p{Enter}");
  expect((review as HTMLButtonElement).disabled).toBe(true);
  expect(
    screen.getByText("Finish or cancel the destination edit before review."),
  ).toBeTruthy();
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(f.update).not.toHaveBeenCalled();
  await user.click(
    screen.getByRole("button", { name: "Cancel destination edit" }),
  );
  expect((review as HTMLButtonElement).disabled).toBe(false);
  expect(
    screen.queryByText("Finish or cancel the destination edit before review."),
  ).toBeNull();
});
it("disables other records while an edit is selected even when both fields are cleared", async () => {
  fixture("destinations", undefined, [
    original,
    { id: "other", origin: "http://other.example.test", allowedCidrs: [] },
  ]);
  const user = userEvent.setup();
  await screen.findByRole("button", {
    name: "Edit approval for http://other.example.test",
  });
  await user.click(screen.getByRole("button", { name: editName }));
  await user.clear(screen.getByRole("textbox", { name: "Exact origin" }));
  await user.clear(
    screen.getByRole("textbox", { name: "Allowed networks (optional)" }),
  );
  for (const name of [
    editName,
    removeName,
    "Edit approval for http://other.example.test",
    "Remove approval for http://other.example.test",
    "Review and save changes",
  ])
    expect(
      (screen.getByRole("button", { name }) as HTMLButtonElement).disabled,
    ).toBe(true);
  expect(
    (screen.getByRole("button", { name: "Update review" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});
it.each([
  ["empty value", ""],
  ["trailing slash", "https://api.example.test/"],
  ["path", "https://api.example.test/v1"],
  ["query", "https://api.example.test?x=1"],
  ["fragment", "https://api.example.test#x"],
  ["credentials", "https://user:pass@api.example.test"],
  ["ftp scheme", "ftp://files.example.test"],
  ["default port", "https://api.example.test:443"],
])("does not stage an invalid origin: %s", async (_name, value) => {
  fixture();
  const user = userEvent.setup();
  const input = await screen.findByRole("textbox", { name: "Exact origin" });
  if (value) await user.type(input, value);
  expect(
    (screen.getByRole("button", { name: "Add to review" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  if (value) {
    const error = screen.getByText(/Enter an exact http or https origin/);
    expect(input.getAttribute("aria-invalid")).toBe("true");
    expect(input.getAttribute("aria-describedby")).toContain(error.id);
    // User text is never rewritten to become valid.
    expect((input as HTMLInputElement).value).toBe(value);
  }
});
it("rejects a duplicate staged origin with an associated message", async () => {
  fixture();
  const user = userEvent.setup();
  await typeOrigin(user, original.origin);
  const input = screen.getByRole("textbox", { name: "Exact origin" });
  const error = screen.getByText(/already in the draft/);
  expect(input.getAttribute("aria-describedby")).toContain(error.id);
  expect(
    (screen.getByRole("button", { name: "Add to review" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});
it("shows editor copy and tokenises networks by whitespace and commas", async () => {
  const f = fixture();
  const user = userEvent.setup();
  expect(
    await screen.findByRole("heading", { name: "Add destination" }),
  ).toBeTruthy();
  expect(
    screen.getByText(
      "Scheme, hostname and port only. No path, credentials, query or fragment.",
    ),
  ).toBeTruthy();
  expect(
    screen.getByText("CIDR ranges, one per line or separated by commas."),
  ).toBeTruthy();
  expect(
    screen.getByText(
      "Leaving networks empty removes network narrowing, including for public HTTPS.",
    ),
  ).toBeTruthy();
  expect(screen.getByText("Updates the draft only.")).toBeTruthy();
  await typeOrigin(user, "http://private.example.test:8080");
  await user.type(
    screen.getByRole("textbox", { name: "Allowed networks (optional)" }),
    "10.0.0.0/8 ,, 2001:db8:1::/48\n\n 198.51.100.0/24",
  );
  await user.click(screen.getByRole("button", { name: "Add to review" }));
  expect(screen.getByText("10.0.0.0/8")).toBeTruthy();
  expect(screen.getByText("2001:db8:1::/48")).toBeTruthy();
  expect(screen.getByText("198.51.100.0/24")).toBeTruthy();
  expect(f.update).not.toHaveBeenCalled();
});
it("moves focus for edit, cancel and staging without stealing it on load", async () => {
  fixture();
  const user = userEvent.setup();
  const scroll = rs.fn();
  Element.prototype.scrollIntoView = scroll;
  await user.click(await screen.findByRole("button", { name: editName }));
  expect(document.activeElement).toBe(
    screen.getByRole("textbox", { name: "Exact origin" }),
  );
  expect(
    screen.getByRole("heading", { name: "Edit destination" }),
  ).toBeTruthy();
  expect(screen.getByText(`Editing ${original.origin}`)).toBeTruthy();
  expect(scroll).toHaveBeenCalled();
  await user.click(
    screen.getByRole("button", { name: "Cancel destination edit" }),
  );
  expect(document.activeElement).toBe(
    screen.getByRole("button", { name: editName }),
  );
  // Staging returns focus to the staged record heading with a polite status.
  await typeOrigin(user, "http://localhost:8080");
  await user.click(screen.getByRole("button", { name: "Add to review" }));
  expect(document.activeElement).toBe(
    screen.getByRole("heading", { name: "http://localhost:8080" }),
  );
  expect(
    screen
      .getByText("Added http://localhost:8080 in the draft. Not saved yet.")
      .getAttribute("role"),
  ).toBe("status");
});
it("shows named loading and unavailable states without a usable form", async () => {
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    router.service(OperatorService, { getInstanceSettings: () => ({}) });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <InstanceConfiguration section="destinations" />
    </ApiProvider>,
  );
  expect(screen.getByText("Loading destination policy…")).toBeTruthy();
  expect(
    await screen.findByText("Instance settings are unavailable."),
  ).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Review and save changes" }),
  ).toBeNull();
});
it("reports a failed settings query with a retry", async () => {
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    router.service(OperatorService, {
      getInstanceSettings: () => {
        throw new ConnectError("down", Code.Unavailable);
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  render(
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <InstanceConfiguration section="destinations" />
    </ApiProvider>,
  );
  expect(await screen.findByRole("button", { name: "Try again" })).toBeTruthy();
  expect(
    screen.queryByRole("button", { name: "Review and save changes" }),
  ).toBeNull();
});

const rules = [
  original,
  { id: "keep", origin: "http://keep.example.test", allowedCidrs: [] },
  {
    id: "drop-restricted",
    origin: "https://drop.example.test",
    allowedCidrs: ["198.51.100.0/24"],
  },
  {
    id: "drop-open",
    origin: "http://drop-open.example.test",
    allowedCidrs: [],
  },
];
async function openReview(user: ReturnType<typeof userEvent.setup>) {
  await user.click(
    screen.getByRole("button", { name: "Review and save changes" }),
  );
  return screen.getByRole("dialog");
}
it("lists the complete resulting policy with Added, Changed and Unchanged labels and removals", async () => {
  const f = fixture("destinations", undefined, rules);
  const user = userEvent.setup();
  // Change the origin and networks of the first rule by stable ID.
  await user.click(await screen.findByRole("button", { name: editName }));
  const origin = screen.getByRole("textbox", { name: "Exact origin" });
  await user.clear(origin);
  await user.type(origin, "https://moved.example.test");
  const networks = screen.getByRole("textbox", {
    name: "Allowed networks (optional)",
  });
  await user.clear(networks);
  await user.type(networks, "203.0.113.0/24");
  await user.click(screen.getByRole("button", { name: "Update review" }));
  // Add two new unsaved rules (empty server IDs).
  for (const value of [
    "http://new-a.example.test",
    "http://new-b.example.test",
  ]) {
    await typeOrigin(user, value);
    await user.click(screen.getByRole("button", { name: "Add to review" }));
  }
  // Remove two saved rules.
  await user.click(
    screen.getByRole("button", {
      name: "Remove approval for https://drop.example.test",
    }),
  );
  await user.click(
    screen.getByRole("button", {
      name: "Remove approval for http://drop-open.example.test",
    }),
  );
  const dialog = await openReview(user);
  expect(
    within(dialog).getByRole("heading", { name: "Save destination policy" }),
  ).toBeTruthy();
  expect(
    within(dialog).getByText("Review the complete policy before saving."),
  ).toBeTruthy();
  expect(within(dialog).getByText("Draft · not saved")).toBeTruthy();
  expect(
    within(dialog).getByRole("region", {
      name: "Default policy remains active",
    }).textContent,
  ).toMatch(
    /An explicit CIDR restriction blocks addresses outside its networks/,
  );
  const result = within(dialog).getByRole("region", {
    name: "Additional destinations after save",
  });
  const items = within(result)
    .getAllByRole("listitem", {
      // Only the rule rows, not the nested CIDR lines.
    })
    .filter((item) => item.hasAttribute("data-change"));
  expect(items.map((item) => item.getAttribute("data-change"))).toEqual([
    "changed",
    "unchanged",
    "added",
    "added",
  ]);
  expect(
    within(items[0]!).getByText("Previously https://api.example.test"),
  ).toBeTruthy();
  expect(within(items[0]!).getByText("Before")).toBeTruthy();
  expect(within(items[0]!).getByText("After")).toBeTruthy();
  expect(within(items[0]!).getByText("192.0.2.0/24")).toBeTruthy();
  expect(within(items[0]!).getByText("203.0.113.0/24")).toBeTruthy();
  expect(
    within(items[1]!).getByText("Exact-origin approval · no CIDR narrowing"),
  ).toBeTruthy();
  expect(
    within(items[2]!).getByText("Exact-origin approval · no CIDR narrowing"),
  ).toBeTruthy();
  const removals = within(dialog).getByRole("region", {
    name: "Approvals to remove",
  });
  expect(
    within(removals).getByText(
      "Future connections that require this approval will be blocked.",
    ),
  ).toBeTruthy();
  expect(within(removals).getByText("https://drop.example.test")).toBeTruthy();
  expect(within(removals).getByText("198.51.100.0/24")).toBeTruthy();
  expect(
    within(removals).getByText(
      "Removing a CIDR restriction on a public HTTPS origin returns that origin to the default public-HTTPS policy.",
    ),
  ).toBeTruthy();
  expect(
    within(removals).getByText("http://drop-open.example.test"),
  ).toBeTruthy();
  // The removed origins never appear in the resulting list.
  expect(within(result).queryByText("https://drop.example.test")).toBeNull();
  expect(
    within(dialog).getByText(
      "Nothing changes until the server confirms the save.",
    ),
  ).toBeTruthy();
  expect(f.update).not.toHaveBeenCalled();
  await user.click(
    within(dialog).getByRole("button", { name: "Save destination policy" }),
  );
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  expect(f.update.mock.lastCall?.[0]).toMatchObject({
    expectedRevision: "1",
    settings: {
      requestLimits: { requestsPerMinute: 120, concurrentRequests: 10 },
      retention: { requestDays: 30, aggregateDays: 365, auditDays: 90 },
      allowedDestinations: [
        {
          id: original.id,
          origin: "https://moved.example.test",
          allowedCidrs: ["203.0.113.0/24"],
        },
        rules[1],
        { id: "", origin: "http://new-a.example.test", allowedCidrs: [] },
        { id: "", origin: "http://new-b.example.test", allowedCidrs: [] },
      ],
    },
  });
});
it("hides the removals section when nothing is removed", async () => {
  fixture();
  const user = userEvent.setup();
  await typeOrigin(user, "http://localhost:8080");
  await user.click(screen.getByRole("button", { name: "Add to review" }));
  const dialog = await openReview(user);
  expect(within(dialog).queryByText("Approvals to remove")).toBeNull();
  expect(
    within(dialog).queryByText(/Future connections that require this approval/),
  ).toBeNull();
});
it("sends an explicit clear-all request and explains the result", async () => {
  const f = fixture();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: removeName }));
  const dialog = await openReview(user);
  expect(
    within(dialog).getByText(
      "No additional destinations will remain approved. Public HTTPS remains allowed by default; private or local destinations that need approval will be blocked.",
    ),
  ).toBeTruthy();
  const removals = within(dialog).getByRole("region", {
    name: "Approvals to remove",
  });
  expect(within(removals).getByText(original.origin)).toBeTruthy();
  await user.click(
    within(dialog).getByRole("button", { name: "Save destination policy" }),
  );
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  expect(f.update.mock.lastCall?.[0].settings?.allowedDestinations).toEqual([]);
  expect(f.update.mock.lastCall?.[0].expectedRevision).toBe("1");
});
it("uses a neutral Cancel, a blue primary action and focuses Cancel first", async () => {
  fixture();
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: removeName }));
  const dialog = await openReview(user);
  const save = within(dialog).getByRole("button", {
    name: "Save destination policy",
  });
  expect(save.className).toContain("admin-primary");
  expect(save.className).not.toContain("admin-danger");
  const cancel = within(dialog).getByRole("button", { name: "Cancel" });
  expect(cancel.className).not.toContain("admin-danger");
  expect(document.activeElement).toBe(cancel);
});
it.each(["Cancel", "Close dialog", "Escape"])(
  "dismisses the review with %s, keeps the draft and returns focus",
  async (how) => {
    const f = fixture();
    const user = userEvent.setup();
    await user.click(await screen.findByRole("button", { name: removeName }));
    const dialog = await openReview(user);
    if (how === "Escape") await user.keyboard("{Escape}");
    else await user.click(within(dialog).getByRole("button", { name: how }));
    expect(screen.queryByRole("dialog")).toBeNull();
    expect(f.update).not.toHaveBeenCalled();
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    expect(document.activeElement).toBe(
      screen.getByRole("button", { name: "Review and save changes" }),
    );
  },
);
it("announces the pending save and ignores duplicate submits", async () => {
  let release: () => void = () => {};
  const gate = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = fixture("destinations", undefined, [original], { gate });
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: removeName }));
  const dialog = await openReview(user);
  await user.click(
    within(dialog).getByRole("button", { name: "Save destination policy" }),
  );
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  const pending = within(dialog).getByText(
    "Saving… Closing a dialog does not cancel a submitted change.",
  );
  expect(pending.getAttribute("role")).toBe("status");
  const busy = within(dialog).getByRole("button", { name: "Saving…" });
  expect((busy as HTMLButtonElement).disabled).toBe(true);
  await user.click(busy);
  expect(f.update).toHaveBeenCalledTimes(1);
  release();
  await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
  expect(await screen.findByText("Matches saved policy")).toBeTruthy();
  expect(f.update).toHaveBeenCalledTimes(1);
});
it("recovers from a conflict only through a fresh load, re-entry and a new review", async () => {
  const f = fixture("destinations", Code.Aborted);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: editName }));
  const networks = screen.getByRole("textbox", {
    name: "Allowed networks (optional)",
  });
  await user.clear(networks);
  await user.type(networks, "198.51.100.0/24");
  await user.click(screen.getByRole("button", { name: "Update review" }));
  await confirm(user);
  await within(screen.getByRole("dialog")).findByText(
    /Reloading discards your unsaved draft/,
  );
  // The unsaved-change guard stays active, so a reload asks for confirmation.
  expect(f.dirtyReports.mock.lastCall?.[0]).toBe(true);
  expect(f.update).toHaveBeenCalledTimes(1);
  // A confirmed full reload discards the old draft and mounts a fresh form
  // from the latest server snapshot.
  cleanup();
  const latest = {
    id: original.id,
    origin: original.origin,
    allowedCidrs: ["203.0.113.0/24"],
  };
  const g = fixture("destinations", undefined, [latest], {
    revision: "2",
    requestsPerMinute: 240,
  });
  await screen.findByRole("heading", { name: "Policy draft" });
  expect(screen.getByText("Matches saved policy")).toBeTruthy();
  expect(screen.getByText("203.0.113.0/24")).toBeTruthy();
  expect(screen.queryByText("198.51.100.0/24")).toBeNull();
  expect(
    (
      screen.getByRole("button", {
        name: "Review and save changes",
      }) as HTMLButtonElement
    ).disabled,
  ).toBe(true);
  // Re-enter the change, review the complete policy and confirm explicitly.
  await user.click(screen.getByRole("button", { name: editName }));
  const again = screen.getByRole("textbox", {
    name: "Allowed networks (optional)",
  });
  await user.clear(again);
  await user.type(again, "198.51.100.0/24");
  await user.click(screen.getByRole("button", { name: "Update review" }));
  expect(g.update).not.toHaveBeenCalled();
  await confirm(user);
  await waitFor(() => expect(g.update).toHaveBeenCalledTimes(1));
  expect(g.update.mock.lastCall?.[0]).toMatchObject({
    expectedRevision: "2",
    settings: {
      requestLimits: { requestsPerMinute: 240, concurrentRequests: 10 },
      allowedDestinations: [
        { id: original.id, allowedCidrs: ["198.51.100.0/24"] },
      ],
    },
  });
});
it("dismisses the review and form after losing instance operator authority", async () => {
  const f = fixture("destinations", Code.PermissionDenied);
  const user = userEvent.setup();
  await user.click(await screen.findByRole("button", { name: removeName }));
  await confirm(user);
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  expect(f.update.mock.lastCall?.[0].expectedRevision).toBe("1");
  expect(
    within(screen.getByRole("dialog")).getByText(
      "Your permissions changed. This action is no longer available.",
    ),
  ).toBeTruthy();
});

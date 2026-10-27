// SPDX-License-Identifier: Apache-2.0
import { expect, it, rs } from "@rstest/core";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  OperatorService,
  type UpdateInstanceSettingsRequest,
} from "@aiprouter/client";
import { ApiProvider } from "../api";
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
) {
  let revision = "1";
  const update = rs.fn<(r: UpdateInstanceSettingsRequest) => void>();
  const settings = () => ({
    revision,
    requestLimits: { requestsPerMinute: 120, concurrentRequests: 10 },
    retention: { requestDays: 30, aggregateDays: 365, auditDays: 90 },
    allowedDestinations: initial,
  });
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    router.service(OperatorService, {
      getInstanceSettings: () => ({ settings: settings() }),
      updateInstanceSettings: (request) => {
        update(request);
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
      <InstanceConfiguration section={section} />
    </ApiProvider>,
  );
  return {
    update,
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
  expect(
    within(dialog).getByText(/Restricted to 10.0.0.0\/8, 2001:db8:1::\/48/),
  ).toBeTruthy();
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
    within(dialog).getByText(/Clearing previous networks broadens access/),
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
    /Your draft and network entries are preserved/,
  );
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

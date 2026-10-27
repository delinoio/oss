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
function fixture(
  section: "limits" | "retention" | "destinations" = "destinations",
  failure?: Code,
) {
  let revision = "1";
  const update = rs.fn<(r: UpdateInstanceSettingsRequest) => void>();
  const settings = () => ({
    revision,
    requestLimits: { requestsPerMinute: 120, concurrentRequests: 10 },
    retention: { requestDays: 30, aggregateDays: 365, auditDays: 90 },
    allowedDestinations: [original],
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
  await user.click(
    await screen.findByRole("button", { name: "Edit approval" }),
  );
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
  await user.click(
    await screen.findByRole("button", { name: "Edit approval" }),
  );
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
    await user.click(
      screen.getByRole("button", {
        name: section === "limits" ? "Save changes" : "Review and save changes",
      }),
    );
    await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
    expect(f.update.mock.lastCall?.[0].settings?.allowedDestinations).toEqual([
      expect.objectContaining(original),
    ]);
  },
);
it("preserves CIDR drafts and the original CAS revision across conflicts and refetches", async () => {
  const f = fixture("destinations", Code.Aborted);
  const user = userEvent.setup();
  await user.click(
    await screen.findByRole("button", { name: "Edit approval" }),
  );
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
  await user.click(screen.getByRole("button", { name: "Edit approval" }));
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
  await user.click(
    await screen.findByRole("button", { name: "Edit approval" }),
  );
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
  await user.click(screen.getByRole("button", { name: "Edit approval" }));
  expect(
    (
      screen.getByRole("textbox", {
        name: "Allowed networks (optional)",
      }) as HTMLTextAreaElement
    ).value,
  ).toBe("invalid-prefix");
});

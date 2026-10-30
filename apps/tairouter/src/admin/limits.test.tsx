// SPDX-License-Identifier: Apache-2.0
import { expect, it, rs } from "@rstest/core";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { QueryClient } from "@tanstack/react-query";
import {
  AccountService,
  OperatorService,
  type InstanceSettings,
  type UpdateInstanceSettingsRequest,
} from "@tairouter/client";
import { ApiProvider, DirtyContext } from "../api";
import { InstanceConfiguration } from "./operator";

const destination = {
  id: "stable-rule",
  origin: "https://api.example.test",
  allowedCidrs: ["192.0.2.0/24", "2001:db8::/32"],
};
const retention = { requestDays: 30, aggregateDays: 365, auditDays: 90 };

type Options = {
  limits?: { requestsPerMinute: number; concurrentRequests: number };
  missingLimits?: boolean;
  noSettings?: boolean;
  failure?: Code;
  settingsFailure?: boolean;
  delay?: Promise<void>;
};

function fixture(options: Options = {}) {
  let revision = "1";
  let limits = options.limits ?? {
    requestsPerMinute: 120,
    concurrentRequests: 10,
  };
  const update = rs.fn<(r: UpdateInstanceSettingsRequest) => void>();
  const dirty = rs.fn<(dirty: boolean) => void>();
  const settings = () =>
    ({
      revision,
      requestLimits: options.missingLimits ? undefined : limits,
      retention,
      allowedDestinations: [destination],
    }) as unknown as InstanceSettings;
  const transport = createRouterTransport((router) => {
    router.service(AccountService, {
      getSession: () => ({ isInstanceOperator: true }),
    });
    router.service(OperatorService, {
      getInstanceSettings: () => {
        if (options.settingsFailure)
          throw new ConnectError("down", Code.Unavailable);
        return { settings: options.noSettings ? undefined : settings() };
      },
      updateInstanceSettings: async (request) => {
        update(request);
        await options.delay;
        if (options.failure !== undefined)
          throw new ConnectError("Rejected", options.failure);
        return { settings: { ...request.settings!, revision: "2" } };
      },
    });
  });
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <ApiProvider queryClient={client} rpcTransport={transport}>
      <DirtyContext.Provider value={dirty}>
        <InstanceConfiguration section="limits" />
      </DirtyContext.Provider>
    </ApiProvider>,
  );
  return {
    update,
    dirty,
    client,
    advance: () => {
      revision = "2";
      limits = { requestsPerMinute: 999, concurrentRequests: 99 };
    },
  };
}
const rpm = () =>
  screen.findByRole("spinbutton", { name: "Requests per minute" });
const concurrent = () =>
  screen.getByRole("spinbutton", { name: "Concurrent requests" });
async function replace(
  user: ReturnType<typeof userEvent.setup>,
  input: HTMLElement,
  value: string,
) {
  await user.clear(input);
  if (value) await user.type(input, value);
}

it("renders the per-user layout with server values and a disabled save", async () => {
  fixture({ limits: { requestsPerMinute: 321, concurrentRequests: 22 } });
  const input = (await rpm()) as HTMLInputElement;
  expect(screen.getAllByRole("heading", { level: 1 })).toHaveLength(1);
  expect(
    screen.getByRole("heading", { level: 1, name: "Request limits" }),
  ).toBeTruthy();
  expect(
    screen.getByText("Applied per user within each organization."),
  ).toBeTruthy();
  expect(
    screen.getByRole("heading", { level: 2, name: "Per-user limits" }),
  ).toBeTruthy();
  expect(
    screen.getByText(
      "Set the request rate and concurrency allowed for each user in each organization.",
    ),
  ).toBeTruthy();
  expect(input.value).toBe("321");
  expect(input.required).toBe(true);
  expect(input.min).toBe("1");
  expect(input.max).toBe("6000");
  expect(input.step).toBe("1");
  expect((concurrent() as HTMLInputElement).value).toBe("22");
  expect(concurrent().getAttribute("max")).toBe("100");
  expect(screen.getByText("requests / min")).toBeTruthy();
  expect(screen.getByText("requests")).toBeTruthy();
  expect(
    screen.getByText("1–6,000 requests per minute. Default: 120."),
  ).toBeTruthy();
  expect(
    screen.getByText("1–100 concurrent requests. Default: 10."),
  ).toBeTruthy();
  expect(screen.getByText("How these limits apply")).toBeTruthy();
  expect(
    screen.getByText(
      "Each user has a separate allowance within each organization.",
    ),
  ).toBeTruthy();
  expect(screen.getByText("No unsaved changes")).toBeTruthy();
  expect(
    (screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
  expect(screen.queryByRole("button", { name: /cancel|reset/i })).toBeNull();
  expect(screen.queryByText(/unlimited/i)).toBeNull();
});

it.each([
  ["1", "1", 1, 1],
  ["6000", "100", 6000, 100],
  ["200", "20", 200, 20],
])(
  "saves %s/%s with the complete settings and original revision once",
  async (a, b, requests, concurrentRequests) => {
    const f = fixture();
    const user = userEvent.setup();
    await replace(user, await rpm(), a);
    await replace(user, concurrent(), b);
    expect(screen.getByText("Unsaved changes")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    await screen.findByText("Changes saved.");
    expect(f.update).toHaveBeenCalledTimes(1);
    expect(f.update.mock.lastCall?.[0]).toMatchObject({
      expectedRevision: "1",
      settings: {
        requestLimits: { requestsPerMinute: requests, concurrentRequests },
        retention,
        allowedDestinations: [destination],
      },
    });
    expect(screen.getByText("No unsaved changes")).toBeTruthy();
    expect(f.dirty).toHaveBeenLastCalledWith(false);
  },
);

it.each([
  ["", "10", /1 to 6,000/],
  ["0", "10", /1 to 6,000/],
  ["-1", "10", /1 to 6,000/],
  ["6001", "10", /1 to 6,000/],
  ["1.5", "10", /1 to 6,000/],
  ["120", "101", /1 to 100/],
  ["120", "0", /1 to 100/],
])(
  "rejects %s/%s without a mutation and focuses the error summary",
  async (a, b, message) => {
    const f = fixture();
    const user = userEvent.setup();
    await replace(user, await rpm(), a);
    await replace(user, concurrent(), b);
    await user.click(screen.getByRole("button", { name: "Save changes" }));
    const summary = await screen.findByRole("alert");
    await waitFor(() => expect(document.activeElement).toBe(summary));
    expect(summary.textContent).toMatch(message);
    const invalid = screen
      .getAllByRole("spinbutton")
      .find((input) => input.getAttribute("aria-invalid") === "true")!;
    const described = invalid.getAttribute("aria-describedby")!;
    expect(
      described
        .split(" ")
        .some((id) => document.getElementById(id)?.textContent?.match(message)),
    ).toBe(true);
    expect(f.update).not.toHaveBeenCalled();
  },
);

it("sends one mutation for repeated save activation while pending", async () => {
  let release: () => void = () => {};
  const delay = new Promise<void>((resolve) => {
    release = resolve;
  });
  const f = fixture({ delay });
  const user = userEvent.setup();
  const input = await rpm();
  await replace(user, input, "200");
  await user.click(screen.getByRole("button", { name: "Save changes" }));
  await user.type(input, "{Enter}{Enter}");
  await user.click(screen.getByRole("button", { name: "Save changes" }));
  await screen.findByText(/Saving…/);
  expect(screen.queryByText("Changes saved.")).toBeNull();
  await waitFor(() => expect(f.update).toHaveBeenCalledTimes(1));
  release();
  await screen.findByText("Changes saved.");
  expect(f.update).toHaveBeenCalledTimes(1);
});

it("keeps the draft and original revision when a newer revision is refetched", async () => {
  const f = fixture({ failure: Code.Aborted });
  const user = userEvent.setup();
  const input = (await rpm()) as HTMLInputElement;
  await replace(user, input, "200");
  f.advance();
  await f.client.invalidateQueries();
  expect(input.value).toBe("200");
  await user.click(screen.getByRole("button", { name: "Save changes" }));
  await screen.findByText(
    "This record changed. Refresh and review before saving again.",
  );
  expect(f.update).toHaveBeenCalledTimes(1);
  expect(f.update.mock.lastCall?.[0].expectedRevision).toBe("1");
  expect(input.value).toBe("200");
  expect(screen.getByText("Unsaved changes")).toBeTruthy();
  expect(screen.queryByText("Changes saved.")).toBeNull();
  expect(
    screen.queryByRole("button", { name: /refresh|rebase|retry/i }),
  ).toBeNull();
  await new Promise((resolve) => setTimeout(resolve, 50));
  expect(f.update).toHaveBeenCalledTimes(1);
});

it("keeps edit-then-revert counted as dirty", async () => {
  const f = fixture();
  const user = userEvent.setup();
  const input = await rpm();
  await replace(user, input, "200");
  await replace(user, input, "120");
  expect(screen.getByText("Unsaved changes")).toBeTruthy();
  expect(f.dirty).toHaveBeenLastCalledWith(true);
});

it("shows loading, query failure and unavailable states without editable values", async () => {
  fixture({ settingsFailure: true });
  expect(await screen.findByRole("button", { name: "Try again" })).toBeTruthy();
  expect(screen.queryByRole("spinbutton")).toBeNull();
});

it("shows unavailable settings and a disabled save when limits are missing", async () => {
  fixture({ missingLimits: true });
  await screen.findByText("Instance settings are unavailable.");
  expect(screen.queryByRole("spinbutton")).toBeNull();
  expect(
    (screen.getByRole("button", { name: "Save changes" }) as HTMLButtonElement)
      .disabled,
  ).toBe(true);
});

it("shows unavailable settings when the response has no settings", async () => {
  fixture({ noSettings: true });
  await screen.findByText("Instance settings are unavailable.");
  expect(screen.queryByRole("spinbutton")).toBeNull();
});

it("renders an accessible loading state before values arrive", () => {
  fixture();
  const loading = screen.getByRole("status");
  expect(loading.getAttribute("aria-busy")).toBe("true");
  expect(loading.textContent).toBe("Loading…");
  expect(screen.queryByRole("spinbutton")).toBeNull();
});
